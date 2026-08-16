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

package catalog

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite docs/metrics.md from the catalog")

// repoRoot walks up from the test's working directory to the directory holding
// go.mod. The alternative is a relative path like ../../../, which silently
// depends on where this package sits in the tree.
func repoRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod above %s", dir)
		}
		dir = parent
	}
}

func TestCatalogIsWellFormed(t *testing.T) {
	if err := Validate(); err != nil {
		t.Fatalf("catalog is not well formed: %v", err)
	}
}

func TestEveryMetricNameIsPrefixed(t *testing.T) {
	// A shared Prometheus instance scrapes many exporters. Anything HAMi
	// exports has to be identifiable as HAMi's from the name alone.
	for _, m := range All() {
		if !strings.HasPrefix(m.Name, "hami_") {
			t.Errorf("metric %q does not start with hami_", m.Name)
		}
	}
}

func TestLookupFindsDeclaredMetrics(t *testing.T) {
	for _, m := range All() {
		got, ok := Lookup(m.Name)
		if !ok {
			t.Errorf("Lookup(%q) found nothing", m.Name)
			continue
		}
		if got.Unit != m.Unit {
			t.Errorf("Lookup(%q) unit = %q, want %q", m.Name, got.Unit, m.Unit)
		}
	}
	if _, ok := Lookup("hami_not_a_real_metric"); ok {
		t.Error("Lookup found a metric that is not declared")
	}
}

func TestLookupSeriesResolvesHistogramSuffixes(t *testing.T) {
	const family = "hami_scheduler_filter_duration_seconds"
	for _, suffix := range histogramSuffixes {
		got, ok := LookupSeries(family + suffix)
		if !ok {
			t.Errorf("LookupSeries(%q) found nothing", family+suffix)
			continue
		}
		if got.Name != family {
			t.Errorf("LookupSeries(%q) resolved to %q, want %q", family+suffix, got.Name, family)
		}
	}

	// A gauge does not expand into those series, so the suffix has to stay
	// unresolved rather than quietly matching the family.
	if _, ok := LookupSeries("hami_gpu_shared_count_bucket"); ok {
		t.Error("LookupSeries resolved a bucket series for a gauge")
	}
}

func TestForComponentIncludesCommonMetrics(t *testing.T) {
	for _, component := range []Component{ComponentScheduler, ComponentMonitor} {
		var sawCommon bool
		for _, m := range ForComponent(component) {
			if m.Component == ComponentCommon {
				sawCommon = true
			}
			if m.Component != component && m.Component != ComponentCommon {
				t.Errorf("ForComponent(%s) returned %q from %s", component, m.Name, m.Component)
			}
		}
		if !sawCommon {
			t.Errorf("ForComponent(%s) dropped the common metrics", component)
		}
	}
}

func TestAllReturnsACopy(t *testing.T) {
	first := All()
	if len(first) == 0 {
		t.Fatal("catalog is empty")
	}
	first[0].Name = "mutated"
	if All()[0].Name == "mutated" {
		t.Error("All() handed out the backing array, a caller can corrupt the catalog")
	}
}

func TestGrafanaUnitSeparatesRatioFromPercent(t *testing.T) {
	// The whole point of the unit field is that these two do not render the
	// same way. percentunit multiplies by 100 for display, percent does not.
	if GrafanaUnit(UnitRatio) == GrafanaUnit(UnitPercent) {
		t.Fatal("ratio and percent map to the same Grafana unit")
	}
}

func TestMetricsReferenceIsUpToDate(t *testing.T) {
	path := filepath.Join(repoRoot(t), "docs", "metrics.md")
	want := Markdown()

	if *update {
		if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		t.Logf("wrote %s", path)
		return
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(got) != want {
		t.Errorf("%s is out of date, regenerate it with:\n"+
			"  go test ./pkg/metrics/catalog/ -run TestMetricsReferenceIsUpToDate -update", path)
	}
}

func TestMustDescBuildsFromTheDeclaration(t *testing.T) {
	desc := MustDesc("hami_gpu_memory_limit_bytes")
	rendered := desc.String()

	entry, _ := Lookup("hami_gpu_memory_limit_bytes")
	if !strings.Contains(rendered, entry.Help) {
		t.Errorf("descriptor does not carry the declared help string: %s", rendered)
	}
	// The labels have to appear in declared order, because that is the order
	// the collector passes values in.
	if want := strings.Join(entry.Labels, ","); !strings.Contains(rendered, want) {
		t.Errorf("descriptor labels are not %q: %s", want, rendered)
	}
}

func TestMustDescRejectsAnUndeclaredMetric(t *testing.T) {
	// A collector that names a metric the catalog does not know about should
	// fail at startup, not export something undocumented.
	defer func() {
		if recover() == nil {
			t.Error("MustDesc accepted a metric that is not declared")
		}
	}()
	MustDesc("hami_not_a_real_metric")
}

func TestMustDescRejectsNonGaugeMetrics(t *testing.T) {
	// Counters and histograms are built by their own constructors, which own
	// their descriptors. Handing one out here would produce a second
	// descriptor for the same name.
	defer func() {
		if recover() == nil {
			t.Error("MustDesc handed out a descriptor for a counter")
		}
	}()
	MustDesc("hami_scheduler_filter_total")
}
