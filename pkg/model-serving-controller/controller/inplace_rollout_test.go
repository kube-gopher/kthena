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
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apiextfake "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/fake"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	kubefake "k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	volcanofake "volcano.sh/apis/pkg/client/clientset/versioned/fake"

	kthenafake "github.com/volcano-sh/kthena/client-go/clientset/versioned/fake"
	workloadv1alpha1 "github.com/volcano-sh/kthena/pkg/apis/workload/v1alpha1"
	"github.com/volcano-sh/kthena/pkg/model-serving-controller/datastore"
	"github.com/volcano-sh/kthena/pkg/model-serving-controller/inplace"
	"github.com/volcano-sh/kthena/pkg/model-serving-controller/utils"
)

const (
	inPlaceRolloutMSUID          = types.UID("inplace-rollout-uid")
	inPlaceRolloutSourceRevision = "rev-source"
	inPlaceRolloutOldImage       = "example.com/engine:v1"
	inPlaceRolloutNewImage       = "example.com/engine:v2"
)

// newInPlaceRolloutModelServing returns a ModelServing whose ServingGroups each have one entry and one worker Pod.
func newInPlaceRolloutModelServing(image string, replicas int32) *workloadv1alpha1.ModelServing {
	template := func() workloadv1alpha1.PodTemplateSpec {
		return workloadv1alpha1.PodTemplateSpec{Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "engine", Image: image}},
		}}
	}
	workerTemplate := template()
	return &workloadv1alpha1.ModelServing{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "llama", UID: inPlaceRolloutMSUID},
		Spec: workloadv1alpha1.ModelServingSpec{
			Replicas:      ptr.To(replicas),
			SchedulerName: "volcano",
			RolloutStrategy: &workloadv1alpha1.RolloutStrategy{
				Type:         workloadv1alpha1.ServingGroupRollingUpdate,
				UpdateMethod: workloadv1alpha1.InPlaceUpdateMethod,
				RollingUpdateConfiguration: &workloadv1alpha1.RollingUpdateConfiguration{
					MaxUnavailable: ptr.To(intstr.FromInt32(1)),
				},
			},
			Template: workloadv1alpha1.ServingGroup{Roles: []workloadv1alpha1.Role{{
				Name:           "server",
				Replicas:       ptr.To[int32](1),
				EntryTemplate:  template(),
				WorkerReplicas: 1,
				WorkerTemplate: &workerTemplate,
			}}},
			RecoveryPolicy: workloadv1alpha1.RoleRecreate,
		},
	}
}

type inPlaceRolloutFixture struct {
	t          *testing.T
	ctx        context.Context
	controller *ModelServingController
	kubeClient *kubefake.Clientset
	recorder   *record.FakeRecorder
	ms         *workloadv1alpha1.ModelServing
	revision   string
}

// newInPlaceRolloutFixture returns a controller with Running ServingGroups whose Pods run the old image, and a spec with the new image.
// The ControllerRevision of the source revision is recorded, as the controller does when it creates the Pods.
func newInPlaceRolloutFixture(t *testing.T, replicas int32) *inPlaceRolloutFixture {
	t.Helper()
	ctx := context.Background()
	source := newInPlaceRolloutModelServing(inPlaceRolloutOldImage, replicas)
	ms := newInPlaceRolloutModelServing(inPlaceRolloutNewImage, replicas)

	kubeClient := kubefake.NewSimpleClientset()
	_, err := utils.CreateControllerRevision(ctx, kubeClient, ms, inPlaceRolloutSourceRevision, source.Spec.Template.Roles)
	require.NoError(t, err)

	controller, err := NewModelServingController(kubeClient, kthenafake.NewSimpleClientset(),
		volcanofake.NewSimpleClientset(), apiextfake.NewSimpleClientset())
	require.NoError(t, err)
	t.Cleanup(controller.workqueue.ShutDown)
	recorder := record.NewFakeRecorder(100)
	controller.recorder = recorder
	controller.initialSync.Store(true)

	f := &inPlaceRolloutFixture{
		t:          t,
		ctx:        ctx,
		controller: controller,
		kubeClient: kubeClient,
		recorder:   recorder,
		ms:         ms,
		revision:   utils.ModelServingRevision(ms),
	}
	f.setModelServing(ms)

	role := source.Spec.Template.Roles[0]
	hash := utils.CalRoleTemplateHash(role)
	msKey := utils.GetNamespaceName(ms)
	for i := 0; i < int(replicas); i++ {
		groupName := utils.GenerateServingGroupName(ms.Name, i)
		roleID := utils.GenerateRoleID(role.Name, 0)
		entry := utils.GenerateEntryPod(*role.DeepCopy(), ms, groupName, roleID, inPlaceRolloutSourceRevision, hash)
		worker := utils.GenerateWorkerPod(*role.DeepCopy(), ms, entry, groupName, roleID, 1, inPlaceRolloutSourceRevision, hash)
		for _, pod := range []*corev1.Pod{entry, worker} {
			f.createRunningPod(pod)
		}
		controller.store.AddServingGroupAndRole(msKey, groupName, inPlaceRolloutSourceRevision, hash, role.Name, roleID)
		require.NoError(t, controller.store.UpdateServingGroupStatus(msKey, groupName, datastore.ServingGroupRunning))
	}
	return f
}

// createRunningPod creates the Pod as the API server and kubelet would leave it: defaulted, running, and ready.
func (f *inPlaceRolloutFixture) createRunningPod(pod *corev1.Pod) {
	pod.UID = types.UID(pod.Name)
	pod.ResourceVersion = "1"
	pod.Spec.RestartPolicy = corev1.RestartPolicyAlways
	pod.Status = corev1.PodStatus{
		Phase: corev1.PodRunning,
		Conditions: []corev1.PodCondition{
			{Type: corev1.ContainersReady, Status: corev1.ConditionTrue},
			{Type: corev1.PodReady, Status: corev1.ConditionTrue},
		},
	}
	for i := range pod.Spec.Containers {
		container := &pod.Spec.Containers[i]
		container.ImagePullPolicy = corev1.PullIfNotPresent
		pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, corev1.ContainerStatus{
			Name:        container.Name,
			Image:       container.Image,
			ContainerID: "containerd://" + pod.Name + "-0",
			Ready:       true,
		})
	}
	_, err := f.kubeClient.CoreV1().Pods(pod.Namespace).Create(f.ctx, pod, metav1.CreateOptions{})
	require.NoError(f.t, err)
	require.NoError(f.t, f.controller.podsInformer.GetIndexer().Add(pod.DeepCopy()))
}

func (f *inPlaceRolloutFixture) setModelServing(ms *workloadv1alpha1.ModelServing) {
	f.ms = ms
	require.NoError(f.t, f.controller.modelServingsInformer.GetIndexer().Update(ms))
}

// sync runs one rollout step and lets the informer cache observe the patched Pods.
func (f *inPlaceRolloutFixture) sync() error {
	err := f.controller.manageRollingUpdate(f.ctx, f.ms, f.revision)
	f.refreshCache()
	return err
}

func (f *inPlaceRolloutFixture) refreshCache() {
	pods, err := f.kubeClient.CoreV1().Pods("default").List(f.ctx, metav1.ListOptions{})
	require.NoError(f.t, err)
	for i := range pods.Items {
		require.NoError(f.t, f.controller.podsInformer.GetIndexer().Update(pods.Items[i].DeepCopy()))
	}
}

// runKubelet restarts every container of the ServingGroup whose spec image differs from the image it runs, as kubelet does.
func (f *inPlaceRolloutFixture) runKubelet(groupName string) {
	for _, pod := range f.pods(groupName) {
		f.runKubeletOn(pod)
	}
	f.refreshCache()
}

// runKubeletOn restarts every container of one Pod whose spec image differs from the image it runs.
func (f *inPlaceRolloutFixture) runKubeletOn(pod *corev1.Pod) {
	for i := range pod.Spec.Containers {
		status := &pod.Status.ContainerStatuses[i]
		if status.Image == pod.Spec.Containers[i].Image {
			continue
		}
		status.RestartCount++
		status.ContainerID = fmt.Sprintf("containerd://%s-%d", pod.Name, status.RestartCount)
		status.Image = pod.Spec.Containers[i].Image
	}
	_, err := f.kubeClient.CoreV1().Pods(pod.Namespace).UpdateStatus(f.ctx, pod, metav1.UpdateOptions{})
	require.NoError(f.t, err)
}

func (f *inPlaceRolloutFixture) pods(groupName string) []*corev1.Pod {
	pods, err := f.kubeClient.CoreV1().Pods("default").List(f.ctx, metav1.ListOptions{
		LabelSelector: workloadv1alpha1.GroupNameLabelKey + "=" + groupName,
	})
	require.NoError(f.t, err)
	result := make([]*corev1.Pod, 0, len(pods.Items))
	for i := range pods.Items {
		result = append(result, &pods.Items[i])
	}
	require.Len(f.t, result, 2, "a ServingGroup keeps its entry and worker Pods")
	return result
}

// phases returns the update phase of each Pod in the ServingGroup, or "" for a Pod without a state.
func (f *inPlaceRolloutFixture) phases(groupName string) []inplace.Phase {
	var phases []inplace.Phase
	for _, pod := range f.pods(groupName) {
		state, err := inplace.ParseState(pod)
		require.NoError(f.t, err)
		if state == nil {
			phases = append(phases, "")
			continue
		}
		phases = append(phases, state.Phase)
	}
	return phases
}

func (f *inPlaceRolloutFixture) storeRevision(groupName string) string {
	revision, ok := f.controller.store.GetServingGroupRevision(utils.GetNamespaceName(f.ms), groupName)
	require.True(f.t, ok)
	return revision
}

// events returns the reasons of the events recorded so far.
func (f *inPlaceRolloutFixture) events() []string {
	var reasons []string
	for {
		select {
		case event := <-f.recorder.Events:
			reasons = append(reasons, strings.Fields(event)[1])
		default:
			return reasons
		}
	}
}

func TestInPlaceRolloutUpdatesServingGroupsWithinBudget(t *testing.T) {
	f := newInPlaceRolloutFixture(t, 2)
	podUIDs := map[types.UID]bool{}
	for _, group := range []string{"llama-0", "llama-1"} {
		for _, pod := range f.pods(group) {
			podUIDs[pod.UID] = true
		}
	}

	// The highest ordinal is marked and patched in one sync, and the budget of one keeps the other untouched.
	require.NoError(t, f.sync())
	assert.Equal(t, []inplace.Phase{inplace.PhasePatched, inplace.PhasePatched}, f.phases("llama-1"))
	assert.Equal(t, []inplace.Phase{"", ""}, f.phases("llama-0"))
	for _, pod := range f.pods("llama-1") {
		assert.Equal(t, inPlaceRolloutNewImage, pod.Spec.Containers[0].Image)
		assert.Equal(t, f.revision, utils.ObjectRevision(pod))
		assert.Equal(t, utils.CalRoleTemplateHash(f.ms.Spec.Template.Roles[0]), utils.ObjectRoleTemplateHash(pod))
		state := inplace.MatchingState(pod)
		require.NotNil(t, state)
		assert.Equal(t, inPlaceRolloutSourceRevision, state.SourceRevision)
		assert.Equal(t, "llama-1", state.Unit)
		assert.Equal(t, map[string]inplace.PatchedContainer{"engine": {Image: inPlaceRolloutNewImage}}, state.Patched)
	}
	assert.Equal(t, inPlaceRolloutSourceRevision, f.storeRevision("llama-1"), "a unit in update stays on its source revision")
	assert.Equal(t, []string{"InPlaceUpdateStarted", "InPlaceUpdatePatched"}, f.events())

	// Until kubelet restarts the containers, the unit waits and keeps its slot.
	require.NoError(t, f.sync())
	assert.Equal(t, []inplace.Phase{inplace.PhasePatched, inplace.PhasePatched}, f.phases("llama-1"))
	assert.Equal(t, []inplace.Phase{"", ""}, f.phases("llama-0"))

	f.runKubelet("llama-1")
	require.NoError(t, f.sync())
	assert.Equal(t, []inplace.Phase{inplace.PhaseDone, inplace.PhaseDone}, f.phases("llama-1"))
	for _, pod := range f.pods("llama-1") {
		assert.Equal(t, inplace.ContainerBaseline{ContainerID: "containerd://" + pod.Name + "-1", RestartCount: 1},
			inplace.MatchingState(pod).Baseline["engine"], "the image-change restart moves into the baseline")
	}
	assert.Equal(t, f.revision, f.storeRevision("llama-1"))
	assert.Equal(t, []string{"InPlaceUpdateCompleted"}, f.events())

	// The completed unit is Running again, so the next unit starts.
	require.NoError(t, f.sync())
	assert.Equal(t, []inplace.Phase{inplace.PhasePatched, inplace.PhasePatched}, f.phases("llama-0"))
	f.runKubelet("llama-0")
	require.NoError(t, f.sync())
	assert.Equal(t, []inplace.Phase{inplace.PhaseDone, inplace.PhaseDone}, f.phases("llama-0"))
	assert.Equal(t, f.revision, f.storeRevision("llama-0"))

	// Every Pod was updated in place.
	for _, group := range []string{"llama-0", "llama-1"} {
		for _, pod := range f.pods(group) {
			assert.True(t, podUIDs[pod.UID], "pod %s was replaced", pod.Name)
		}
	}
	patches := f.podPatches()
	require.NoError(t, f.sync())
	assert.Equal(t, patches, f.podPatches(), "a finished rollout patches nothing")
}

func (f *inPlaceRolloutFixture) podPatches() int {
	count := 0
	for _, action := range f.kubeClient.Actions() {
		if action.Matches("patch", "pods") {
			count++
		}
	}
	return count
}

func TestInPlaceRolloutPartition(t *testing.T) {
	f := newInPlaceRolloutFixture(t, 2)
	ms := f.ms.DeepCopy()
	ms.Spec.RolloutStrategy.RollingUpdateConfiguration.Partition = ptr.To(intstr.FromInt32(1))
	f.setModelServing(ms)

	require.NoError(t, f.sync())
	assert.Equal(t, []inplace.Phase{inplace.PhasePatched, inplace.PhasePatched}, f.phases("llama-1"))

	// Raising the partition does not stop a unit that has started.
	ms = f.ms.DeepCopy()
	ms.Spec.RolloutStrategy.RollingUpdateConfiguration.Partition = ptr.To(intstr.FromInt32(2))
	f.setModelServing(ms)
	f.runKubelet("llama-1")
	require.NoError(t, f.sync())
	assert.Equal(t, []inplace.Phase{inplace.PhaseDone, inplace.PhaseDone}, f.phases("llama-1"))

	require.NoError(t, f.sync())
	assert.Equal(t, []inplace.Phase{"", ""}, f.phases("llama-0"), "partition protects units that have not started")
}

func TestInPlaceRolloutStartsUnavailableUnitsFirst(t *testing.T) {
	f := newInPlaceRolloutFixture(t, 3)
	require.NoError(t, f.controller.store.UpdateServingGroupStatus(utils.GetNamespaceName(f.ms), "llama-0", datastore.ServingGroupCreating))

	// llama-0 is already unavailable and uses the only slot, so updating it does not reduce availability further.
	require.NoError(t, f.sync())
	assert.Equal(t, []inplace.Phase{inplace.PhasePatched, inplace.PhasePatched}, f.phases("llama-0"))
	assert.Equal(t, []inplace.Phase{"", ""}, f.phases("llama-1"))
	assert.Equal(t, []inplace.Phase{"", ""}, f.phases("llama-2"))
}

func TestInPlaceRolloutClampsMaxUnavailable(t *testing.T) {
	f := newInPlaceRolloutFixture(t, 2)
	ms := f.ms.DeepCopy()
	ms.Spec.RolloutStrategy.RollingUpdateConfiguration.MaxUnavailable = ptr.To(intstr.FromString("10%"))
	f.setModelServing(ms)

	require.NoError(t, f.sync())
	assert.Equal(t, []inplace.Phase{inplace.PhasePatched, inplace.PhasePatched}, f.phases("llama-1"),
		"a maxUnavailable that resolves to 0 still updates one unit, because an in-place update cannot surge")
	assert.Equal(t, []inplace.Phase{"", ""}, f.phases("llama-0"))
}

func TestInPlaceRolloutStalls(t *testing.T) {
	tests := []struct {
		name       string
		setup      func(f *inPlaceRolloutFixture)
		wantReason string
		// wantOtherPhases are the phases of llama-0, which another unit's stall does not block.
		wantOtherPhases []inplace.Phase
	}{
		{
			name: "live image differs from the source revision",
			setup: func(f *inPlaceRolloutFixture) {
				pod := f.pods("llama-1")[0]
				pod.Spec.Containers[0].Image = "example.com/engine:hotfix"
				_, err := f.kubeClient.CoreV1().Pods(pod.Namespace).Update(f.ctx, pod, metav1.UpdateOptions{})
				require.NoError(f.t, err)
				f.refreshCache()
			},
			wantReason:      inplace.ReasonUnexplainedImageDrift,
			wantOtherPhases: []inplace.Phase{inplace.PhasePatched, inplace.PhasePatched},
		},
		{
			name: "ControllerRevision of the source revision is missing",
			setup: func(f *inPlaceRolloutFixture) {
				require.NoError(f.t, f.kubeClient.AppsV1().ControllerRevisions("default").Delete(f.ctx,
					utils.GenerateControllerRevisionName(f.ms.Name, inPlaceRolloutSourceRevision), metav1.DeleteOptions{}))
			},
			wantReason:      inplace.ReasonRevisionHistoryMissing,
			wantOtherPhases: []inplace.Phase{"", ""},
		},
		{
			name: "source revision differs in more than images",
			setup: func(f *inPlaceRolloutFixture) {
				ms := f.ms.DeepCopy()
				ms.Spec.Template.Roles[0].EntryTemplate.Spec.Containers[0].Args = []string{"--new-flag"}
				f.setModelServing(ms)
				f.revision = utils.ModelServingRevision(ms)
			},
			wantReason:      inplace.ReasonIneligible,
			wantOtherPhases: []inplace.Phase{"", ""},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newInPlaceRolloutFixture(t, 2)
			tt.setup(f)

			require.NoError(t, f.sync())
			assert.Equal(t, []inplace.Phase{"", ""}, f.phases("llama-1"), "a unit that cannot be updated in place is never marked")
			assert.Contains(t, f.events(), tt.wantReason)
			assert.Equal(t, inPlaceRolloutSourceRevision, f.storeRevision("llama-1"))
			for _, pod := range f.pods("llama-1") {
				assert.NotEqual(t, inPlaceRolloutNewImage, pod.Spec.Containers[0].Image)
			}
			assert.Equal(t, tt.wantOtherPhases, f.phases("llama-0"))
		})
	}
}

// TestInPlaceRolloutResumesAfterCrash starts from a unit whose entry Pod was patched before the controller stopped.
// After the restart, the store may hold the revision of whichever Pod it observed first.
func TestInPlaceRolloutResumesAfterCrash(t *testing.T) {
	f := newInPlaceRolloutFixture(t, 1)
	workerName := utils.GeneratePodName("llama-0", utils.GenerateRoleID("server", 0), 1)
	failWorkerPatch := true
	f.kubeClient.PrependReactor("patch", "pods", func(action ktesting.Action) (bool, runtime.Object, error) {
		patch := action.(ktesting.PatchAction)
		if failWorkerPatch && patch.GetName() == workerName && strings.Contains(string(patch.GetPatch()), `"replace"`) {
			return true, nil, fmt.Errorf("connection lost")
		}
		return false, nil, nil
	})

	require.Error(t, f.sync())
	assert.ElementsMatch(t, []inplace.Phase{inplace.PhasePatched, inplace.PhaseDraining}, f.phases("llama-0"))
	entry := f.pods("llama-0")[0]
	entryPatchedAt := inplace.MatchingState(entry).PatchedAt

	failWorkerPatch = false
	require.NoError(t, f.controller.store.UpdateServingGroupRevision(utils.GetNamespaceName(f.ms), "llama-0", f.revision))
	require.NoError(t, f.sync())
	assert.Equal(t, []inplace.Phase{inplace.PhasePatched, inplace.PhasePatched}, f.phases("llama-0"))
	assert.Equal(t, entryPatchedAt, inplace.MatchingState(f.pods("llama-0")[0]).PatchedAt, "the patched entry Pod is not patched again")
	assert.Equal(t, inPlaceRolloutSourceRevision, f.storeRevision("llama-0"), "the store follows the unit's source revision")
}

func TestInPlaceRolloutRejectsStaleCache(t *testing.T) {
	f := newInPlaceRolloutFixture(t, 1)
	// The Pod changed after the informer cache observed it.
	pod := f.pods("llama-0")[0]
	pod.ResourceVersion = "2"
	_, err := f.kubeClient.CoreV1().Pods(pod.Namespace).UpdateStatus(f.ctx, pod, metav1.UpdateOptions{})
	require.NoError(t, err)

	require.Error(t, f.controller.manageRollingUpdate(f.ctx, f.ms, f.revision))
	stale, err := f.kubeClient.CoreV1().Pods(pod.Namespace).Get(f.ctx, pod.Name, metav1.GetOptions{})
	require.NoError(t, err)
	assert.Nil(t, inplace.MatchingState(stale), "a pod read from a stale cache is never marked")
}

// TestInPlaceRolloutKeepsUnsupportedConfigurations covers configurations that the webhook rejects, in case admission was bypassed.
func TestInPlaceRolloutKeepsUnsupportedConfigurations(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(ms *workloadv1alpha1.ModelServing)
	}{
		{
			name: "RoleRollingUpdate",
			mutate: func(ms *workloadv1alpha1.ModelServing) {
				ms.Spec.RolloutStrategy.Type = workloadv1alpha1.RoleRollingUpdate
				ms.Spec.RolloutStrategy.RollingUpdateConfiguration = nil
			},
		},
		{
			name: "plugins",
			mutate: func(ms *workloadv1alpha1.ModelServing) {
				ms.Spec.Plugins = []workloadv1alpha1.PluginSpec{{Name: "lws-labels"}}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newInPlaceRolloutFixture(t, 1)
			ms := f.ms.DeepCopy()
			tt.mutate(ms)
			f.setModelServing(ms)

			require.NoError(t, f.sync())
			assert.Equal(t, []inplace.Phase{"", ""}, f.phases("llama-0"))
			assert.Equal(t, datastore.ServingGroupRunning, f.controller.store.GetServingGroupStatus(utils.GetNamespaceName(f.ms), "llama-0"),
				"outdated resources are never recreated under InPlace")
			assert.Equal(t, []string{inplace.ReasonIneligible}, f.events())
		})
	}
}

// TestRecreateReplacesUnfinishedInPlaceUpdate switches to Recreate while a unit is in update.
// The store holds the target revision for that unit, as after a controller restart, so only its unfinished state makes it outdated.
func TestRecreateReplacesUnfinishedInPlaceUpdate(t *testing.T) {
	f := newInPlaceRolloutFixture(t, 2)
	require.NoError(t, f.sync())
	require.Equal(t, []inplace.Phase{inplace.PhasePatched, inplace.PhasePatched}, f.phases("llama-1"))

	ms := f.ms.DeepCopy()
	ms.Spec.RolloutStrategy.UpdateMethod = workloadv1alpha1.RecreateUpdateMethod
	f.setModelServing(ms)
	msKey := utils.GetNamespaceName(f.ms)
	require.NoError(t, f.controller.store.UpdateServingGroupRevision(msKey, "llama-1", f.revision))

	require.NoError(t, f.controller.manageRollingUpdate(f.ctx, f.ms, f.revision))
	assert.Equal(t, datastore.ServingGroupDeleting, f.controller.store.GetServingGroupStatus(msKey, "llama-1"))
	assert.Equal(t, datastore.ServingGroupRunning, f.controller.store.GetServingGroupStatus(msKey, "llama-0"))
}

func TestInPlaceRolloutDoesNotSurge(t *testing.T) {
	f := newInPlaceRolloutFixture(t, 2)
	ms := f.ms.DeepCopy()
	ms.Spec.RolloutStrategy.RollingUpdateConfiguration.MaxSurge = ptr.To(intstr.FromInt32(1))
	f.setModelServing(ms)

	require.NoError(t, f.controller.syncServingGroupReplicas(f.ctx, f.ms, f.revision))
	groups, err := f.controller.store.GetServingGroupByModelServing(utils.GetNamespaceName(f.ms))
	require.NoError(t, err)
	assert.Len(t, groups, 2, "no ServingGroup is created above spec.replicas")
}

func TestUpdatePodEnqueuesInPlaceUpdate(t *testing.T) {
	f := newInPlaceRolloutFixture(t, 1)
	require.NoError(t, f.sync())
	for f.controller.workqueue.Len() > 0 {
		item, _ := f.controller.workqueue.Get()
		f.controller.workqueue.Done(item)
	}

	// Kubelet has stopped the container to apply the new image, so the pod is not ready and has not restarted yet.
	pod := f.pods("llama-0")[0]
	pod.Status.Conditions = []corev1.PodCondition{{Type: corev1.ContainersReady, Status: corev1.ConditionFalse}}
	f.controller.updatePod(nil, pod)
	require.Equal(t, 1, f.controller.workqueue.Len(), "a pod event moves its in-place update forward")
	item, _ := f.controller.workqueue.Get()
	assert.Equal(t, "default/llama", item)
}

// startUpdateAt moves the start of every unfinished update in the ServingGroup to startedAt.
func (f *inPlaceRolloutFixture) startUpdateAt(groupName string, startedAt time.Time) {
	for _, pod := range f.pods(groupName) {
		state := inplace.MatchingState(pod)
		require.NotNil(f.t, state)
		state.StartedAt = metav1.NewTime(startedAt)
		value, err := state.Encode()
		require.NoError(f.t, err)
		pod.Annotations[workloadv1alpha1.InPlaceUpdateStateAnnotationKey] = value
		_, err = f.kubeClient.CoreV1().Pods(pod.Namespace).Update(f.ctx, pod, metav1.UpdateOptions{})
		require.NoError(f.t, err)
	}
	f.refreshCache()
}

// remainingPods returns the names of the Pods of the ServingGroup that were not deleted.
func (f *inPlaceRolloutFixture) remainingPods(groupName string) []string {
	pods, err := f.kubeClient.CoreV1().Pods("default").List(f.ctx, metav1.ListOptions{
		LabelSelector: workloadv1alpha1.GroupNameLabelKey + "=" + groupName,
	})
	require.NoError(f.t, err)
	var names []string
	for i := range pods.Items {
		names = append(names, pods.Items[i].Name)
	}
	return names
}

// TestInPlaceRolloutProgressDeadline covers a unit that is not verified within the progress deadline, measured from when its update started.
// Its unverified Pods are deleted, so they follow the RecoveryPolicy, while verified Pods are kept.
func TestInPlaceRolloutProgressDeadline(t *testing.T) {
	entry := utils.GeneratePodName("llama-0", utils.GenerateRoleID("server", 0), 0)
	worker := utils.GeneratePodName("llama-0", utils.GenerateRoleID("server", 0), 1)
	tests := []struct {
		name string
		// deadlineSeconds sets rolloutStrategy.progressDeadlineSeconds, or keeps the default when 0.
		deadlineSeconds int32
		// startedAgo is how long before the sync the update of the unit started.
		startedAgo time.Duration
		// prepare runs after the unit is patched and before its start time is moved.
		prepare       func(f *inPlaceRolloutFixture)
		wantRemaining []string
		wantPhases    []inplace.Phase
		wantEvent     bool
	}{
		{
			name:          "unit waiting for kubelet within the default deadline is kept",
			startedAgo:    29 * time.Minute,
			wantRemaining: []string{entry, worker},
			wantPhases:    []inplace.Phase{inplace.PhasePatched, inplace.PhasePatched},
		},
		{
			name:       "unverified pods are deleted after the default deadline",
			startedAgo: 31 * time.Minute,
			wantEvent:  true,
		},
		{
			name:            "configured deadline",
			deadlineSeconds: 60,
			startedAgo:      2 * time.Minute,
			wantEvent:       true,
		},
		{
			name:       "verified unit completes after the deadline",
			startedAgo: time.Hour,
			prepare: func(f *inPlaceRolloutFixture) {
				f.runKubelet("llama-0")
			},
			wantRemaining: []string{entry, worker},
			wantPhases:    []inplace.Phase{inplace.PhaseDone, inplace.PhaseDone},
		},
		{
			name:       "only unverified pods are deleted",
			startedAgo: time.Hour,
			prepare: func(f *inPlaceRolloutFixture) {
				for _, pod := range f.pods("llama-0") {
					if pod.Name == entry {
						f.runKubeletOn(pod)
					}
				}
				f.refreshCache()
			},
			wantRemaining: []string{entry},
			wantEvent:     true,
		},
		{
			name:       "update that stalled after marking is ended by the deadline",
			startedAgo: time.Hour,
			prepare: func(f *inPlaceRolloutFixture) {
				// The target changes during the update, which stalls the unit until retargeting is supported.
				ms := f.ms.DeepCopy()
				ms.Spec.Template.Roles[0].EntryTemplate.Spec.Containers[0].Image = "example.com/engine:v3"
				ms.Spec.Template.Roles[0].WorkerTemplate.Spec.Containers[0].Image = "example.com/engine:v3"
				f.setModelServing(ms)
				f.revision = utils.ModelServingRevision(ms)
			},
			wantEvent: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newInPlaceRolloutFixture(t, 1)
			if tt.deadlineSeconds > 0 {
				ms := f.ms.DeepCopy()
				ms.Spec.RolloutStrategy.ProgressDeadlineSeconds = ptr.To(tt.deadlineSeconds)
				f.setModelServing(ms)
			}
			require.NoError(t, f.sync())
			require.Equal(t, []inplace.Phase{inplace.PhasePatched, inplace.PhasePatched}, f.phases("llama-0"))
			if tt.prepare != nil {
				tt.prepare(f)
			}
			f.startUpdateAt("llama-0", time.Now().Add(-tt.startedAgo))
			f.events()

			require.NoError(t, f.sync())
			assert.ElementsMatch(t, tt.wantRemaining, f.remainingPods("llama-0"))
			if tt.wantPhases != nil {
				assert.Equal(t, tt.wantPhases, f.phases("llama-0"))
			}
			if tt.wantEvent {
				assert.Contains(t, f.events(), inplace.ReasonProgressDeadlineExceeded)
			} else {
				assert.NotContains(t, f.events(), inplace.ReasonProgressDeadlineExceeded)
			}
		})
	}
}
