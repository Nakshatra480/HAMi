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
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	promtestutil "github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"

	"github.com/Project-HAMi/HAMi/pkg/device"
	versionmetrics "github.com/Project-HAMi/HAMi/pkg/metrics"
	"github.com/Project-HAMi/HAMi/pkg/metrics/catalog"
	schedulerpkg "github.com/Project-HAMi/HAMi/pkg/scheduler"
	"github.com/Project-HAMi/HAMi/pkg/scheduler/policy"
)

var updateGolden = flag.Bool("update-golden", false, "rewrite the golden scrape in cmd/scheduler/testdata")

// newSchedulerRegistry wires up the scheduler's collectors exactly the way
// initMetrics does, including the zone wrapper, over a fixture that exercises
// every branch of the collector.
func newSchedulerRegistry(t *testing.T) *prometheus.Registry {
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

	return reg
}

// gatherSchedulerMetrics scrapes that registry and returns one entry per
// exported metric family holding its sorted label names.
func gatherSchedulerMetrics(t *testing.T) map[string][]string {
	t.Helper()

	families, err := newSchedulerRegistry(t).Gather()
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

// knownLintExceptions are Prometheus naming problems that predate this branch.
// They are listed rather than silenced so that the check still fails if a new
// metric adds to them, and so the list itself is a to-do a maintainer can see.
var knownLintExceptions = map[string]string{
	"hami_gpu_shared_count": `non-histogram and non-summary metrics should not have "_count" suffix`,
}

func TestSchedulerMetricsPassPrometheusLint(t *testing.T) {
	// promlint is the Prometheus project's own view of whether a metric is
	// named and documented conventionally. Running it here means a new metric
	// is judged by that standard at review time rather than after someone has
	// already built a dashboard on it.
	problems, err := promtestutil.GatherAndLint(newSchedulerRegistry(t))
	if err != nil {
		t.Fatalf("lint: %v", err)
	}

	for _, problem := range problems {
		if known, ok := knownLintExceptions[problem.Metric]; ok && known == problem.Text {
			t.Logf("known, pre-existing: %s: %s", problem.Metric, problem.Text)
			continue
		}
		t.Errorf("%s: %s", problem.Metric, problem.Text)
	}
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

// formatFamilies renders a scrape the way a golden file can be compared
// against: one line per sample, labels in name order with their values.
//
// It is written here rather than taken from expfmt so that the branch adds no
// direct dependency. Labels are printed with their values attached, which is
// what makes this catch a reordering of a metric's declared label list: the
// names stay the same and the values move between them.
func formatFamilies(families []*dto.MetricFamily) string {
	var b strings.Builder
	for _, family := range families {
		// Build info carries the Go version, platform and revision of whoever
		// ran the test, so it cannot be pinned in a file shared across
		// machines. Its labels are constant and cannot be reordered anyway.
		if family.GetName() == "hami_build_info" {
			continue
		}
		for _, metric := range family.GetMetric() {
			pairs := make([]string, 0, len(metric.GetLabel()))
			for _, label := range metric.GetLabel() {
				pairs = append(pairs, fmt.Sprintf("%s=%q", label.GetName(), label.GetValue()))
			}
			sort.Strings(pairs)

			var value float64
			switch {
			case metric.GetGauge() != nil:
				value = metric.GetGauge().GetValue()
			case metric.GetCounter() != nil:
				value = metric.GetCounter().GetValue()
			case metric.GetHistogram() != nil:
				value = float64(metric.GetHistogram().GetSampleCount())
			}
			fmt.Fprintf(&b, "%s{%s} %g\n", family.GetName(), strings.Join(pairs, ","), value)
		}
	}
	return b.String()
}

func TestSchedulerScrapeMatchesGolden(t *testing.T) {
	// The catalog stores each metric's labels in the order the collector passes
	// values, because Desc matches the two by position. Nothing about that is
	// visible in a set comparison: swapping two names in the declaration keeps
	// the same label set and silently moves the values between them. This
	// golden holds the pairing itself.
	families, err := newSchedulerRegistry(t).Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	got := formatFamilies(families)

	path := filepath.Join("testdata", "scheduler_scrape.golden")
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		t.Logf("wrote %s", path)
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if got != string(want) {
		t.Errorf("scrape does not match %s.\n\ngot:\n%s\nwant:\n%s\n"+
			"If this change is intended, regenerate with:\n"+
			"  go test ./cmd/scheduler/ -run TestSchedulerScrapeMatchesGolden -update-golden",
			path, got, want)
	}
}
