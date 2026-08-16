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
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

// Panel is one panel of a Grafana dashboard, reduced to the fields that have to
// agree with the catalog.
type Panel struct {
	Title string
	// Unit is the Grafana field unit the panel renders its values with.
	Unit string
	// Queries are the PromQL expressions the panel runs.
	Queries []string
}

// Dashboard is a Grafana dashboard reduced to its panels.
type Dashboard struct {
	Title  string
	Panels []Panel
}

type rawDashboard struct {
	Title  string     `json:"title"`
	Panels []rawPanel `json:"panels"`
}

type rawPanel struct {
	Title       string `json:"title"`
	Type        string `json:"type"`
	FieldConfig struct {
		Defaults struct {
			Unit string `json:"unit"`
		} `json:"defaults"`
	} `json:"fieldConfig"`
	Targets []struct {
		Expr string `json:"expr"`
	} `json:"targets"`
	// A collapsed row keeps its children nested instead of at the top level.
	Panels []rawPanel `json:"panels"`
}

// LoadDashboard reads a Grafana dashboard from path.
func LoadDashboard(path string) (*Dashboard, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read dashboard: %w", err)
	}
	var raw rawDashboard
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("parse dashboard %s: %w", path, err)
	}

	d := &Dashboard{Title: raw.Title}
	var walk func(panels []rawPanel)
	walk = func(panels []rawPanel) {
		for _, p := range panels {
			walk(p.Panels)
			if p.Type == "row" {
				continue
			}
			queries := make([]string, 0, len(p.Targets))
			for _, t := range p.Targets {
				if strings.TrimSpace(t.Expr) != "" {
					queries = append(queries, t.Expr)
				}
			}
			if len(queries) == 0 {
				continue
			}
			d.Panels = append(d.Panels, Panel{
				Title:   p.Title,
				Unit:    p.FieldConfig.Defaults.Unit,
				Queries: queries,
			})
		}
	}
	walk(raw.Panels)
	return d, nil
}

// MetricNames returns every metric name the dashboard queries, sorted and
// deduplicated.
func (d *Dashboard) MetricNames() []string {
	seen := make(map[string]bool)
	for _, p := range d.Panels {
		for _, q := range p.Queries {
			for _, name := range MetricNamesIn(q) {
				seen[name] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

var (
	labelMatcher   = regexp.MustCompile(`\{[^{}]*\}`)
	stringLiteral  = regexp.MustCompile(`"[^"]*"|'[^']*'`)
	modifierList   = regexp.MustCompile(`\b(by|without|on|ignoring|group_left|group_right)\s*\([^()]*\)`)
	rangeSelector  = regexp.MustCompile(`\[[^\[\]]*\]`)
	duration       = regexp.MustCompile(`\b\d+(\.\d+)?(ms|s|m|h|d|w|y)\b`)
	identifier     = regexp.MustCompile(`[a-zA-Z_:][a-zA-Z0-9_:]*\s*\(?`)
	bareSelectorRe = regexp.MustCompile(`^\s*([a-zA-Z_:][a-zA-Z0-9_:]*)\s*(\{[^{}]*\})?\s*(\[[^\[\]]*\])?\s*$`)
)

// histogramSuffixes are the series a histogram family expands into at scrape
// time. A dashboard queries these, not the family name.
var histogramSuffixes = []string{"_bucket", "_sum", "_count"}

// promQLKeywords are the words a lexical scan would otherwise mistake for
// metric names. Function names are excluded separately, by dropping any
// identifier followed by an opening parenthesis.
var promQLKeywords = map[string]bool{
	"and": true, "or": true, "unless": true, "by": true, "without": true,
	"on": true, "ignoring": true, "group_left": true, "group_right": true,
	"offset": true, "bool": true, "start": true, "end": true, "atan2": true,
	"inf": true, "nan": true,
}

// MetricNamesIn returns the metric names referenced by a PromQL expression.
//
// This is a lexical scan, not a PromQL parse: HAMi does not depend on the
// Prometheus server module and pulling it in for a test would be a large
// dependency for a small job. It strips string literals, label matchers,
// aggregation modifier lists and range selectors, then takes the identifiers
// that are left and are not followed by an opening parenthesis. That covers the
// expressions a dashboard panel holds. It would miss a metric named only inside
// a label matcher value, which is not something a panel query does.
func MetricNamesIn(expr string) []string {
	cleaned := stringLiteral.ReplaceAllString(expr, `""`)
	cleaned = modifierList.ReplaceAllString(cleaned, " ")
	cleaned = labelMatcher.ReplaceAllString(cleaned, " ")
	cleaned = rangeSelector.ReplaceAllString(cleaned, " ")
	// A duration's unit letter reads as an identifier once the digits in front
	// of it are skipped, so 5m in "offset 5m" would be reported as a metric
	// named m. Range selectors are already gone by here; this catches the
	// durations that are not bracketed.
	cleaned = duration.ReplaceAllString(cleaned, " ")

	seen := make(map[string]bool)
	var out []string
	for _, match := range identifier.FindAllString(cleaned, -1) {
		if strings.HasSuffix(strings.TrimSpace(match), "(") {
			continue
		}
		name := strings.TrimSpace(match)
		if promQLKeywords[name] || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// BareSelector returns the metric name when expr selects a single metric and
// does nothing else to it, so the panel renders the metric's own values on the
// metric's own scale. It returns false for any expression that aggregates or
// rescales, because those can legitimately change the unit.
func BareSelector(expr string) (string, bool) {
	m := bareSelectorRe.FindStringSubmatch(expr)
	if m == nil {
		return "", false
	}
	name := m[1]
	if promQLKeywords[name] {
		return "", false
	}
	return name, true
}
