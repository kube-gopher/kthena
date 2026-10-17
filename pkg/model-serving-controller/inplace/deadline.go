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
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	workloadv1alpha1 "github.com/volcano-sh/kthena/pkg/apis/workload/v1alpha1"
)

const (
	// DefaultProgressDeadlineSeconds applies when rolloutStrategy.progressDeadlineSeconds is not set.
	DefaultProgressDeadlineSeconds = 1800

	// ReasonProgressDeadlineExceeded is reported when a unit is not verified within its progress deadline.
	ReasonProgressDeadlineExceeded = "ProgressDeadlineExceeded"
)

// ProgressDeadline returns how long the update of a unit may take before it has failed.
func ProgressDeadline(ms *workloadv1alpha1.ModelServing) time.Duration {
	seconds := int32(DefaultProgressDeadlineSeconds)
	if ms.Spec.RolloutStrategy != nil && ms.Spec.RolloutStrategy.ProgressDeadlineSeconds != nil {
		seconds = *ms.Spec.RolloutStrategy.ProgressDeadlineSeconds
	}
	return time.Duration(seconds) * time.Second
}

// UnitStartedAt returns when the update of a unit started, which is the earliest start time among its Pods with an unfinished update.
func UnitStartedAt(pods []*corev1.Pod, ownerUID types.UID) (time.Time, bool) {
	var startedAt time.Time
	found := false
	for _, pod := range pods {
		state := ownedState(pod, ownerUID)
		if state == nil || !state.InProgress() {
			continue
		}
		if !found || state.StartedAt.Time.Before(startedAt) {
			startedAt, found = state.StartedAt.Time, true
		}
	}
	return startedAt, found
}

// UnverifiedPods returns the Pods of a unit whose unfinished update has not reached the target revision.
// A Pod that completed its update or was recreated on the target revision is not returned.
func UnverifiedPods(ownerUID types.UID, revision string, pods []*UnitPod) []*UnitPod {
	var unverified []*UnitPod
	for _, p := range pods {
		state := ownedState(p.Pod, ownerUID)
		if state == nil || !state.InProgress() {
			continue
		}
		if ok, _ := Verify(p, state, revision); !ok {
			unverified = append(unverified, p)
		}
	}
	return unverified
}
