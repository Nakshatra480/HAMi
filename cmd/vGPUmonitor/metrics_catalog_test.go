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
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/Project-HAMi/HAMi/pkg/metrics/catalog"
)

// descFields pulls the name and the variable labels out of a descriptor's
// string form. Neither is reachable through an exported field, and the monitor
// cannot be scraped in a unit test because Collect needs a real NVML device.
var descFields = regexp.MustCompile(`fqName: "([^"]+)".*variableLabels: \{([^}]*)\}`)

// restoreLegacyDescriptors puts the package-level legacy descriptors back to
// what they were when the test started.
func restoreLegacyDescriptors(t *testing.T) {
	t.Helper()
	saved := []struct {
		target **prometheus.Desc
		value  *prometheus.Desc
	}{
		{&legacyHostGPUdesc, legacyHostGPUdesc},
		{&legacyHostGPUUtilizationdesc, legacyHostGPUUtilizationdesc},
		{&legacyCtrvGPUdesc, legacyCtrvGPUdesc},
		{&legacyCtrvGPUlimitdesc, legacyCtrvGPUlimitdesc},
		{&legacyCtrDeviceMemorydesc, legacyCtrDeviceMemorydesc},
		{&legacyCtrDeviceUtilizationdesc, legacyCtrDeviceUtilizationdesc},
		{&legacyCtrDeviceLastKernelDesc, legacyCtrDeviceLastKernelDesc},
		{&legacyCtrDeviceMigInfo, legacyCtrDeviceMigInfo},
	}
	t.Cleanup(func() {
		for _, entry := range saved {
			*entry.target = entry.value
		}
	})
}

// describeMonitorMetrics returns the metrics the monitor's collector declares,
// mapped to their sorted label names.
func describeMonitorMetrics(t *testing.T, legacy bool) map[string][]string {
	t.Helper()

	if legacy {
		// The legacy descriptors are package-level and start nil, and
		// sendLegacyMetric uses that nil to decide whether to emit. Leaving
		// them populated would make every later test in this package see
		// legacy metrics it did not ask for.
		restoreLegacyDescriptors(t)
		initLegacyDescriptors()
	}
	collector := ClusterManagerCollector{
		ClusterManager: &ClusterManager{Zone: "vGPU", LegacyMetrics: legacy},
	}

	ch := make(chan *prometheus.Desc, 64)
	go func() {
		collector.Describe(ch)
		close(ch)
	}()

	got := make(map[string][]string)
	for desc := range ch {
		fields := descFields.FindStringSubmatch(desc.String())
		if fields == nil {
			t.Fatalf("could not read name and labels out of %s", desc.String())
		}
		var labels []string
		if fields[2] != "" {
			labels = strings.Split(fields[2], ",")
		}
		sort.Strings(labels)
		got[fields[1]] = labels
	}
	return got
}

func TestMonitorDescribesExactlyTheCatalogMetrics(t *testing.T) {
	got := describeMonitorMetrics(t, false)

	declared := make(map[string]bool)
	for _, name := range catalog.Names(catalog.ComponentMonitor) {
		declared[name] = true
	}
	// The monitor's collector does not own the build info metric; main.go
	// registers it straight onto the registry, so it never reaches Describe.
	delete(declared, "hami_build_info")

	for name := range got {
		if !declared[name] {
			t.Errorf("monitor describes %q, which the catalog does not declare", name)
		}
	}
	for name := range declared {
		if _, ok := got[name]; !ok {
			t.Errorf("catalog declares %q for the monitor, but Describe did not send it", name)
		}
	}
}

func TestMonitorMetricLabelsMatchTheCatalog(t *testing.T) {
	got := describeMonitorMetrics(t, false)

	for _, m := range catalog.ForComponent(catalog.ComponentMonitor) {
		labels, ok := got[m.Name]
		if !ok {
			continue // reported by TestMonitorDescribesExactlyTheCatalogMetrics
		}
		// Describe runs before the registry wrapper adds the zone label, so
		// the descriptors carry the metric's own labels only.
		want := m.Labels
		if len(want) == 0 {
			want = nil
		}
		if !reflect.DeepEqual(labels, want) {
			t.Errorf("%s labels = %v, catalog declares %v", m.Name, labels, want)
		}
	}
}

func TestLegacyMetricsAreNotInTheCatalog(t *testing.T) {
	// Legacy names stay undeclared on purpose. They exist to keep old
	// dashboards alive behind --legacy-metrics, and documenting them next to
	// the current names would invite new dashboards to use them.
	for name := range describeMonitorMetrics(t, true) {
		if strings.HasPrefix(name, "hami_") {
			continue
		}
		if _, ok := catalog.Lookup(name); ok {
			t.Errorf("legacy metric %q is declared in the catalog", name)
		}
	}
}
