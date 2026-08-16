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
	"os"
	"regexp"
	"slices"
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

func TestEveryReasonTheExtenderReportsIsPreInitialised(t *testing.T) {
	// A reason that Filter or Bind sets but RegisterMetrics does not create
	// would appear only after it first happened, which is the case an alert
	// needs it to be present for.
	source, err := os.ReadFile("scheduler.go")
	if err != nil {
		t.Fatalf("read scheduler.go: %v", err)
	}

	assignments := regexp.MustCompile(`reason = (reason\w+)`).FindAllStringSubmatch(string(source), -1)
	if len(assignments) == 0 {
		t.Fatal("found no reason assignments in scheduler.go, the guard is not looking at the right thing")
	}

	known := map[string]string{
		"reasonNone":                reasonNone,
		"reasonNoHAMiResource":      reasonNoHAMiResource,
		"reasonSimulation":          reasonSimulation,
		"reasonNodeUsageFailed":     reasonNodeUsageFailed,
		"reasonScoreFailed":         reasonScoreFailed,
		"reasonNoFittingNode":       reasonNoFittingNode,
		"reasonAnnotationsRejected": reasonAnnotationsRejected,
		"reasonPodNotFound":         reasonPodNotFound,
		"reasonNodeNotFound":        reasonNodeNotFound,
		"reasonNodeLocked":          reasonNodeLocked,
		"reasonBindRejected":        reasonBindRejected,
	}

	for _, match := range assignments {
		value, ok := known[match[1]]
		if !ok {
			t.Errorf("scheduler.go sets %s, which this test does not know about", match[1])
			continue
		}
		if !slices.Contains(filterReasons, value) && !slices.Contains(bindReasons, value) {
			t.Errorf("scheduler.go sets %s (%q), but RegisterMetrics never creates it", match[1], value)
		}
	}
}
