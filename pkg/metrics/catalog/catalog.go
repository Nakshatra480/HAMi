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

// Package catalog declares the metrics HAMi exports, together with the unit,
// label set and value range of each one.
//
// The declaration is the reference the rest of the tree is checked against: the
// collectors are checked so the catalog cannot fall behind the code, the shipped
// Grafana dashboard is checked so a panel cannot plot a metric on the wrong
// scale, and docs/metrics.md is generated from it so the documented units are
// the declared ones.
package catalog

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// Component is the HAMi binary that exports a metric.
type Component string

const (
	// ComponentScheduler exports allocation and scheduling metrics.
	ComponentScheduler Component = "scheduler"
	// ComponentMonitor exports runtime metrics read from the device and from
	// each container's shared-memory usage cache.
	ComponentMonitor Component = "vgpu-monitor"
	// ComponentCommon covers metrics every HAMi binary registers.
	ComponentCommon Component = "common"
)

// Kind is the Prometheus metric type.
type Kind string

const (
	KindGauge     Kind = "gauge"
	KindCounter   Kind = "counter"
	KindHistogram Kind = "histogram"
)

// Unit is the unit of a metric's value. It records the range a sample can take,
// which is what a dashboard panel and an alert threshold both depend on.
type Unit string

const (
	// UnitBytes is a byte count.
	UnitBytes Unit = "bytes"
	// UnitPercent is a value between 0 and 100.
	UnitPercent Unit = "percent"
	// UnitRatio is a value between 0 and 1.
	UnitRatio Unit = "ratio"
	// UnitSeconds is a duration in seconds.
	UnitSeconds Unit = "seconds"
	// UnitCount is a dimensionless count of things.
	UnitCount Unit = "count"
	// UnitInfo is the constant 1. The information is carried by the labels.
	UnitInfo Unit = "info"
)

// GrafanaUnit is the Grafana field unit that displays u correctly without
// rescaling. A panel that plots a metric under any other unit is either wrong
// or has to rescale in its query.
func GrafanaUnit(u Unit) string {
	switch u {
	case UnitBytes:
		return "bytes"
	case UnitPercent:
		return "percent"
	case UnitRatio:
		return "percentunit"
	case UnitSeconds:
		return "s"
	default:
		return "none"
	}
}

// Metric is one exported metric family.
type Metric struct {
	// Name is the metric family name as it appears in a scrape.
	Name string
	// Component is the binary that exports it.
	Component Component
	// Kind is the Prometheus type.
	Kind Kind
	// Unit is the unit of the value, including its range.
	Unit Unit
	// Labels are the per-sample labels, excluding the wrapper labels every
	// collector in the same registry adds. Sorted.
	Labels []string
	// Help is the HELP string the collector emits.
	Help string
	// Cardinality says what drives the series count for this metric.
	Cardinality string
	// Example is a PromQL query that answers a question an operator asks.
	Example string
}

// WrapperLabels are added by the registering component rather than by the
// metric itself. The scheduler and the vGPU monitor both register their
// collector through prometheus.WrapRegistererWith, so the samples that
// collector produces carry these on top of the labels declared on the Metric.
//
// Metrics under ComponentCommon are registered straight onto the registry and
// do not go through the wrapper, so they do not carry these labels.
var WrapperLabels = []string{"zone"}

// EffectiveLabels returns the labels a scrape actually shows for m, sorted.
func EffectiveLabels(m Metric) []string {
	if m.Component == ComponentCommon {
		return append([]string(nil), m.Labels...)
	}
	out := append([]string(nil), m.Labels...)
	out = append(out, WrapperLabels...)
	sort.Strings(out)
	return out
}

// metrics is the declared set. Keep it sorted by component, then by name.
var metrics = []Metric{
	{
		Name:        "hami_build_info",
		Component:   ComponentCommon,
		Kind:        KindGauge,
		Unit:        UnitInfo,
		Labels:      []string{"build_date", "compiler", "go_version", "platform", "revision", "version"},
		Help:        "hami build metadata exposed as labels with a constant value of 1.",
		Cardinality: "One series per running component version.",
		Example:     `count by (version) (hami_build_info)`,
	},
	{
		Name:        "hami_gpu_core_allocated_ratio",
		Component:   ComponentScheduler,
		Kind:        KindGauge,
		Unit:        UnitPercent,
		Labels:      []string{"device_index", "device_type", "device_uuid", "node"},
		Help:        "Device core allocated for a certain GPU",
		Cardinality: "One series per physical device.",
		Example:     `hami_gpu_core_allocated_ratio > 80`,
	},
	{
		Name:        "hami_gpu_core_limit_ratio",
		Component:   ComponentScheduler,
		Kind:        KindGauge,
		Unit:        UnitPercent,
		Labels:      []string{"device_index", "device_type", "device_uuid", "node"},
		Help:        "Device core limit for a certain GPU",
		Cardinality: "One series per physical device.",
		Example:     `hami_gpu_core_limit_ratio - hami_gpu_core_allocated_ratio`,
	},
	{
		Name:        "hami_gpu_memory_allocated_bytes",
		Component:   ComponentScheduler,
		Kind:        KindGauge,
		Unit:        UnitBytes,
		Labels:      []string{"device_cores", "device_index", "device_type", "device_uuid", "node"},
		Help:        "Device memory allocated for a certain GPU",
		Cardinality: "One series per physical device.",
		Example:     `sum by (node) (hami_gpu_memory_allocated_bytes)`,
	},
	{
		Name:        "hami_gpu_memory_limit_bytes",
		Component:   ComponentScheduler,
		Kind:        KindGauge,
		Unit:        UnitBytes,
		Labels:      []string{"device_index", "device_type", "device_uuid", "node"},
		Help:        "Device memory limit for a certain GPU",
		Cardinality: "One series per physical device.",
		Example:     `sum(hami_gpu_memory_limit_bytes)`,
	},
	{
		Name:        "hami_gpu_shared_count",
		Component:   ComponentScheduler,
		Kind:        KindGauge,
		Unit:        UnitCount,
		Labels:      []string{"device_index", "device_type", "device_uuid", "node"},
		Help:        "Number of containers sharing this GPU",
		Cardinality: "One series per physical device.",
		Example:     `topk(10, hami_gpu_shared_count)`,
	},
	{
		Name:        "hami_node_gpu_memory_allocated_ratio",
		Component:   ComponentScheduler,
		Kind:        KindGauge,
		Unit:        UnitRatio,
		Labels:      []string{"device_index", "device_uuid", "node"},
		Help:        "GPU Memory Allocated Percentage on a certain GPU",
		Cardinality: "One series per physical device.",
		Example:     `100 * hami_node_gpu_memory_allocated_ratio > 90`,
	},
	{
		Name:        "hami_node_gpu_mig_instance_info",
		Component:   ComponentScheduler,
		Kind:        KindGauge,
		Unit:        UnitInfo,
		Labels:      []string{"compute_instance_id", "device_index", "device_uuid", "gpu_instance_id", "mig_uuid", "node", "placement_size", "placement_start", "profile"},
		Help:        "Realized MIG instance identity and scheduler placement",
		Cardinality: "One series per realized MIG instance. Zero on clusters not using MIG.",
		Example:     `count by (profile) (hami_node_gpu_mig_instance_info)`,
	},
	{
		Name:        "hami_node_gpu_overview",
		Component:   ComponentScheduler,
		Kind:        KindGauge,
		Unit:        UnitBytes,
		Labels:      []string{"device_cores", "device_index", "device_memory_limit", "device_type", "device_uuid", "node"},
		Help:        "GPU overview on a certain node",
		Cardinality: "One series per physical device, but device_cores and device_memory_limit carry values in labels, so a device whose capacity changes leaves a stale series behind.",
		Example:     `hami_node_gpu_overview`,
	},
	{
		Name:        "hami_resource_quota_used",
		Component:   ComponentScheduler,
		Kind:        KindGauge,
		Unit:        UnitCount,
		Labels:      []string{"limit", "namespace", "quota_name"},
		Help:        "resourcequota usage for a certain device",
		Cardinality: "One series per namespace and quota name.",
		Example:     `hami_resource_quota_used`,
	},
	{
		Name:        "hami_scheduler_bind_total",
		Component:   ComponentScheduler,
		Kind:        KindCounter,
		Unit:        UnitCount,
		Labels:      []string{"reason", "result"},
		Help:        "Bind requests handled by the scheduler extender, by outcome",
		Cardinality: "One series per result and reason pair. The reason set is closed, so this does not grow with the cluster.",
		Example:     `sum(rate(hami_scheduler_bind_total{result="failed"}[5m])) by (reason)`,
	},
	{
		Name:        "hami_scheduler_filter_duration_seconds",
		Component:   ComponentScheduler,
		Kind:        KindHistogram,
		Unit:        UnitSeconds,
		Labels:      []string{"result"},
		Help:        "Time the scheduler extender spent handling a filter request",
		Cardinality: "One series per result and bucket, 12 buckets.",
		Example:     `histogram_quantile(0.99, sum(rate(hami_scheduler_filter_duration_seconds_bucket[5m])) by (le))`,
	},
	{
		Name:        "hami_scheduler_filter_total",
		Component:   ComponentScheduler,
		Kind:        KindCounter,
		Unit:        UnitCount,
		Labels:      []string{"reason", "result"},
		Help:        "Filter requests handled by the scheduler extender, by outcome",
		Cardinality: "One series per result and reason pair. The reason set is closed, so this does not grow with the cluster.",
		Example:     `sum(rate(hami_scheduler_filter_total{result="failed"}[5m])) by (reason)`,
	},
	{
		Name:        "hami_vgpu_core_allocated_ratio",
		Component:   ComponentScheduler,
		Kind:        KindGauge,
		Unit:        UnitPercent,
		Labels:      []string{"container_index", "device_uuid", "namespace", "node", "pod"},
		Help:        "vGPU core allocated from a container",
		Cardinality: "One series per container and device. Grows with pod churn.",
		Example:     `sum by (namespace) (hami_vgpu_core_allocated_ratio)`,
	},
	{
		Name:        "hami_vgpu_memory_allocated_bytes",
		Component:   ComponentScheduler,
		Kind:        KindGauge,
		Unit:        UnitBytes,
		Labels:      []string{"container_index", "device_uuid", "namespace", "node", "pod"},
		Help:        "vGPU memory allocated from a container",
		Cardinality: "One series per container and device. Grows with pod churn.",
		Example:     `sum by (namespace) (hami_vgpu_memory_allocated_bytes)`,
	},
	{
		Name:        "hami_container_device_memory_bytes",
		Component:   ComponentMonitor,
		Kind:        KindGauge,
		Unit:        UnitBytes,
		Labels:      []string{"container", "device_uuid", "namespace", "pod", "vdevice_index"},
		Help:        "Container device memory usage in bytes",
		Cardinality: "One series per container and vdevice. Carries the same value as hami_vgpu_memory_used_bytes.",
		Example:     `hami_container_device_memory_bytes`,
	},
	{
		Name:        "hami_container_device_utilization_ratio",
		Component:   ComponentMonitor,
		Kind:        KindGauge,
		Unit:        UnitPercent,
		Labels:      []string{"container", "device_uuid", "namespace", "pod", "vdevice_index"},
		Help:        "Container device SM utilization ratio",
		Cardinality: "One series per container and vdevice.",
		Example:     `avg by (namespace, pod) (hami_container_device_utilization_ratio)`,
	},
	{
		Name:        "hami_container_last_kernel_elapsed_seconds",
		Component:   ComponentMonitor,
		Kind:        KindGauge,
		Unit:        UnitSeconds,
		Labels:      []string{"container", "device_uuid", "namespace", "pod", "vdevice_index"},
		Help:        "Seconds since last kernel execution in container",
		Cardinality: "One series per container and vdevice that has run at least one kernel.",
		Example:     `hami_container_last_kernel_elapsed_seconds > 3600`,
	},
	{
		Name:        "hami_host_gpu_memory_used_bytes",
		Component:   ComponentMonitor,
		Kind:        KindGauge,
		Unit:        UnitBytes,
		Labels:      []string{"device_index", "device_type", "device_uuid"},
		Help:        "GPU device memory usage in bytes",
		Cardinality: "One series per physical device on the node.",
		Example:     `hami_host_gpu_memory_used_bytes`,
	},
	{
		Name:        "hami_host_gpu_utilization_ratio",
		Component:   ComponentMonitor,
		Kind:        KindGauge,
		Unit:        UnitPercent,
		Labels:      []string{"device_index", "device_type", "device_uuid"},
		Help:        "GPU core utilization ratio (0-100)",
		Cardinality: "One series per physical device on the node.",
		Example:     `avg_over_time(hami_host_gpu_utilization_ratio[1h])`,
	},
	{
		Name:        "hami_mig_device_info",
		Component:   ComponentMonitor,
		Kind:        KindGauge,
		Unit:        UnitInfo,
		Labels:      []string{"compute_instance_id", "container", "device_uuid", "gpu_instance_id", "mig_uuid", "namespace", "pod", "profile", "vdevice_index"},
		Help:        "MIG runtime identity for a container allocation",
		Cardinality: "One series per container MIG allocation. Zero on clusters not using MIG.",
		Example:     `hami_mig_device_info`,
	},
	{
		Name:        "hami_vgpu_memory_buffer_bytes",
		Component:   ComponentMonitor,
		Kind:        KindGauge,
		Unit:        UnitBytes,
		Labels:      []string{"container", "device_uuid", "namespace", "pod", "vdevice_index"},
		Help:        "Container device memory buffer size in bytes",
		Cardinality: "One series per container and vdevice.",
		Example:     `hami_vgpu_memory_buffer_bytes`,
	},
	{
		Name:        "hami_vgpu_memory_context_bytes",
		Component:   ComponentMonitor,
		Kind:        KindGauge,
		Unit:        UnitBytes,
		Labels:      []string{"container", "device_uuid", "namespace", "pod", "vdevice_index"},
		Help:        "Container device memory context size in bytes",
		Cardinality: "One series per container and vdevice.",
		Example:     `hami_vgpu_memory_context_bytes`,
	},
	{
		Name:        "hami_vgpu_memory_limit_bytes",
		Component:   ComponentMonitor,
		Kind:        KindGauge,
		Unit:        UnitBytes,
		Labels:      []string{"container", "device_uuid", "namespace", "pod", "vdevice_index"},
		Help:        "vGPU device memory limit in bytes",
		Cardinality: "One series per container and vdevice.",
		Example:     `hami_vgpu_memory_used_bytes / hami_vgpu_memory_limit_bytes`,
	},
	{
		Name:        "hami_vgpu_memory_module_bytes",
		Component:   ComponentMonitor,
		Kind:        KindGauge,
		Unit:        UnitBytes,
		Labels:      []string{"container", "device_uuid", "namespace", "pod", "vdevice_index"},
		Help:        "Container device memory module size in bytes",
		Cardinality: "One series per container and vdevice.",
		Example:     `hami_vgpu_memory_module_bytes`,
	},
	{
		Name:        "hami_vgpu_memory_used_bytes",
		Component:   ComponentMonitor,
		Kind:        KindGauge,
		Unit:        UnitBytes,
		Labels:      []string{"container", "device_uuid", "namespace", "pod", "vdevice_index"},
		Help:        "vGPU device memory usage in bytes",
		Cardinality: "One series per container and vdevice.",
		Example:     `topk(10, hami_vgpu_memory_used_bytes)`,
	},
}

// index and byName are built once, so Lookup does not walk the slice and All
// does not re-sort it on every call. The declaration above is grouped by
// component to read well; callers want it by name.
var (
	index = func() map[string]Metric {
		m := make(map[string]Metric, len(metrics))
		for _, entry := range metrics {
			m[entry.Name] = entry
		}
		return m
	}()

	byName = func() []Metric {
		out := make([]Metric, len(metrics))
		copy(out, metrics)
		sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
		return out
	}()
)

// All returns every declared metric, sorted by name. The result is a copy, so a
// caller cannot reorder or corrupt the declaration.
func All() []Metric {
	out := make([]Metric, len(byName))
	copy(out, byName)
	return out
}

// ForComponent returns the metrics exported by c, sorted by name. Metrics
// declared under ComponentCommon are included for every component, since every
// HAMi binary registers them.
func ForComponent(c Component) []Metric {
	var out []Metric
	for _, entry := range All() {
		if entry.Component == c || entry.Component == ComponentCommon {
			out = append(out, entry)
		}
	}
	return out
}

// Lookup returns the declared metric with the given name.
func Lookup(name string) (Metric, bool) {
	entry, ok := index[name]
	return entry, ok
}

// LookupSeries is Lookup for a series name as it appears in a scrape or a
// query. A histogram family is declared once but scrapes as three series, so
// hami_x_seconds_bucket resolves back to hami_x_seconds.
func LookupSeries(series string) (Metric, bool) {
	if entry, ok := Lookup(series); ok {
		return entry, true
	}
	for _, suffix := range histogramSuffixes {
		base, found := strings.CutSuffix(series, suffix)
		if !found {
			continue
		}
		if entry, ok := Lookup(base); ok && entry.Kind == KindHistogram {
			return entry, true
		}
	}
	return Metric{}, false
}

// Names returns the names of the metrics exported by c, including the common
// ones, sorted.
func Names(c Component) []string {
	entries := ForComponent(c)
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry.Name)
	}
	return out
}

// Validate reports the declaration errors that the catalog can check about
// itself: duplicate names, missing fields, and labels that are not sorted or
// that collide with a wrapper label.
func Validate() error {
	seen := make(map[string]bool, len(metrics))
	for _, entry := range metrics {
		if seen[entry.Name] {
			return fmt.Errorf("metric %q is declared twice", entry.Name)
		}
		seen[entry.Name] = true

		switch {
		case entry.Component == "":
			return fmt.Errorf("metric %q has no component", entry.Name)
		case entry.Kind == "":
			return fmt.Errorf("metric %q has no kind", entry.Name)
		case entry.Unit == "":
			return fmt.Errorf("metric %q has no unit", entry.Name)
		case entry.Help == "":
			return fmt.Errorf("metric %q has no help string", entry.Name)
		case entry.Cardinality == "":
			return fmt.Errorf("metric %q does not say what drives its cardinality", entry.Name)
		case entry.Example == "":
			return fmt.Errorf("metric %q has no example query", entry.Name)
		}

		if !sort.StringsAreSorted(entry.Labels) {
			return fmt.Errorf("metric %q has unsorted labels %v", entry.Name, entry.Labels)
		}
		for _, label := range entry.Labels {
			if slices.Contains(WrapperLabels, label) {
				return fmt.Errorf("metric %q declares wrapper label %q", entry.Name, label)
			}
		}
	}
	return nil
}
