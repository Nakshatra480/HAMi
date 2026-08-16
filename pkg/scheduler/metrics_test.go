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

package scheduler

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	promtestutil "github.com/prometheus/client_golang/prometheus/testutil"
)

// registerSchedulingMetrics gives the test its own registry and clears the
// package-level metrics, which are shared by every test in this package.
func registerSchedulingMetrics(t *testing.T) *prometheus.Registry {
	t.Helper()
	filterTotal.Reset()
	bindTotal.Reset()
	filterDuration.Reset()
	t.Cleanup(func() {
		filterTotal.Reset()
		bindTotal.Reset()
		filterDuration.Reset()
	})

	reg := prometheus.NewPedanticRegistry()
	RegisterMetrics(reg)
	return reg
}

func TestResultForMapsEveryReason(t *testing.T) {
	tests := map[string]string{
		reasonNone:                resultSucceeded,
		reasonNoHAMiResource:      resultSkipped,
		reasonSimulation:          resultSkipped,
		reasonNodeUsageFailed:     resultFailed,
		reasonScoreFailed:         resultFailed,
		reasonNoFittingNode:       resultFailed,
		reasonAnnotationsRejected: resultFailed,
		reasonPodNotFound:         resultFailed,
		reasonNodeNotFound:        resultFailed,
		reasonNodeLocked:          resultFailed,
		reasonBindRejected:        resultFailed,
	}

	for reason, want := range tests {
		if got := resultFor(reason); got != want {
			t.Errorf("resultFor(%q) = %q, want %q", reason, got, want)
		}
	}

	// An unknown reason has to count as a failure. Treating it as a success
	// would hide the outcome it was added to describe.
	if got := resultFor("something_new"); got != resultFailed {
		t.Errorf("resultFor(unknown) = %q, want %q", got, resultFailed)
	}
}

func TestRegisterMetricsStartsEverySeriesAtZero(t *testing.T) {
	reg := registerSchedulingMetrics(t)

	if got := promtestutil.CollectAndCount(reg, "hami_scheduler_filter_total"); got != len(filterReasons) {
		t.Errorf("filter counter has %d series after registration, want %d", got, len(filterReasons))
	}
	if got := promtestutil.CollectAndCount(reg, "hami_scheduler_bind_total"); got != len(bindReasons) {
		t.Errorf("bind counter has %d series after registration, want %d", got, len(bindReasons))
	}

	for _, reason := range filterReasons {
		if got := promtestutil.ToFloat64(filterTotal.WithLabelValues(resultFor(reason), reason)); got != 0 {
			t.Errorf("filter counter for reason %q starts at %v, want 0", reason, got)
		}
	}
}

func TestObserveFilterCountsTheOutcomeAndTheDuration(t *testing.T) {
	reg := registerSchedulingMetrics(t)

	observeFilter(reasonNone, 5*time.Millisecond)
	observeFilter(reasonNoFittingNode, 12*time.Millisecond)
	observeFilter(reasonNoFittingNode, 9*time.Millisecond)

	if got := promtestutil.ToFloat64(filterTotal.WithLabelValues(resultSucceeded, reasonNone)); got != 1 {
		t.Errorf("succeeded filters = %v, want 1", got)
	}
	if got := promtestutil.ToFloat64(filterTotal.WithLabelValues(resultFailed, reasonNoFittingNode)); got != 2 {
		t.Errorf("filters with no fitting node = %v, want 2", got)
	}
	// A skipped filter that never ran must not appear as a success.
	if got := promtestutil.ToFloat64(filterTotal.WithLabelValues(resultSkipped, reasonNoHAMiResource)); got != 0 {
		t.Errorf("skipped filters = %v, want 0", got)
	}

	if got := promtestutil.CollectAndCount(reg, "hami_scheduler_filter_duration_seconds"); got == 0 {
		t.Error("filter duration histogram exported nothing")
	}
}

func TestObserveBindCountsTheOutcome(t *testing.T) {
	registerSchedulingMetrics(t)

	observeBind(reasonNone)
	observeBind(reasonNodeLocked)

	if got := promtestutil.ToFloat64(bindTotal.WithLabelValues(resultSucceeded, reasonNone)); got != 1 {
		t.Errorf("succeeded binds = %v, want 1", got)
	}
	if got := promtestutil.ToFloat64(bindTotal.WithLabelValues(resultFailed, reasonNodeLocked)); got != 1 {
		t.Errorf("binds that lost the node lock = %v, want 1", got)
	}
}

// reasonConstants reads the reason constants out of metrics.go, so this test
// does not keep a second copy of them that can drift from the first.
func reasonConstants(t *testing.T) map[string]string {
	t.Helper()

	file, err := parser.ParseFile(token.NewFileSet(), "metrics.go", nil, 0)
	if err != nil {
		t.Fatalf("parse metrics.go: %v", err)
	}

	out := map[string]string{}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok || len(value.Names) != 1 || len(value.Values) != 1 {
				continue
			}
			literal, ok := value.Values[0].(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				continue
			}
			unquoted, err := strconv.Unquote(literal.Value)
			if err != nil {
				continue
			}
			out[value.Names[0].Name] = unquoted
		}
	}
	return out
}

// reasonsAssignedIn returns the reason constants that path assigns to the
// reason variable it reports at the end of the request.
func reasonsAssignedIn(t *testing.T, path string) []string {
	t.Helper()

	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	var names []string
	ast.Inspect(file, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok || len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
			return true
		}
		target, ok := assign.Lhs[0].(*ast.Ident)
		if !ok || target.Name != "reason" {
			return true
		}
		if source, ok := assign.Rhs[0].(*ast.Ident); ok {
			names = append(names, source.Name)
		}
		return true
	})
	return names
}

func TestEveryReasonTheExtenderReportsIsPreInitialised(t *testing.T) {
	// A reason that Filter or Bind sets but RegisterMetrics does not create
	// would appear only after it first happened, which is the case an alert
	// needs it to be present for.
	constants := reasonConstants(t)
	assigned := reasonsAssignedIn(t, "scheduler.go")
	if len(assigned) == 0 {
		t.Fatal("found no reason assignments in scheduler.go, the guard is not looking at the right thing")
	}

	for _, name := range assigned {
		value, ok := constants[name]
		if !ok {
			t.Errorf("scheduler.go sets reason to %s, which is not a constant in metrics.go", name)
			continue
		}
		if !slices.Contains(filterReasons, value) && !slices.Contains(bindReasons, value) {
			t.Errorf("scheduler.go sets %s (%q), but RegisterMetrics never creates it", name, value)
		}
	}
}
