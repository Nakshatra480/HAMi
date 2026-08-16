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
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Results a scheduling phase can end in.
const (
	// resultSucceeded means the phase did what it was asked to do.
	resultSucceeded = "succeeded"
	// resultFailed means the phase could not place or bind the pod.
	resultFailed = "failed"
	// resultSkipped means the phase returned without making a decision, so
	// counting it as either of the other two would distort a success rate.
	resultSkipped = "skipped"
)

// Reasons the filter phase ends on. The set is closed so that the reason label
// cannot grow with cluster size: a reason describes the code path, never the
// pod, node or device that went through it.
const (
	reasonNone                = "none"
	reasonNoHAMiResource      = "no_hami_resource"
	reasonSimulation          = "simulation"
	reasonNodeUsageFailed     = "node_usage_failed"
	reasonScoreFailed         = "score_failed"
	reasonNoFittingNode       = "no_fitting_node"
	reasonAnnotationsRejected = "annotations_rejected"
)

// Reasons the bind phase ends on.
const (
	reasonPodNotFound  = "pod_not_found"
	reasonNodeNotFound = "node_not_found"
	reasonNodeLocked   = "node_lock_failed"
	reasonBindRejected = "bind_rejected"
)

var (
	filterTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "hami_scheduler_filter_total",
			Help: "Filter requests handled by the scheduler extender, by outcome",
		},
		[]string{"result", "reason"},
	)

	bindTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "hami_scheduler_bind_total",
			Help: "Bind requests handled by the scheduler extender, by outcome",
		},
		[]string{"result", "reason"},
	)

	filterDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name: "hami_scheduler_filter_duration_seconds",
			Help: "Time the scheduler extender spent handling a filter request",
			// A filter that fits devices on a handful of nodes lands near a
			// millisecond; one that walks a large cluster under lock contention
			// runs into seconds. The range covers both without needing a
			// bucket per node count.
			Buckets: prometheus.ExponentialBuckets(0.001, 2, 12),
		},
		[]string{"result"},
	)
)

// filterReasons and bindReasons are every reason each phase can report. They
// are also what the metrics are pre-initialised from.
var (
	filterReasons = []string{
		reasonNone,
		reasonNoHAMiResource,
		reasonSimulation,
		reasonNodeUsageFailed,
		reasonScoreFailed,
		reasonNoFittingNode,
		reasonAnnotationsRejected,
	}
	bindReasons = []string{
		reasonNone,
		reasonPodNotFound,
		reasonNodeNotFound,
		reasonNodeLocked,
		reasonAnnotationsRejected,
		reasonBindRejected,
	}
)

// RegisterMetrics adds the extender's scheduling metrics to reg. The caller
// passes the same registerer the rest of HAMi's scheduler metrics go through,
// so these carry the same wrapper labels.
//
// Every label combination is created at zero here rather than on first use. A
// counter that is absent until something goes wrong cannot be alerted on: an
// alert like rate(hami_scheduler_bind_total{result="failed"}[5m]) > 0 matches
// no series on a healthy cluster, and would still match no series during the
// first failure, because rate needs two samples of a series that already
// exists. Starting at zero also lets a dashboard show a real 0 instead of a
// gap.
func RegisterMetrics(reg prometheus.Registerer) {
	reg.MustRegister(filterTotal, bindTotal, filterDuration)

	for _, reason := range filterReasons {
		filterTotal.WithLabelValues(resultFor(reason), reason)
	}
	for _, reason := range bindReasons {
		bindTotal.WithLabelValues(resultFor(reason), reason)
	}
	for _, result := range []string{resultSucceeded, resultFailed, resultSkipped} {
		filterDuration.WithLabelValues(result)
	}
}

// resultFor maps a reason to the outcome it represents. Keeping the mapping in
// one place stops a new reason from being counted as a success by accident.
func resultFor(reason string) string {
	switch reason {
	case reasonNone:
		return resultSucceeded
	case reasonNoHAMiResource, reasonSimulation:
		return resultSkipped
	default:
		return resultFailed
	}
}

func observeFilter(reason string, elapsed time.Duration) {
	result := resultFor(reason)
	filterTotal.WithLabelValues(result, reason).Inc()
	filterDuration.WithLabelValues(result).Observe(elapsed.Seconds())
}

func observeBind(reason string) {
	bindTotal.WithLabelValues(resultFor(reason), reason).Inc()
}
