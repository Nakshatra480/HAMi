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
	"fmt"
	"strings"
)

// unitRanges describes the range of each unit for the generated reference.
var unitRanges = []struct {
	unit  Unit
	value string
}{
	{UnitBytes, "byte count"},
	{UnitCount, "count of things, dimensionless"},
	{UnitInfo, "always 1, the information is in the labels"},
	{UnitPercent, "0 to 100"},
	{UnitRatio, "0 to 1"},
	{UnitSeconds, "seconds"},
}

const docHeader = `# HAMi metrics reference

<!--
Generated from pkg/metrics/catalog. Do not edit by hand.
Regenerate with: go test ./pkg/metrics/catalog/ -run TestMetricsReferenceIsUpToDate -update
-->

This page lists the metrics HAMi exports, the unit of each value, the labels it
carries, and what makes the series count grow.

The units matter because two metrics whose names both end in ` + "`_ratio`" + ` do not
share a scale. Check the unit column before writing an alert threshold or setting
a Grafana panel unit.
`

// Markdown renders the catalog as the metrics reference document.
func Markdown() string {
	var b strings.Builder
	b.WriteString(docHeader)

	b.WriteString("\n## Units\n\n")
	b.WriteString("| Unit | Value range | Grafana panel unit |\n| --- | --- | --- |\n")
	for _, u := range unitRanges {
		fmt.Fprintf(&b, "| `%s` | %s | `%s` |\n", u.unit, u.value, GrafanaUnit(u.unit))
	}

	b.WriteString("\n## Labels added to every series\n\n")
	b.WriteString("Both the scheduler and the vGPU monitor register their collector through\n")
	b.WriteString("`prometheus.WrapRegistererWith`, so every sample carries these on top of the\n")
	b.WriteString("labels listed for the metric itself.\n\n")
	for _, label := range WrapperLabels {
		fmt.Fprintf(&b, "- `%s`\n", label)
	}

	for _, component := range []Component{ComponentScheduler, ComponentMonitor} {
		fmt.Fprintf(&b, "\n## %s\n\n", component)
		b.WriteString("| Metric | Type | Unit | Labels |\n| --- | --- | --- | --- |\n")
		for _, m := range ForComponent(component) {
			fmt.Fprintf(&b, "| `%s` | %s | %s | %s |\n", m.Name, m.Kind, m.Unit, labelList(m.Labels))
		}

		b.WriteString("\n### What each metric means\n\n")
		for _, m := range ForComponent(component) {
			fmt.Fprintf(&b, "**`%s`** %s\n\n", m.Name, m.Help)
			fmt.Fprintf(&b, "- Cardinality: %s\n", m.Cardinality)
			fmt.Fprintf(&b, "- Example: `%s`\n\n", m.Example)
		}
	}

	return b.String()
}

func labelList(labels []string) string {
	if len(labels) == 0 {
		return "none"
	}
	quoted := make([]string, 0, len(labels))
	for _, label := range labels {
		quoted = append(quoted, "`"+label+"`")
	}
	return strings.Join(quoted, ", ")
}
