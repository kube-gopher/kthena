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

package inplace

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	workloadv1alpha1 "github.com/volcano-sh/kthena/pkg/apis/workload/v1alpha1"
	"github.com/volcano-sh/kthena/pkg/model-serving-controller/utils"
)

// Stall reasons are reported when a unit cannot make progress until the spec or the Pods change.
const (
	ReasonIneligible             = "InPlaceUpdateIneligible"
	ReasonRevisionHistoryMissing = "RevisionHistoryMissing"
	ReasonUnexplainedImageDrift  = "UnexplainedImageDrift"
	ReasonRetargetNotSupported   = "InPlaceRetargetNotSupported"
)

// StallError explains why a unit cannot be updated in place.
type StallError struct {
	Reason  string
	Message string
}

func (e *StallError) Error() string {
	return e.Reason + ": " + e.Message
}

func stall(reason, format string, args ...interface{}) *StallError {
	return &StallError{Reason: reason, Message: fmt.Sprintf(format, args...)}
}

// templateKey identifies the Pod template a Pod is rendered from.
type templateKey struct {
	role  string
	entry bool
}

// ContainerTarget is a regular container's image and effective pull policy at a revision.
type ContainerTarget struct {
	Image           string
	ImagePullPolicy corev1.PullPolicy
}

// PodTemplate is what a Pod rendered from one template looks like at a revision.
type PodTemplate struct {
	Containers       map[string]ContainerTarget
	RoleTemplateHash string
}

// RevisionTemplates holds the Pod templates of every Role of a ModelServing at one revision.
type RevisionTemplates struct {
	Revision  string
	templates map[templateKey]PodTemplate
}

// NewRevisionTemplates resolves the Pod templates of ms, which must be the ModelServing at revision.
func NewRevisionTemplates(ms *workloadv1alpha1.ModelServing, revision string) *RevisionTemplates {
	result := &RevisionTemplates{Revision: revision, templates: map[templateKey]PodTemplate{}}
	for i := range ms.Spec.Template.Roles {
		role := &ms.Spec.Template.Roles[i]
		hash := utils.CalRoleTemplateHash(*role)
		result.templates[templateKey{role: role.Name, entry: true}] = newPodTemplate(&role.EntryTemplate, hash)
		if role.WorkerTemplate != nil {
			result.templates[templateKey{role: role.Name}] = newPodTemplate(role.WorkerTemplate, hash)
		}
	}
	return result
}

func newPodTemplate(template *workloadv1alpha1.PodTemplateSpec, hash string) PodTemplate {
	resolved := resolveTemplate(template)
	containers := make(map[string]ContainerTarget, len(resolved.Spec.Containers))
	for _, container := range resolved.Spec.Containers {
		containers[container.Name] = ContainerTarget{Image: container.Image, ImagePullPolicy: container.ImagePullPolicy}
	}
	return PodTemplate{Containers: containers, RoleTemplateHash: hash}
}

// ForPod returns the template the Pod is rendered from, by its Role and entry labels.
func (r *RevisionTemplates) ForPod(pod *corev1.Pod) (PodTemplate, bool) {
	key := templateKey{
		role:  utils.GetRoleName(pod),
		entry: pod.Labels[workloadv1alpha1.EntryLabelKey] == utils.Entry,
	}
	template, ok := r.templates[key]
	return template, ok
}

// Images returns the image of every regular container.
func (t PodTemplate) Images() map[string]string {
	images := make(map[string]string, len(t.Containers))
	for name, container := range t.Containers {
		images[name] = container.Image
	}
	return images
}

// Labels returns the revision labels of a Pod rendered from the template at revision.
func (t PodTemplate) Labels(revision string) map[string]string {
	return map[string]string{
		workloadv1alpha1.RevisionLabelKey:         revision,
		workloadv1alpha1.RoleTemplateHashLabelKey: t.RoleTemplateHash,
	}
}

// UnitPod is one Pod of a unit with the template it must match at the target revision.
type UnitPod struct {
	Pod    *corev1.Pod
	Target PodTemplate
}

// HasTargetLabels reports whether the Pod carries the target revision labels.
func (p *UnitPod) HasTargetLabels(revision string) bool {
	return utils.ObjectRevision(p.Pod) == revision && utils.ObjectRoleTemplateHash(p.Pod) == p.Target.RoleTemplateHash
}

// CheckPatchable verifies that the live Pod can be moved from the source template to the target template by an image patch.
// Every regular container must still run its source image with the pull policy the target resolves to, under restartPolicy Always.
// Any other live image is drift that the update must not overwrite.
func CheckPatchable(pod *corev1.Pod, source, target PodTemplate) *StallError {
	if pod.Spec.RestartPolicy != corev1.RestartPolicyAlways {
		return stall(ReasonIneligible, "pod %s has restartPolicy %q, want %q", pod.Name, pod.Spec.RestartPolicy, corev1.RestartPolicyAlways)
	}
	if len(pod.Spec.Containers) != len(target.Containers) {
		return stall(ReasonIneligible, "pod %s has %d containers, the target revision has %d", pod.Name, len(pod.Spec.Containers), len(target.Containers))
	}
	for i := range pod.Spec.Containers {
		container := &pod.Spec.Containers[i]
		want, ok := target.Containers[container.Name]
		if !ok {
			return stall(ReasonIneligible, "pod %s has container %q, which the target revision does not have", pod.Name, container.Name)
		}
		if container.RestartPolicy != nil || len(container.RestartPolicyRules) > 0 {
			return stall(ReasonIneligible, "container %q of pod %s sets its own restart policy", container.Name, pod.Name)
		}
		if container.ImagePullPolicy != want.ImagePullPolicy {
			return stall(ReasonIneligible, "container %q of pod %s has imagePullPolicy %q, the target revision resolves to %q",
				container.Name, pod.Name, container.ImagePullPolicy, want.ImagePullPolicy)
		}
		if sourceImage := source.Containers[container.Name].Image; container.Image != sourceImage {
			return stall(ReasonUnexplainedImageDrift, "container %q of pod %s runs image %q, but its source revision has %q",
				container.Name, pod.Name, container.Image, sourceImage)
		}
	}
	return nil
}

// Verify reports whether the Pod has finished its update to the target revision, or explains what it is waiting for.
// Every regular container must report the target image and be ready.
// A container whose image the patch changed must also have been restarted by kubelet since the patch, so an image that only differs in spelling is not mistaken for an applied one.
func Verify(p *UnitPod, state *State, revision string) (bool, string) {
	pod := p.Pod
	switch {
	case state == nil || state.Phase != PhasePatched || state.TargetRevision != revision:
		return false, fmt.Sprintf("pod %s is not patched to revision %s", pod.Name, revision)
	case pod.DeletionTimestamp != nil:
		return false, fmt.Sprintf("pod %s is terminating", pod.Name)
	case !p.HasTargetLabels(revision):
		return false, fmt.Sprintf("pod %s does not carry the labels of revision %s", pod.Name, revision)
	case pod.Spec.RestartPolicy != corev1.RestartPolicyAlways:
		return false, fmt.Sprintf("pod %s has restartPolicy %q", pod.Name, pod.Spec.RestartPolicy)
	}

	statuses := make(map[string]*corev1.ContainerStatus, len(pod.Status.ContainerStatuses))
	for i := range pod.Status.ContainerStatuses {
		statuses[pod.Status.ContainerStatuses[i].Name] = &pod.Status.ContainerStatuses[i]
	}
	for i := range pod.Spec.Containers {
		container := &pod.Spec.Containers[i]
		want := p.Target.Containers[container.Name]
		if container.Image != want.Image || container.ImagePullPolicy != want.ImagePullPolicy {
			return false, fmt.Sprintf("container %q of pod %s does not match revision %s", container.Name, pod.Name, revision)
		}
		status, ok := statuses[container.Name]
		if !ok || !SameImage(status.Image, want.Image) {
			return false, fmt.Sprintf("container %q of pod %s is not running image %q yet", container.Name, pod.Name, want.Image)
		}
		if patched, changed := state.Patched[container.Name]; changed && !restartedSincePatch(status, patched, state.Baseline[container.Name]) {
			return false, fmt.Sprintf("container %q of pod %s has not restarted on image %q yet", container.Name, pod.Name, want.Image)
		}
		if !status.Ready {
			return false, fmt.Sprintf("container %q of pod %s is not ready", container.Name, pod.Name)
		}
	}
	if !podConditionTrue(pod, corev1.ContainersReady) {
		return false, fmt.Sprintf("containers of pod %s are not ready", pod.Name)
	}
	return true, ""
}

// restartedSincePatch reports whether kubelet has started the container again after the image patch.
// Kubelet applies a new image by restarting the container, which raises its restart count by one.
// A container that had never been created before the patch starts for the first time instead.
func restartedSincePatch(status *corev1.ContainerStatus, patched PatchedContainer, baseline ContainerBaseline) bool {
	if status.RestartCount > patched.RestartCount {
		return true
	}
	return baseline.ContainerID == "" && patched.RestartCount == 0 && status.ContainerID != ""
}

func podConditionTrue(pod *corev1.Pod, conditionType corev1.PodConditionType) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == conditionType {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

// Step is the next action for a unit.
type Step string

const (
	// StepNone means every Pod carries the target revision and no update is unfinished.
	StepNone Step = "None"
	// StepMark records the update and the restart baseline on Pods without an unfinished update.
	StepMark Step = "Mark"
	// StepPatch patches the images of Pods that are marked but not patched.
	StepPatch Step = "Patch"
	// StepVerify waits for patched Pods to run and be ready on the target images.
	StepVerify Step = "Verify"
	// StepComplete finishes the update of every Pod once the whole unit is verified.
	StepComplete Step = "Complete"
	// StepStalled means the unit cannot make progress without a change to the spec or the Pods.
	StepStalled Step = "Stalled"
)

// Plan is the next step for a unit and the Pods it applies to.
type Plan struct {
	Step Step
	Pods []*UnitPod
	// Message explains why the unit is waiting or stalled.
	Message string
	// Reason is set for StepStalled.
	Reason string
}

// PlanUnit derives the next step of a unit from its Pods alone, so a restarted controller resumes from the same step.
// Steps run in order across the whole unit: every Pod is marked before any is patched, and no Pod completes until every Pod is verified.
// The traffic drain between marking and patching is not implemented yet, so marked Pods are patched right away.
// The caller decides whether a unit without an unfinished update may start one, and checks each Pod with CheckPatchable before marking or patching it.
func PlanUnit(ownerUID types.UID, revision string, pods []*UnitPod) Plan {
	var unmarked, draining, verified []*UnitPod
	inUpdate := false
	waiting := ""
	for _, p := range pods {
		state := ownedState(p.Pod, ownerUID)
		if state == nil || !state.InProgress() {
			// A Pod that completed its update, or was recreated on the target revision, needs no update.
			if !p.HasTargetLabels(revision) {
				unmarked = append(unmarked, p)
			}
			continue
		}
		inUpdate = true
		if state.TargetRevision != revision {
			return Plan{Step: StepStalled, Reason: ReasonRetargetNotSupported,
				Message: fmt.Sprintf("pod %s is updating to revision %s, and changing the target of an unfinished in-place update is not supported yet", p.Pod.Name, state.TargetRevision)}
		}
		switch state.Phase {
		case PhaseDraining:
			draining = append(draining, p)
		case PhasePatched:
			if ok, message := Verify(p, state, revision); ok {
				verified = append(verified, p)
			} else if waiting == "" {
				waiting = message
			}
		}
	}

	switch {
	case !inUpdate && len(unmarked) == 0:
		return Plan{Step: StepNone}
	case len(unmarked) > 0:
		return Plan{Step: StepMark, Pods: unmarked}
	case len(draining) > 0:
		return Plan{Step: StepPatch, Pods: draining}
	case waiting != "":
		return Plan{Step: StepVerify, Message: waiting}
	default:
		return Plan{Step: StepComplete, Pods: verified}
	}
}

// ownedState returns the Pod's matching state only if it was written for the ModelServing with ownerUID.
func ownedState(pod *corev1.Pod, ownerUID types.UID) *State {
	state := MatchingState(pod)
	if state == nil || state.OwnerUID != ownerUID {
		return nil
	}
	return state
}

// InUpdate reports whether any Pod has an unfinished update written for the ModelServing with ownerUID.
func InUpdate(pods []*corev1.Pod, ownerUID types.UID) bool {
	for _, pod := range pods {
		if state := ownedState(pod, ownerUID); state != nil && state.InProgress() {
			return true
		}
	}
	return false
}

// UnfinishedSourceRevision returns the revision a unit in update started from, preferring one that differs from revision.
// Pods recreated during the update start from revision itself, so they do not tell where the unit came from.
func UnfinishedSourceRevision(pods []*corev1.Pod, ownerUID types.UID, revision string) (string, bool) {
	source, found := "", false
	for _, pod := range pods {
		state := ownedState(pod, ownerUID)
		if state == nil || !state.InProgress() {
			continue
		}
		if !found || source == revision {
			source, found = state.SourceRevision, true
		}
	}
	return source, found
}
