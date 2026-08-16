/*
Copyright 2026 The HAMi Authors.

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

package main

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"

	"github.com/Project-HAMi/HAMi/pkg/device"
	versionmetrics "github.com/Project-HAMi/HAMi/pkg/metrics"
	"github.com/Project-HAMi/HAMi/pkg/metrics/catalog"
	schedulerpkg "github.com/Project-HAMi/HAMi/pkg/scheduler"
	"github.com/Project-HAMi/HAMi/pkg/scheduler/policy"
)

// gatherSchedulerMetrics registers the scheduler's collectors the same way
// initMetrics does, including the zone wrapper, and returns one entry per
// exported metric family holding its sorted label names.
func gatherSchedulerMetrics(t *testing.T) map[string][]string {
	t.Helper()

	// A device in each mode, so that the MIG branch of collectNodeMetrics runs
	// alongside the ordinary one and every family gets a sample.
	nodeUsage := map[string]*schedulerpkg.NodeUsage{
		"node-1": {
			Devices: policy.DeviceUsageList{
				DeviceLists: []*policy.DeviceListsScore{
					{Device: &device.DeviceUsage{
						ID: "GPU-shared", Index: 0, Used: 2, Count: 4,
						Usedmem: 4096, Totalmem: 8192, Totalcore: 100, Usedcores: 50,
						Type: "NVIDIA", Mode: "hami-core",
					}},
					{Device: &device.DeviceUsage{
						ID: "GPU-mig", Index: 1, Totalmem: 8192, Totalcore: 100,
						Type: "NVIDIA", Mode: "mig",
						MigAllocationsInUse: []device.MigAllocation{{
							Profile: "2g.10gb", Placement: device.MigPlacement{Start: 0, Size: 2},
							MigUUID: "MIG-runtime", GPUInstanceID: 4, ComputeInstanceID: 0,
							RuntimeReady: true,
						}},
					}},
				},
			},
		},
	}

	quotaManager := device.NewQuotaManager()
	quotaManager.Quotas["team-a"] = &device.DeviceQuota{
		"nvidia.com/gpumem": &device.Quota{Used: 4096, Limit: 8192, LimitSet: true},
	}

	podManager := device.NewPodManager()
	podManager.AddPod(
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{
			Name: "trainer", Namespace: "team-a", UID: k8stypes.UID("pod-uid"),
		}},
		"node-1",
		device.PodDevices{"NVIDIA": device.PodSingleDevice{device.ContainerDevices{{
			UUID: "GPU-shared", Type: "NVIDIA", Usedmem: 2048, Usedcores: 25,
		}}}},
	)

	reg := prometheus.NewPedanticRegistry()
	reg.MustRegister(versionmetrics.NewBuildInfoCollector())
	wrapped := prometheus.WrapRegistererWith(prometheus.Labels{"zone": metricsZone}, reg)
	wrapped.MustRegister(
		ClusterManagerCollector{
			ClusterManager: &ClusterManager{Zone: metricsZone},
			metricsProvider: &fakeMetricsProvider{
				nodeUsage:    nodeUsage,
				quotaManager: quotaManager,
				podManager:   podManager,
			},
		},
	)
	schedulerpkg.RegisterMetrics(wrapped)

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}

	got := make(map[string][]string, len(families))
	for _, family := range families {
		var labels []string
		for _, pair := range family.GetMetric()[0].GetLabel() {
			labels = append(labels, pair.GetName())
		}
		sort.Strings(labels)
		got[family.GetName()] = labels
	}
	return got
}

func TestSchedulerExportsExactlyTheCatalogMetrics(t *testing.T) {
	got := gatherSchedulerMetrics(t)

	declared := make(map[string]bool)
	for _, name := range catalog.Names(catalog.ComponentScheduler) {
		declared[name] = true
	}

	for name := range got {
		if !declared[name] {
			t.Errorf("scheduler exports %q, which the catalog does not declare", name)
		}
	}
	for name := range declared {
		if _, ok := got[name]; !ok {
			t.Errorf("catalog declares %q for the scheduler, but it was not exported", name)
		}
	}
}

func TestSchedulerMetricLabelsMatchTheCatalog(t *testing.T) {
	got := gatherSchedulerMetrics(t)

	for _, m := range catalog.ForComponent(catalog.ComponentScheduler) {
		labels, ok := got[m.Name]
		if !ok {
			continue // reported by TestSchedulerExportsExactlyTheCatalogMetrics
		}
		want := catalog.EffectiveLabels(m)
		if !reflect.DeepEqual(labels, want) {
			t.Errorf("%s labels = %v, catalog declares %v", m.Name, labels, want)
		}
	}
}

func TestSchedulerRatioMetricsAreOnTheirDeclaredScale(t *testing.T) {
	// The fixture allocates half the memory and half the cores of the same
	// device, so a metric on a 0-100 scale reads 50 and one on a 0-1 scale
	// reads 0.5. Reading the values back is what pins the declared unit to
	// what the collector really emits.
	nodeUsage := map[string]*schedulerpkg.NodeUsage{
		"node-1": {
			Devices: policy.DeviceUsageList{
				DeviceLists: []*policy.DeviceListsScore{{
					Device: &device.DeviceUsage{
						ID: "GPU-half", Index: 0,
						Usedmem: 4096, Totalmem: 8192, Totalcore: 100, Usedcores: 50,
						Type: "NVIDIA", Mode: "hami-core",
					},
				}},
			},
		},
	}

	reg := prometheus.NewPedanticRegistry()
	reg.MustRegister(ClusterManagerCollector{
		ClusterManager: &ClusterManager{},
		metricsProvider: &fakeMetricsProvider{
			nodeUsage:    nodeUsage,
			quotaManager: device.NewQuotaManager(),
			podManager:   device.NewPodManager(),
		},
	})

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}

	for _, family := range families {
		// Limit metrics report capacity, not the fraction in use, so only the
		// allocation metrics can be checked against the fixture's one half.
		if !strings.HasSuffix(family.GetName(), "_allocated_ratio") {
			continue
		}
		m, ok := catalog.Lookup(family.GetName())
		if !ok {
			continue
		}
		var wantValue float64
		switch m.Unit {
		case catalog.UnitPercent:
			wantValue = 50
		case catalog.UnitRatio:
			wantValue = 0.5
		default:
			continue
		}
		for _, sample := range family.GetMetric() {
			if got := sample.GetGauge().GetValue(); got != wantValue {
				t.Errorf("%s is declared as %s but a half-allocated device reads %v, want %v",
					family.GetName(), m.Unit, got, wantValue)
			}
		}
	}
}
