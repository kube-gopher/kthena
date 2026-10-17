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

package webhook

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/validation/field"

	workloadv1alpha1 "github.com/volcano-sh/kthena/pkg/apis/workload/v1alpha1"
	"github.com/volcano-sh/kthena/pkg/model-serving-controller/inplace"
)

// validateUpdateMethod validates the rollout settings that depend on rolloutStrategy.updateMethod.
func validateUpdateMethod(ms *workloadv1alpha1.ModelServing) field.ErrorList {
	strategy := ms.Spec.RolloutStrategy
	if strategy == nil {
		return nil
	}
	var allErrs field.ErrorList
	strategyPath := field.NewPath("spec", "rolloutStrategy")
	if inplace.UpdateMethodOf(ms) != workloadv1alpha1.InPlaceUpdateMethod {
		if strategy.ProgressDeadlineSeconds != nil {
			allErrs = append(allErrs, field.Forbidden(strategyPath.Child("progressDeadlineSeconds"),
				"progressDeadlineSeconds is only valid when rolloutStrategy.updateMethod is InPlace"))
		}
		return allErrs
	}

	if strategy.Type == workloadv1alpha1.RoleRollingUpdate {
		allErrs = append(allErrs, field.Forbidden(strategyPath.Child("type"),
			"RoleRollingUpdate cannot be combined with rolloutStrategy.updateMethod InPlace"))
	}
	allErrs = append(allErrs, validateInPlaceBudget(ms)...)
	if len(ms.Spec.Plugins) > 0 {
		allErrs = append(allErrs, field.Forbidden(field.NewPath("spec", "plugins"),
			"plugins cannot be used with rolloutStrategy.updateMethod InPlace"))
	}
	rolesPath := field.NewPath("spec", "template", "roles")
	for i := range ms.Spec.Template.Roles {
		role := &ms.Spec.Template.Roles[i]
		allErrs = append(allErrs, validateInPlaceTemplate(&role.EntryTemplate, rolesPath.Index(i).Child("entryTemplate"))...)
		if role.WorkerTemplate != nil {
			allErrs = append(allErrs, validateInPlaceTemplate(role.WorkerTemplate, rolesPath.Index(i).Child("workerTemplate"))...)
		}
	}
	return allErrs
}

// validateInPlaceBudget requires an in-place rollout not to surge, whatever the replica count is.
// validateRollingUpdateConfiguration already rejects a maxUnavailable that resolves to 0 when maxSurge is 0.
func validateInPlaceBudget(ms *workloadv1alpha1.ModelServing) field.ErrorList {
	config := ms.Spec.RolloutStrategy.RollingUpdateConfiguration
	if config == nil || config.MaxSurge == nil {
		return nil
	}
	// Scaling to 100 with rounding up keeps every non-zero value non-zero, so a later scale-up cannot turn an admitted maxSurge positive.
	maxSurge, err := intstr.GetScaledValueFromIntOrPercent(config.MaxSurge, 100, true)
	if err != nil || maxSurge == 0 {
		return nil
	}
	return field.ErrorList{field.Invalid(field.NewPath("spec", "rolloutStrategy", "rollingUpdateConfiguration", "maxSurge"), config.MaxSurge,
		"maxSurge must be 0 when rolloutStrategy.updateMethod is InPlace")}
}

// validateInPlaceTemplate rejects restart settings under which a container would not restart on the patched image.
func validateInPlaceTemplate(template *workloadv1alpha1.PodTemplateSpec, fldPath *field.Path) field.ErrorList {
	var allErrs field.ErrorList
	// The API server defaults an empty restartPolicy to Always.
	if restartPolicy := template.Spec.RestartPolicy; restartPolicy != "" && restartPolicy != corev1.RestartPolicyAlways {
		allErrs = append(allErrs, field.NotSupported(fldPath.Child("spec", "restartPolicy"), restartPolicy,
			[]string{string(corev1.RestartPolicyAlways)}))
	}
	containersPath := fldPath.Child("spec", "containers")
	for i := range template.Spec.Containers {
		container := &template.Spec.Containers[i]
		if container.RestartPolicy != nil || len(container.RestartPolicyRules) > 0 {
			allErrs = append(allErrs, field.Forbidden(containersPath.Index(i),
				"container restartPolicy and restartPolicyRules cannot be set when rolloutStrategy.updateMethod is InPlace"))
		}
	}
	return allErrs
}

// validateInPlaceUpdate checks an update that leaves rolloutStrategy.updateMethod as InPlace.
// It only gives early feedback, because the controller checks the same eligibility before any in-place update.
func validateInPlaceUpdate(oldMS, ms *workloadv1alpha1.ModelServing) field.ErrorList {
	if inplace.UpdateMethodOf(ms) != workloadv1alpha1.InPlaceUpdateMethod {
		return nil
	}
	var allErrs field.ErrorList
	if inplace.UpdateMethodOf(oldMS) != workloadv1alpha1.InPlaceUpdateMethod {
		allErrs = append(allErrs, validateRolloutComplete(oldMS)...)
	}
	return append(allErrs, inplace.CheckEligibility(oldMS, ms)...)
}

// validateRolloutComplete requires every ServingGroup to be on one revision before InPlace takes effect.
// Otherwise some units would start from an older revision whose differences may not be image-only.
func validateRolloutComplete(oldMS *workloadv1alpha1.ModelServing) field.ErrorList {
	status := &oldMS.Status
	replicas := replicasOrDefault(oldMS.Spec.Replicas)
	if status.ObservedGeneration == oldMS.Generation &&
		status.CurrentRevision == status.UpdateRevision &&
		status.UpdatedReplicas == status.Replicas &&
		status.Replicas == replicas {
		return nil
	}
	return field.ErrorList{field.Forbidden(field.NewPath("spec", "rolloutStrategy", "updateMethod"),
		fmt.Sprintf("switching to InPlace requires the previous rollout to be complete: observedGeneration=%d generation=%d currentRevision=%q updateRevision=%q updatedReplicas=%d replicas=%d spec.replicas=%d",
			status.ObservedGeneration, oldMS.Generation, status.CurrentRevision, status.UpdateRevision, status.UpdatedReplicas, status.Replicas, replicas))}
}
