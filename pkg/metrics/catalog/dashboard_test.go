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
	"path/filepath"
	"reflect"
	"testing"
)

const shippedDashboard = "hami-vgpu-dashboard.json"

func loadShippedDashboard(t *testing.T) *Dashboard {
	t.Helper()
	path := filepath.Join("..", "..", "..", "dashboards", shippedDashboard)
	d, err := LoadDashboard(path)
	if err != nil {
		t.Fatalf("load %s: %v", path, err)
	}
	if len(d.Panels) == 0 {
		t.Fatalf("%s has no panels with queries", path)
	}
	return d
}

func TestMetricNamesIn(t *testing.T) {
	tests := []struct {
		name string
		expr string
		want []string
	}{
		{
			name: "bare selector",
			expr: `hami_gpu_shared_count`,
			want: []string{"hami_gpu_shared_count"},
		},
		{
			name: "label matcher values are not metric names",
			expr: `hami_vgpu_memory_used_bytes{namespace=~"$namespace",pod="hami_fake_metric"}`,
			want: []string{"hami_vgpu_memory_used_bytes"},
		},
		{
			name: "functions are not metric names",
			expr: `topk(10, hami_vgpu_memory_used_bytes)`,
			want: []string{"hami_vgpu_memory_used_bytes"},
		},
		{
			name: "grouping labels are not metric names",
			expr: `sum by (node) (hami_gpu_memory_allocated_bytes{node=~"$node"})`,
			want: []string{"hami_gpu_memory_allocated_bytes"},
		},
		{
			name: "vector matching keeps both sides",
			expr: `hami_host_gpu_utilization_ratio and on (device_uuid) hami_gpu_memory_limit_bytes{node=~"$node"}`,
			want: []string{"hami_gpu_memory_limit_bytes", "hami_host_gpu_utilization_ratio"},
		},
		{
			name: "arithmetic keeps both operands",
			expr: `100 * hami_vgpu_memory_used_bytes / hami_vgpu_memory_limit_bytes`,
			want: []string{"hami_vgpu_memory_limit_bytes", "hami_vgpu_memory_used_bytes"},
		},
		{
			name: "a range selector is not a metric name",
			expr: `sum by (result) (rate(hami_scheduler_filter_total[5m]))`,
			want: []string{"hami_scheduler_filter_total"},
		},
		{
			name: "histogram series keep their suffix",
			expr: `histogram_quantile(0.99, sum by (le) (rate(hami_scheduler_filter_duration_seconds_bucket[5m])))`,
			want: []string{"hami_scheduler_filter_duration_seconds_bucket"},
		},
		{
			// An offset duration sits outside any bracket, so stripping range
			// selectors alone leaves its unit letter looking like a metric.
			name: "an unbracketed duration is not a metric name",
			expr: `hami_gpu_shared_count offset 5m`,
			want: []string{"hami_gpu_shared_count"},
		},
		{
			name: "a comparison against a plain number keeps only the metric",
			expr: `avg_over_time(hami_host_gpu_utilization_ratio[1h]) > bool 90`,
			want: []string{"hami_host_gpu_utilization_ratio"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MetricNamesIn(tt.expr); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("MetricNamesIn(%q) = %v, want %v", tt.expr, got, tt.want)
			}
		})
	}
}

func TestBareSelector(t *testing.T) {
	tests := []struct {
		expr     string
		wantName string
		wantOK   bool
	}{
		{`hami_gpu_shared_count`, "hami_gpu_shared_count", true},
		{`hami_gpu_shared_count{node=~"$node"}`, "hami_gpu_shared_count", true},
		{`  hami_gpu_shared_count{node=~"$node"}  `, "hami_gpu_shared_count", true},
		{`hami_host_gpu_utilization_ratio[5m]`, "hami_host_gpu_utilization_ratio", true},
		{`sum(hami_gpu_shared_count)`, "", false},
		{`100 * hami_node_gpu_memory_allocated_ratio`, "", false},
		{`hami_a and on (device_uuid) hami_b`, "", false},
	}

	for _, tt := range tests {
		name, ok := BareSelector(tt.expr)
		if ok != tt.wantOK || name != tt.wantName {
			t.Errorf("BareSelector(%q) = (%q, %v), want (%q, %v)", tt.expr, name, ok, tt.wantName, tt.wantOK)
		}
	}
}

func TestShippedDashboardOnlyQueriesDeclaredMetrics(t *testing.T) {
	// A panel that queries a metric HAMi does not export renders an empty
	// graph, and an empty graph reads as "no GPU load" rather than "wrong
	// query". Renaming a metric without updating the dashboard fails here.
	for _, name := range loadShippedDashboard(t).MetricNames() {
		if _, ok := LookupSeries(name); !ok {
			t.Errorf("dashboard queries %q, which the catalog does not declare", name)
		}
	}
}

func TestShippedDashboardPanelUnitsMatchDeclaredUnits(t *testing.T) {
	// Only panels that plot a metric unchanged are checked. A panel that
	// aggregates or rescales in its query is free to choose any unit, so
	// judging it here would produce false failures.
	for _, panel := range loadShippedDashboard(t).Panels {
		for _, query := range panel.Queries {
			name, ok := BareSelector(query)
			if !ok {
				continue
			}
			m, ok := Lookup(name)
			if !ok {
				continue
			}
			want := GrafanaUnit(m.Unit)
			if panel.Unit != want {
				t.Errorf("panel %q plots %s unchanged with unit %q, but %s is declared as %s, which renders correctly under %q",
					panel.Title, name, panel.Unit, name, m.Unit, want)
			}
		}
	}
}
