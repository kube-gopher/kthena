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

package datastore

import (
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"istio.io/istio/pkg/util/sets"
	"k8s.io/apimachinery/pkg/types"

	aiv1alpha1 "github.com/volcano-sh/kthena/pkg/apis/networking/v1alpha1"
	"github.com/volcano-sh/kthena/pkg/kthena-router/backend"
	"github.com/volcano-sh/kthena/pkg/kthena-router/utils"
)

func TestAddOrUpdatePod_MetricsPreservedOnUpdate(t *testing.T) {
	sampleCount := uint64(100)
	sampleSum := 0.42
	stubHistogram := &dto.Histogram{
		SampleCount: &sampleCount,
		SampleSum:   &sampleSum,
	}

	tests := []struct {
		name            string
		initialMetrics  map[string]float64
		initialHist     map[string]*dto.Histogram
		initialModels   []string
		updatedLabels   map[string]string
		wantGPUCache    float64
		wantWaiting     float64
		wantRunning     float64
		wantTPOT        float64
		wantTTFT        float64
		wantModels      []string
		wantHistPresent bool
	}{
		{
			name: "pod label update preserves all gauge metrics",
			initialMetrics: map[string]float64{
				utils.GPUCacheUsage:     0.75,
				utils.RequestWaitingNum: 8,
				utils.RequestRunningNum: 12,
				utils.TPOT:             0.03,
				utils.TTFT:             0.15,
			},
			initialHist:     map[string]*dto.Histogram{},
			initialModels:   []string{"llama-3"},
			updatedLabels:   map[string]string{"version": "v2"},
			wantGPUCache:    0.75,
			wantWaiting:     8,
			wantRunning:     12,
			wantTPOT:        0.03,
			wantTTFT:        0.15,
			wantModels:      []string{"llama-3"},
			wantHistPresent: false,
		},
		{
			name: "pod status update preserves histogram metrics",
			initialMetrics: map[string]float64{
				utils.GPUCacheUsage:     0.5,
				utils.RequestWaitingNum: 3,
				utils.RequestRunningNum: 7,
				utils.TPOT:             0.02,
				utils.TTFT:             0.1,
			},
			initialHist: map[string]*dto.Histogram{
				utils.TPOT: stubHistogram,
				utils.TTFT: stubHistogram,
			},
			initialModels:   []string{"mistral-7b", "lora-adapter-1"},
			updatedLabels:   map[string]string{},
			wantGPUCache:    0.5,
			wantWaiting:     3,
			wantRunning:     7,
			wantTPOT:        0.02,
			wantTTFT:        0.1,
			wantModels:      []string{"mistral-7b", "lora-adapter-1"},
			wantHistPresent: true,
		},
		{
			name: "pod update with zero initial metrics preserves zeros",
			initialMetrics: map[string]float64{
				utils.GPUCacheUsage:     0,
				utils.RequestWaitingNum: 0,
				utils.RequestRunningNum: 0,
			},
			initialHist:     map[string]*dto.Histogram{},
			initialModels:   []string{},
			updatedLabels:   map[string]string{"canary": "true"},
			wantGPUCache:    0,
			wantWaiting:     0,
			wantRunning:     0,
			wantTPOT:        0,
			wantTTFT:        0,
			wantModels:      []string{},
			wantHistPresent: false,
		},
		{
			name: "pod update with high load preserves high metrics",
			initialMetrics: map[string]float64{
				utils.GPUCacheUsage:     0.99,
				utils.RequestWaitingNum: 50,
				utils.RequestRunningNum: 100,
				utils.TPOT:             0.08,
				utils.TTFT:             0.5,
			},
			initialHist:     map[string]*dto.Histogram{},
			initialModels:   []string{"gpt-j"},
			updatedLabels:   map[string]string{"zone": "us-east-1"},
			wantGPUCache:    0.99,
			wantWaiting:     50,
			wantRunning:     100,
			wantTPOT:        0.08,
			wantTTFT:        0.5,
			wantModels:      []string{"gpt-j"},
			wantHistPresent: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newStore()

			ms := createTestModelServer("default", "ms1", aiv1alpha1.VLLM)
			s.AddOrUpdateModelServer(ms, sets.New[types.NamespacedName]())

			// Patch backend calls for the initial add
			patch := gomonkey.NewPatches()
			patch.ApplyFunc(backend.GetPodMetrics, func(_ string, _ *corev1.Pod, _ map[string]*dto.Histogram) (map[string]float64, map[string]*dto.Histogram) {
				return tc.initialMetrics, tc.initialHist
			})
			patch.ApplyFunc(backend.GetPodModels, func(_ string, _ *corev1.Pod) ([]string, error) {
				return tc.initialModels, nil
			})

			pod := createTestPod("default", "pod1")
			err := s.AddOrUpdatePod(pod, []*aiv1alpha1.ModelServer{ms})
			assert.NoError(t, err)

			patch.Reset()

			// Backend should NOT be called during an update. If it is, fail loudly.
			patch2 := gomonkey.NewPatches()
			patch2.ApplyFunc(backend.GetPodMetrics, func(_ string, _ *corev1.Pod, _ map[string]*dto.Histogram) (map[string]float64, map[string]*dto.Histogram) {
				t.Fatal("backend.GetPodMetrics must not be called on pod update")
				return nil, nil
			})
			patch2.ApplyFunc(backend.GetPodModels, func(_ string, _ *corev1.Pod) ([]string, error) {
				t.Fatal("backend.GetPodModels must not be called on pod update")
				return nil, nil
			})
			defer patch2.Reset()

			// Simulate a pod update (e.g. label change, status update)
			updatedPod := pod.DeepCopy()
			if tc.updatedLabels != nil {
				updatedPod.Labels = tc.updatedLabels
			}

			err = s.AddOrUpdatePod(updatedPod, []*aiv1alpha1.ModelServer{ms})
			assert.NoError(t, err)

			podInfo := s.GetPodInfo(utils.GetNamespaceName(updatedPod))
			assert.NotNil(t, podInfo)

			assert.InDelta(t, tc.wantGPUCache, podInfo.GetGPUCacheUsage(), 1e-9,
				"GPUCacheUsage dropped after pod update")
			assert.InDelta(t, tc.wantWaiting, podInfo.GetRequestWaitingNum(), 1e-9,
				"RequestWaitingNum dropped after pod update")
			assert.InDelta(t, tc.wantRunning, podInfo.GetRequestRunningNum(), 1e-9,
				"RequestRunningNum dropped after pod update")
			assert.InDelta(t, tc.wantTPOT, podInfo.GetTPOT(), 1e-9,
				"TPOT dropped after pod update")
			assert.InDelta(t, tc.wantTTFT, podInfo.GetTTFT(), 1e-9,
				"TTFT dropped after pod update")

			models := podInfo.GetModels()
			for _, m := range tc.wantModels {
				assert.True(t, models.Contains(m), "model %s lost after pod update", m)
			}
			assert.Equal(t, len(tc.wantModels), models.Len(),
				"model count changed after pod update")

			if tc.wantHistPresent {
				podInfo.mutex.RLock()
				assert.NotNil(t, podInfo.TimePerOutputToken, "TPOT histogram lost after pod update")
				assert.NotNil(t, podInfo.TimeToFirstToken, "TTFT histogram lost after pod update")
				podInfo.mutex.RUnlock()
			}
		})
	}
}

func TestAddOrUpdatePod_NewPodStillFetchesMetrics(t *testing.T) {
	s := newStore()

	ms := createTestModelServer("default", "ms1", aiv1alpha1.VLLM)
	s.AddOrUpdateModelServer(ms, sets.New[types.NamespacedName]())

	metricsCalled := false
	modelsCalled := false

	patch := gomonkey.NewPatches()
	patch.ApplyFunc(backend.GetPodMetrics, func(_ string, _ *corev1.Pod, _ map[string]*dto.Histogram) (map[string]float64, map[string]*dto.Histogram) {
		metricsCalled = true
		return map[string]float64{
			utils.GPUCacheUsage:     0.3,
			utils.RequestRunningNum: 2,
		}, map[string]*dto.Histogram{}
	})
	patch.ApplyFunc(backend.GetPodModels, func(_ string, _ *corev1.Pod) ([]string, error) {
		modelsCalled = true
		return []string{"base-model"}, nil
	})
	defer patch.Reset()

	pod := createTestPod("default", "fresh-pod")
	err := s.AddOrUpdatePod(pod, []*aiv1alpha1.ModelServer{ms})
	assert.NoError(t, err)

	assert.True(t, metricsCalled, "backend.GetPodMetrics must be called for new pods")
	assert.True(t, modelsCalled, "backend.GetPodModels must be called for new pods")

	podInfo := s.GetPodInfo(utils.GetNamespaceName(pod))
	assert.InDelta(t, 0.3, podInfo.GetGPUCacheUsage(), 1e-9)
	assert.InDelta(t, 2.0, podInfo.GetRequestRunningNum(), 1e-9)
}

func TestAddOrUpdatePod_ModelServerChangePreservesMetrics(t *testing.T) {
	s := newStore()

	ms1 := createTestModelServer("default", "ms1", aiv1alpha1.VLLM)
	ms2 := createTestModelServer("default", "ms2", aiv1alpha1.VLLM)
	s.AddOrUpdateModelServer(ms1, sets.New[types.NamespacedName]())
	s.AddOrUpdateModelServer(ms2, sets.New[types.NamespacedName]())

	patch := gomonkey.NewPatches()
	patch.ApplyFunc(backend.GetPodMetrics, func(_ string, _ *corev1.Pod, _ map[string]*dto.Histogram) (map[string]float64, map[string]*dto.Histogram) {
		return map[string]float64{
			utils.GPUCacheUsage:     0.6,
			utils.RequestWaitingNum: 5,
			utils.RequestRunningNum: 10,
			utils.TPOT:             0.04,
			utils.TTFT:             0.2,
		}, map[string]*dto.Histogram{}
	})
	patch.ApplyFunc(backend.GetPodModels, func(_ string, _ *corev1.Pod) ([]string, error) {
		return []string{"model-a"}, nil
	})

	pod := createTestPod("default", "pod1")
	err := s.AddOrUpdatePod(pod, []*aiv1alpha1.ModelServer{ms1})
	assert.NoError(t, err)

	patch.Reset()

	// Block backend calls during the reassignment update
	patch2 := gomonkey.NewPatches()
	patch2.ApplyFunc(backend.GetPodMetrics, func(_ string, _ *corev1.Pod, _ map[string]*dto.Histogram) (map[string]float64, map[string]*dto.Histogram) {
		t.Fatal("backend.GetPodMetrics must not be called on pod update")
		return nil, nil
	})
	patch2.ApplyFunc(backend.GetPodModels, func(_ string, _ *corev1.Pod) ([]string, error) {
		t.Fatal("backend.GetPodModels must not be called on pod update")
		return nil, nil
	})
	defer patch2.Reset()

	// Move pod from ms1 to ms2
	err = s.AddOrUpdatePod(pod, []*aiv1alpha1.ModelServer{ms2})
	assert.NoError(t, err)

	podInfo := s.GetPodInfo(utils.GetNamespaceName(pod))
	assert.InDelta(t, 0.6, podInfo.GetGPUCacheUsage(), 1e-9,
		"GPUCacheUsage lost during model server reassignment")
	assert.InDelta(t, 5.0, podInfo.GetRequestWaitingNum(), 1e-9,
		"RequestWaitingNum lost during model server reassignment")
	assert.InDelta(t, 10.0, podInfo.GetRequestRunningNum(), 1e-9,
		"RequestRunningNum lost during model server reassignment")
	assert.InDelta(t, 0.04, podInfo.GetTPOT(), 1e-9,
		"TPOT lost during model server reassignment")
	assert.InDelta(t, 0.2, podInfo.GetTTFT(), 1e-9,
		"TTFT lost during model server reassignment")

	models := podInfo.GetModels()
	assert.True(t, models.Contains("model-a"), "model lost during model server reassignment")
}

func newStore() *store {
	return New().(*store)
}
