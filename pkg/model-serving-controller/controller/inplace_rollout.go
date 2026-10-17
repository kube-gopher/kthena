/*
Copyright The Volcano Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"

	workloadv1alpha1 "github.com/volcano-sh/kthena/pkg/apis/workload/v1alpha1"
	"github.com/volcano-sh/kthena/pkg/model-serving-controller/datastore"
	"github.com/volcano-sh/kthena/pkg/model-serving-controller/inplace"
	"github.com/volcano-sh/kthena/pkg/model-serving-controller/utils"
)

// maxInPlaceStepsPerSync bounds how many steps one unit takes in a single sync.
// Marking and patching can follow each other at once, while verification waits for Pod events.
const maxInPlaceStepsPerSync = 3

// inPlaceUnit is one ServingGroup and its Pods during an in-place rollout.
type inPlaceUnit struct {
	group datastore.ServingGroup
	pods  []*inplace.UnitPod
	// inUpdate is true while any Pod has an unfinished update.
	inUpdate bool
	// terminating is true while any Pod is being deleted.
	terminating bool
	// missingTemplate explains a Pod that no template of the target revision renders.
	missingTemplate string
}

// outdated reports whether the unit has a Pod without the target labels or an unfinished update.
func (u *inPlaceUnit) outdated(revision string) bool {
	if u.inUpdate {
		return true
	}
	for _, p := range u.pods {
		if !p.HasTargetLabels(revision) {
			return true
		}
	}
	return false
}

// available reports whether the unit counts as available for the maxUnavailable budget.
func (u *inPlaceUnit) available() bool {
	return u.group.Status == datastore.ServingGroupRunning && !u.inUpdate
}

func (u *inPlaceUnit) rawPods() []*corev1.Pod {
	pods := make([]*corev1.Pod, 0, len(u.pods))
	for _, p := range u.pods {
		pods = append(pods, p.Pod)
	}
	return pods
}

// inPlaceRollout holds what one sync of an in-place rollout resolves once.
type inPlaceRollout struct {
	c        *ModelServingController
	ms       *workloadv1alpha1.ModelServing
	revision string
	target   *inplace.RevisionTemplates
	updater  *inplace.PodUpdater
	// sources caches the templates of each source revision, or why it cannot be updated from.
	sources map[string]sourceTemplates
}

type sourceTemplates struct {
	templates *inplace.RevisionTemplates
	stall     *inplace.StallError
}

// manageInPlaceRollout moves outdated ServingGroups to revision by patching container images on their Pods.
// It never deletes a Pod: a unit that cannot be updated in place stalls and is reported.
// All progress is read from the Pods, so the rollout resumes from the same step after a controller restart.
func (c *ModelServingController) manageInPlaceRollout(ctx context.Context, ms *workloadv1alpha1.ModelServing, groups []datastore.ServingGroup, revision string) error {
	if ms.Spec.RolloutStrategy != nil && ms.Spec.RolloutStrategy.Type == workloadv1alpha1.RoleRollingUpdate {
		// Role-instance units are not implemented yet, and the webhook rejects this combination.
		c.emitInPlaceEvent(ms, corev1.EventTypeWarning, inplace.ReasonIneligible,
			"RoleRollingUpdate cannot be combined with updateMethod InPlace yet, so outdated Roles are kept")
		return nil
	}
	if len(ms.Spec.Plugins) > 0 {
		// Plugins can change a Pod when it is created, and an image patch would not apply those changes again.
		// The webhook rejects plugins under InPlace, and the controller repeats the check in case admission was bypassed.
		c.emitInPlaceEvent(ms, corev1.EventTypeWarning, inplace.ReasonIneligible,
			"plugins cannot be used with updateMethod InPlace, so outdated ServingGroups are kept")
		return nil
	}

	maxUnavailable, err := utils.GetMaxUnavailable(ms)
	if err != nil {
		return fmt.Errorf("failed to calculate maxUnavailable: %v", err)
	}
	// An in-place update cannot surge, so a budget that resolves to 0 after a scale-subresource change still updates one unit.
	maxUnavailable = max(maxUnavailable, 1)
	partition, _, err := c.getPartition(modelServingPartition(ms), modelServingReplicas(ms))
	if err != nil {
		return fmt.Errorf("failed to calculate partition: %v", err)
	}

	r := &inPlaceRollout{
		c:        c,
		ms:       ms,
		revision: revision,
		target:   inplace.NewRevisionTemplates(ms, revision),
		updater:  inplace.NewPodUpdater(c.kubeClientSet),
		sources:  map[string]sourceTemplates{},
	}

	var units, candidates []*inPlaceUnit
	unavailable := 0
	for index, group := range groups {
		if group.Status == datastore.ServingGroupDeleting {
			continue
		}
		unit, err := r.loadUnit(group)
		if err != nil {
			return err
		}
		r.syncStoreRevision(unit)
		if !unit.available() {
			unavailable++
		}
		units = append(units, unit)
		// Partition only protects units that have not started; a unit in update finishes its target.
		if !unit.inUpdate && index >= partition && unit.outdated(revision) {
			candidates = append(candidates, unit)
		}
	}

	var errs []error
	for _, unit := range units {
		if unit.inUpdate {
			errs = append(errs, r.updateUnit(ctx, unit))
		}
	}

	// Units that are already unavailable go first, because updating them does not reduce availability.
	// Within each group, the highest ordinals go first, as in the Recreate rollout.
	for _, wantAvailable := range []bool{false, true} {
		for i := len(candidates) - 1; i >= 0; i-- {
			unit := candidates[i]
			if unit.available() != wantAvailable {
				continue
			}
			if wantAvailable && unavailable >= maxUnavailable {
				klog.V(4).Infof("No ServingGroups of ModelServing %s/%s can start an in-place update: %d unavailable, maxUnavailable=%d",
					ms.Namespace, ms.Name, unavailable, maxUnavailable)
				return errors.Join(errs...)
			}
			started, err := r.startUnit(ctx, unit)
			errs = append(errs, err)
			if started && wantAvailable {
				unavailable++
			}
		}
	}
	return errors.Join(errs...)
}

// loadUnit reads a ServingGroup's Pods owned by the ModelServing and matches each with its target template.
func (r *inPlaceRollout) loadUnit(group datastore.ServingGroup) (*inPlaceUnit, error) {
	pods, err := r.c.getPodsByIndex(GroupNameKey, fmt.Sprintf("%s/%s", r.ms.Namespace, group.Name))
	if err != nil {
		return nil, fmt.Errorf("cannot list pods of ServingGroup %s: %v", group.Name, err)
	}
	unit := &inPlaceUnit{group: group}
	for _, pod := range pods {
		if !utils.HasModelServingOwner(pod, r.ms.UID) {
			continue
		}
		// A terminating Pod is replaced by a new one, which joins the unit when it is created.
		if utils.IsPodTerminating(pod) {
			unit.terminating = true
			continue
		}
		target, ok := r.target.ForPod(pod)
		if !ok {
			unit.missingTemplate = fmt.Sprintf("pod %s belongs to no Role template of revision %s", pod.Name, r.revision)
			continue
		}
		unit.pods = append(unit.pods, &inplace.UnitPod{Pod: pod, Target: target})
	}
	unit.inUpdate = inplace.InUpdate(unit.rawPods(), r.ms.UID)
	return unit, nil
}

// startUnit starts an update of an outdated unit, unless one of its Pods cannot be updated in place.
// A unit that does not start keeps its availability slot free.
func (r *inPlaceRollout) startUnit(ctx context.Context, unit *inPlaceUnit) (bool, error) {
	if unit.terminating {
		klog.V(4).Infof("ServingGroup %s has terminating pods, so its in-place update waits", unit.group.Name)
		return false, nil
	}
	if unit.missingTemplate != "" {
		r.reportStall(unit, &inplace.StallError{Reason: inplace.ReasonIneligible, Message: unit.missingTemplate})
		return false, nil
	}
	for _, p := range unit.pods {
		stall, err := r.checkPatchable(ctx, p, utils.ObjectRevision(p.Pod))
		if err != nil {
			return false, err
		}
		if stall != nil {
			r.reportStall(unit, stall)
			return false, nil
		}
	}
	return true, r.updateUnit(ctx, unit)
}

// updateUnit takes the next steps of a unit, as derived from its Pods by inplace.PlanUnit.
func (r *inPlaceRollout) updateUnit(ctx context.Context, unit *inPlaceUnit) error {
	if unit.missingTemplate != "" {
		r.reportStall(unit, &inplace.StallError{Reason: inplace.ReasonIneligible, Message: unit.missingTemplate})
		return nil
	}
	if unit.inUpdate {
		failed, err := r.enforceProgressDeadline(ctx, unit)
		if failed || err != nil {
			return err
		}
	}
	for step := 0; step < maxInPlaceStepsPerSync; step++ {
		plan := inplace.PlanUnit(r.ms.UID, r.revision, unit.pods)
		switch plan.Step {
		case inplace.StepNone:
			return nil
		case inplace.StepStalled:
			r.reportStall(unit, &inplace.StallError{Reason: plan.Reason, Message: plan.Message})
			return nil
		case inplace.StepVerify:
			klog.V(4).Infof("ServingGroup %s of ModelServing %s/%s waits for verification: %s", unit.group.Name, r.ms.Namespace, r.ms.Name, plan.Message)
			return nil
		case inplace.StepMark:
			if err := r.mark(ctx, unit, plan.Pods); err != nil {
				return err
			}
		case inplace.StepPatch:
			patched, err := r.patch(ctx, unit, plan.Pods)
			if err != nil || !patched {
				return err
			}
		case inplace.StepComplete:
			return r.complete(ctx, unit, plan.Pods)
		}
	}
	return nil
}

// enforceProgressDeadline fails a unit that is not verified within the progress deadline, measured from when its update started.
// The unverified Pods of a failed unit are deleted, so they follow the RecoveryPolicy and are recreated on the target revision.
// This also ends an update that stalled after its Pods were marked.
// A unit whose Pods are all verified does not fail, because it completes in this sync.
func (r *inPlaceRollout) enforceProgressDeadline(ctx context.Context, unit *inPlaceUnit) (bool, error) {
	startedAt, ok := inplace.UnitStartedAt(unit.rawPods(), r.ms.UID)
	if !ok {
		return false, nil
	}
	deadline := inplace.ProgressDeadline(r.ms)
	if remaining := time.Until(startedAt.Add(deadline)); remaining > 0 {
		// No Pod event may arrive before the deadline, so the unit is checked again when it passes.
		r.c.enqueueModelServingAfter(r.ms, remaining)
		return false, nil
	}
	unverified := inplace.UnverifiedPods(r.ms.UID, r.revision, unit.pods)
	if len(unverified) == 0 {
		return false, nil
	}
	for _, p := range unverified {
		err := r.c.kubeClientSet.CoreV1().Pods(p.Pod.Namespace).Delete(ctx, p.Pod.Name, *metav1.NewPreconditionDeleteOptions(string(p.Pod.UID)))
		if err != nil && !apierrors.IsNotFound(err) {
			return true, fmt.Errorf("delete pod %s after the progress deadline: %v", p.Pod.Name, err)
		}
	}
	klog.V(2).Infof("In-place update of ServingGroup %s of ModelServing %s/%s to revision %s exceeded its progress deadline of %s, deleted %d unverified pods",
		unit.group.Name, r.ms.Namespace, r.ms.Name, r.revision, deadline, len(unverified))
	r.c.emitInPlaceEvent(r.ms, corev1.EventTypeWarning, inplace.ReasonProgressDeadlineExceeded,
		fmt.Sprintf("ServingGroup %s was not updated in place to revision %s within %s, so its %d unverified pods follow the RecoveryPolicy",
			unit.group.Name, r.revision, deadline, len(unverified)))
	return true, nil
}

// mark records the update on every given Pod, each with the revision it starts from.
func (r *inPlaceRollout) mark(ctx context.Context, unit *inPlaceUnit, pods []*inplace.UnitPod) error {
	started := !unit.inUpdate
	for _, p := range pods {
		marked, err := r.updater.Mark(ctx, p.Pod, r.ms.UID, unit.group.Name, utils.ObjectRevision(p.Pod), r.revision)
		if err != nil {
			return err
		}
		p.Pod = marked
	}
	unit.inUpdate = true
	if started {
		r.c.emitInPlaceEvent(r.ms, corev1.EventTypeNormal, "InPlaceUpdateStarted",
			fmt.Sprintf("ServingGroup %s started an in-place update to revision %s", unit.group.Name, r.revision))
	}
	return nil
}

// patch sets the target images and labels on every given Pod after checking all of them, so a unit is never left partly patched because of one bad Pod.
func (r *inPlaceRollout) patch(ctx context.Context, unit *inPlaceUnit, pods []*inplace.UnitPod) (bool, error) {
	for _, p := range pods {
		stall, err := r.checkPatchable(ctx, p, inplace.MatchingState(p.Pod).SourceRevision)
		if err != nil {
			return false, err
		}
		if stall != nil {
			r.reportStall(unit, stall)
			return false, nil
		}
	}
	for _, p := range pods {
		patched, err := r.updater.PatchImages(ctx, p.Pod, p.Target.Images(), p.Target.Labels(r.revision))
		if err != nil {
			return false, err
		}
		p.Pod = patched
	}
	r.c.emitInPlaceEvent(r.ms, corev1.EventTypeNormal, "InPlaceUpdatePatched",
		fmt.Sprintf("Patched the images of %d pods in ServingGroup %s to revision %s", len(pods), unit.group.Name, r.revision))
	return true, nil
}

// complete finishes the update of every Pod in a verified unit and moves the unit to revision in the store.
func (r *inPlaceRollout) complete(ctx context.Context, unit *inPlaceUnit, pods []*inplace.UnitPod) error {
	for _, p := range pods {
		completed, err := r.updater.Complete(ctx, p.Pod)
		if err != nil {
			return err
		}
		p.Pod = completed
	}
	unit.inUpdate = false
	r.syncStoreRevision(unit)
	r.c.emitInPlaceEvent(r.ms, corev1.EventTypeNormal, "InPlaceUpdateCompleted",
		fmt.Sprintf("ServingGroup %s completed its in-place update to revision %s", unit.group.Name, r.revision))
	// The unit releases its slot once it is Running, which the next sync observes.
	r.c.enqueueModelServing(r.ms)
	return nil
}

// checkPatchable checks that the Pod can be updated in place from sourceRevision to the target revision.
// It returns a stall for a change that waiting cannot fix, and an error only when the check itself failed.
func (r *inPlaceRollout) checkPatchable(ctx context.Context, p *inplace.UnitPod, sourceRevision string) (*inplace.StallError, error) {
	source, err := r.sourceTemplates(ctx, sourceRevision)
	if err != nil || source.stall != nil {
		return source.stall, err
	}
	template, ok := source.templates.ForPod(p.Pod)
	if !ok {
		return &inplace.StallError{Reason: inplace.ReasonIneligible,
			Message: fmt.Sprintf("pod %s belongs to no Role template of revision %s", p.Pod.Name, sourceRevision)}, nil
	}
	return inplace.CheckPatchable(p.Pod, template, p.Target), nil
}

// sourceTemplates resolves the templates of a source revision from its ControllerRevision.
// The controller repeats the admission eligibility check here, because admission can be bypassed and the source revision may predate it.
func (r *inPlaceRollout) sourceTemplates(ctx context.Context, sourceRevision string) (sourceTemplates, error) {
	if cached, ok := r.sources[sourceRevision]; ok {
		return cached, nil
	}
	var result sourceTemplates
	switch {
	case sourceRevision == r.revision:
		result.templates = r.target
	case sourceRevision == "":
		result.stall = &inplace.StallError{Reason: inplace.ReasonRevisionHistoryMissing, Message: "a pod has no revision label"}
	default:
		cr, err := utils.GetControllerRevision(ctx, r.c.kubeClientSet, r.ms, sourceRevision)
		if err != nil {
			return result, fmt.Errorf("get ControllerRevision of revision %s: %v", sourceRevision, err)
		}
		if cr == nil {
			result.stall = &inplace.StallError{Reason: inplace.ReasonRevisionHistoryMissing,
				Message: fmt.Sprintf("the ControllerRevision of revision %s is missing", sourceRevision)}
			break
		}
		source, err := utils.ModelServingAtRevision(r.ms, cr)
		if err != nil {
			result.stall = &inplace.StallError{Reason: inplace.ReasonRevisionHistoryMissing,
				Message: fmt.Sprintf("the ControllerRevision of revision %s cannot be read: %v", sourceRevision, err)}
			break
		}
		if errs := inplace.CheckEligibility(source, r.ms); len(errs) > 0 {
			result.stall = &inplace.StallError{Reason: inplace.ReasonIneligible,
				Message: fmt.Sprintf("revision %s cannot be updated in place to revision %s: %v", sourceRevision, r.revision, errs.ToAggregate())}
			break
		}
		result.templates = inplace.NewRevisionTemplates(source, sourceRevision)
	}
	r.sources[sourceRevision] = result
	return result, nil
}

// syncStoreRevision keeps the ServingGroup revision in the store equal to what its Pods show.
// A unit in update stays on its source revision, which keeps that ControllerRevision referenced and the unit counted as not updated.
// A unit whose Pods all carry the target labels without an unfinished update is on the target revision.
func (r *inPlaceRollout) syncStoreRevision(unit *inPlaceUnit) {
	want, ok := inplace.UnfinishedSourceRevision(unit.rawPods(), r.ms.UID, r.revision)
	if !ok {
		if len(unit.pods) == 0 || unit.outdated(r.revision) {
			return
		}
		want = r.revision
	}
	if unit.group.Revision == want {
		return
	}
	if err := r.c.store.UpdateServingGroupRevision(utils.GetNamespaceName(r.ms), unit.group.Name, want); err != nil {
		klog.Errorf("failed to update ServingGroup %s revision: %v", unit.group.Name, err)
		return
	}
	unit.group.Revision = want
	klog.V(2).Infof("Updated ServingGroup %s revision to %s from its pods", unit.group.Name, want)
}

func (c *ModelServingController) emitInPlaceEvent(ms *workloadv1alpha1.ModelServing, eventType, reason, message string) {
	if c.recorder == nil {
		return
	}
	c.recorder.Event(ms, eventType, reason, message)
}

func (r *inPlaceRollout) reportStall(unit *inPlaceUnit, stall *inplace.StallError) {
	klog.V(2).Infof("In-place update of ServingGroup %s of ModelServing %s/%s to revision %s stalled: %v",
		unit.group.Name, r.ms.Namespace, r.ms.Name, r.revision, stall)
	r.c.emitInPlaceEvent(r.ms, corev1.EventTypeWarning, stall.Reason,
		fmt.Sprintf("ServingGroup %s cannot be updated in place to revision %s: %s", unit.group.Name, r.revision, stall.Message))
}

// hasUnfinishedInPlaceUpdate reports whether any Pod of the ServingGroup has an unfinished in-place update.
// Such a ServingGroup is outdated under Recreate whatever its revision labels say, because its images may be patched without being verified.
func (c *ModelServingController) hasUnfinishedInPlaceUpdate(ms *workloadv1alpha1.ModelServing, groupName string) bool {
	pods, err := c.getPodsByIndex(GroupNameKey, fmt.Sprintf("%s/%s", ms.Namespace, groupName))
	if err != nil {
		klog.Errorf("cannot list pods of ServingGroup %s: %v", groupName, err)
		return false
	}
	return inplace.InUpdate(pods, ms.UID)
}
