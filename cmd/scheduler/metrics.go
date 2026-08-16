/*
Copyright 2024 The HAMi Authors.

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
	"fmt"
	"log"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	klog "k8s.io/klog/v2"

	"github.com/Project-HAMi/HAMi/pkg/device"
	versionmetrics "github.com/Project-HAMi/HAMi/pkg/metrics"
	"github.com/Project-HAMi/HAMi/pkg/metrics/catalog"
	schedulerpkg "github.com/Project-HAMi/HAMi/pkg/scheduler"
)

type ClusterManager struct {
	Zone          string
	LegacyMetrics bool

	legacyOnce  sync.Once
	legacyDescs *legacyDescriptors
}

// legacy returns the legacy descriptors when LegacyMetrics is set, and nil when
// it is not. They are built once per ClusterManager rather than at package
// level, so a collector running with the flag on cannot leave them switched on
// for one running with it off.
func (c *ClusterManager) legacy() *legacyDescriptors {
	if !c.LegacyMetrics {
		return nil
	}
	c.legacyOnce.Do(func() { c.legacyDescs = newLegacyDescriptors() })
	return c.legacyDescs
}

type schedulerMetricsProvider interface {
	InspectAllNodesUsage() *map[string]*schedulerpkg.NodeUsage
	GetQuotaManager() *device.QuotaManager
	GetPodManager() *device.PodManager
}

// ClusterManagerCollector implements the Collector interface.
type ClusterManagerCollector struct {
	ClusterManager  *ClusterManager
	metricsProvider schedulerMetricsProvider
}

const normalizedCoreLimit = 100

// metricsZone is the value of the zone label every scheduler series carries.
const metricsZone = "vGPU"

// normalizeAMDCoreMetrics converts AMD physical CU counts to the percentage
// unit used by HAMi's core ratio metrics. Other devices keep their existing
// metric values.
func normalizeAMDCoreMetrics(deviceType string, total, allocated int32) (float64, float64) {
	if !strings.HasPrefix(strings.ToUpper(deviceType), "AMD") || total <= 0 {
		return float64(total), float64(allocated)
	}
	return normalizedCoreLimit, math.Ceil(float64(allocated) / float64(total) * normalizedCoreLimit)
}

// findNodeDeviceUsage looks up a device by UUID across every node's usage and
// returns its total core capacity and type. ok is false when no node advertises
// the device, in which case the caller falls back to emitting raw values.
func findNodeDeviceUsage(nu *map[string]*schedulerpkg.NodeUsage, uuid string) (totalcore int32, deviceType string, ok bool) {
	for _, ni := range *nu {
		for _, dls := range ni.Devices.DeviceLists {
			if dls.Device != nil && dls.Device.ID == uuid {
				return dls.Device.Totalcore, dls.Device.Type, true
			}
		}
	}
	return 0, "", false
}

// mibToBytes converts a memory quantity expressed in mebibytes (MiB), the unit
// used throughout HAMi's device accounting, to bytes for byte-oriented metrics.
func mibToBytes(mib int32) float64 {
	return float64(mib) * 1024 * 1024
}

// Descriptors for every metric this collector emits, built from the metric
// catalog rather than from literals here. A metric that is not declared there
// cannot be described, so it cannot be emitted, and the help text and label
// order have one source instead of two.
//
// They are package-level because Collect runs on every scrape and rebuilding
// eleven descriptors each time is work that produces the same eleven values.
var (
	nodevGPUMemoryLimitDesc     = catalog.MustDesc("hami_gpu_memory_limit_bytes")
	nodevGPUCoreLimitDesc       = catalog.MustDesc("hami_gpu_core_limit_ratio")
	nodevGPUMemoryAllocatedDesc = catalog.MustDesc("hami_gpu_memory_allocated_bytes")
	nodevGPUSharedNumDesc       = catalog.MustDesc("hami_gpu_shared_count")
	nodeGPUCoreAllocatedDesc    = catalog.MustDesc("hami_gpu_core_allocated_ratio")
	nodeGPUOverview             = catalog.MustDesc("hami_node_gpu_overview")
	nodeGPUMemoryPercentage     = catalog.MustDesc("hami_node_gpu_memory_allocated_ratio")
	nodeGPUMigInstance          = catalog.MustDesc("hami_node_gpu_mig_instance_info")
	quotaUsedDesc               = catalog.MustDesc("hami_resource_quota_used")

	ctrvGPUdeviceAllocatedMemoryDesc = catalog.MustDesc("hami_vgpu_memory_allocated_bytes")
	ctrvGPUdeviceAllocatedCoreDesc   = catalog.MustDesc("hami_vgpu_core_allocated_ratio")

	// collectorDescs is the current-name half of what Describe sends. The
	// legacy half is added separately, because it exists only when
	// --legacy-metrics is set.
	collectorDescs = []*prometheus.Desc{
		nodevGPUMemoryLimitDesc, nodevGPUCoreLimitDesc, nodevGPUMemoryAllocatedDesc,
		nodevGPUSharedNumDesc, nodeGPUCoreAllocatedDesc, nodeGPUOverview,
		nodeGPUMemoryPercentage, nodeGPUMigInstance, quotaUsedDesc,
		ctrvGPUdeviceAllocatedMemoryDesc, ctrvGPUdeviceAllocatedCoreDesc,
	}
)

// legacyDescriptors holds the pre-#1644 metric names, which are emitted
// alongside the current ones when --legacy-metrics is set.
//
// They live on the collector rather than at package level because a package
// variable is shared by every collector in the process: one test enabling
// legacy mode would leave the descriptors in place for the next one, and the
// only thing standing between that and a wrong scrape would be a nil check at
// the far end. A nil struct pointer says the same thing once, here.
type legacyDescriptors struct {
	memoryLimitDesc     *prometheus.Desc
	coreLimitDesc       *prometheus.Desc
	memoryAllocatedDesc *prometheus.Desc
	sharedNumDesc       *prometheus.Desc
	coreAllocatedDesc   *prometheus.Desc
	overview            *prometheus.Desc
	memoryPercentage    *prometheus.Desc
	migInstance         *prometheus.Desc
	quotaUsed           *prometheus.Desc
	allocatedMemory     *prometheus.Desc
	allocatedCore       *prometheus.Desc
}

func newLegacyDescriptors() *legacyDescriptors {
	return &legacyDescriptors{
		memoryLimitDesc: prometheus.NewDesc(
			"GPUDeviceMemoryLimit",
			"Device memory limit for a certain GPU",
			[]string{"nodeid", "deviceuuid", "deviceidx", "devicetype"}, nil,
		),
		coreLimitDesc: prometheus.NewDesc(
			"GPUDeviceCoreLimit",
			"Device memory core limit for a certain GPU",
			[]string{"nodeid", "deviceuuid", "deviceidx", "devicetype"}, nil,
		),
		memoryAllocatedDesc: prometheus.NewDesc(
			"GPUDeviceMemoryAllocated",
			"Device memory allocated for a certain GPU",
			[]string{"nodeid", "deviceuuid", "deviceidx", "devicecores", "devicetype"}, nil,
		),
		sharedNumDesc: prometheus.NewDesc(
			"GPUDeviceSharedNum",
			"Number of containers sharing this GPU",
			[]string{"nodeid", "deviceuuid", "deviceidx", "devicetype"}, nil,
		),
		coreAllocatedDesc: prometheus.NewDesc(
			"GPUDeviceCoreAllocated",
			"Device core allocated for a certain GPU",
			[]string{"nodeid", "deviceuuid", "deviceidx", "devicetype"}, nil,
		),
		overview: prometheus.NewDesc(
			"nodeGPUOverview",
			"GPU overview on a certain node",
			[]string{"nodeid", "deviceuuid", "deviceidx", "devicecores", "devicememorylimit", "devicetype"}, nil,
		),
		memoryPercentage: prometheus.NewDesc(
			"nodeGPUMemoryPercentage",
			"GPU Memory Allocated Percentage on a certain GPU",
			[]string{"nodeid", "deviceuuid", "deviceidx"}, nil,
		),
		migInstance: prometheus.NewDesc(
			"nodeGPUMigInstance",
			"GPU Sharing mode. 0 for hami-core, 1 for mig, 2 for mps",
			[]string{"nodeid", "deviceuuid", "deviceidx", "migname"}, nil,
		),
		quotaUsed: prometheus.NewDesc(
			"QuotaUsed",
			"resourcequota usage for a certain device",
			[]string{"quotanamespace", "quotaName", "limit"}, nil,
		),
		allocatedMemory: prometheus.NewDesc(
			"vGPUMemoryAllocated",
			"vGPU memory allocated from a container",
			[]string{"podnamespace", "nodename", "podname", "containeridx", "deviceuuid"}, nil,
		),
		allocatedCore: prometheus.NewDesc(
			"vGPUCoreAllocated",
			"vGPU core allocated from a container",
			[]string{"podnamespace", "nodename", "podname", "containeridx", "deviceuuid"}, nil,
		),
	}
}

// all returns every legacy descriptor, for Describe.
func (l *legacyDescriptors) all() []*prometheus.Desc {
	if l == nil {
		return nil
	}
	return []*prometheus.Desc{
		l.memoryLimitDesc,
		l.coreLimitDesc,
		l.memoryAllocatedDesc,
		l.sharedNumDesc,
		l.coreAllocatedDesc,
		l.overview,
		l.memoryPercentage,
		l.migInstance,
		l.quotaUsed,
		l.allocatedMemory,
		l.allocatedCore,
	}
}

// Describe sends every descriptor this collector can emit.
//
// It used to be DescribeByCollect, which asks Collect for them. That reports
// nothing at all on a scheduler with no nodes registered yet, so the registry
// could not tell an idle scheduler from a broken one, and it could not catch a
// sample emitted under a descriptor the collector never announced.
func (cc ClusterManagerCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, desc := range collectorDescs {
		ch <- desc
	}
	for _, desc := range cc.ClusterManager.legacy().all() {
		ch <- desc
	}
}

// Collect creates constant metrics for each host on the fly based on the returned data.
func (cc ClusterManagerCollector) Collect(ch chan<- prometheus.Metric) {
	klog.V(3).Info("Starting to collect metrics for scheduler")
	// nil when --legacy-metrics is off, which is what turns the old names off.
	lg := cc.ClusterManager.legacy()
	// A single snapshot is shared by the node- and container-level collectors so
	// they observe a consistent cluster state within one scrape.
	nu := cc.metricsProvider.InspectAllNodesUsage()
	cc.collectNodeMetrics(ch, nu, lg)
	cc.collectQuotaMetrics(ch, lg)
	cc.collectContainerMetrics(ch, nu, lg)
}

// collectNodeMetrics emits node-level GPU metrics (memory/core limits and
// allocations, sharing counts, MIG instances and overview) for every device on
// every node. AMD core values are normalized to a percentage.
func (cc ClusterManagerCollector) collectNodeMetrics(ch chan<- prometheus.Metric, nu *map[string]*schedulerpkg.NodeUsage, lg *legacyDescriptors) {

	for nodeID, val := range *nu {
		for _, devs := range val.Devices.DeviceLists {
			coreLimit, coreAllocated := normalizeAMDCoreMetrics(devs.Device.Type, devs.Device.Totalcore, devs.Device.Usedcores)
			if devs.Device.Mode == "mig" {
				for _, allocation := range devs.Device.MigAllocationsInUse {
					if !allocation.RuntimeReady {
						continue
					}
					klog.V(3).InfoS("MIG instance allocation",
						"profile", allocation.Profile,
						"gpuInstanceID", allocation.GPUInstanceID,
						"computeInstanceID", allocation.ComputeInstanceID,
						"migUUID", allocation.MigUUID)
					if err := sendMetric(
						ch,
						nodeGPUMigInstance,
						prometheus.GaugeValue,
						1,
						nodeID,
						devs.Device.ID,
						fmt.Sprint(devs.Device.Index),
						allocation.MigUUID,
						allocation.Profile,
						fmt.Sprint(allocation.GPUInstanceID),
						fmt.Sprint(allocation.ComputeInstanceID),
						fmt.Sprint(allocation.Placement.Start),
						fmt.Sprint(allocation.Placement.Size),
					); err != nil {
						klog.V(4).Infof("Failed to send nodeGPUMigInstance metric: %v", err)
					}
					if lg != nil {
						sendLegacyMetric(
							ch,
							lg.migInstance,
							prometheus.GaugeValue,
							1,
							nodeID, devs.Device.ID, fmt.Sprint(devs.Device.Index), allocation.Profile+"-"+fmt.Sprint(allocation.GPUInstanceID),
						)
					}
				}
			}

			if err := sendMetric(ch, nodevGPUMemoryLimitDesc, prometheus.GaugeValue, mibToBytes(devs.Device.Totalmem), nodeID, devs.Device.ID, fmt.Sprint(devs.Device.Index), devs.Device.Type); err != nil {
				klog.V(4).Infof("Failed to send nodevGPUMemoryLimitDesc metric: %v", err)
			}
			if err := sendMetric(ch, nodevGPUCoreLimitDesc, prometheus.GaugeValue, coreLimit, nodeID, devs.Device.ID, fmt.Sprint(devs.Device.Index), devs.Device.Type); err != nil {
				klog.V(4).Infof("Failed to send nodevGPUCoreLimitDesc metric: %v", err)
			}
			if err := sendMetric(ch, nodevGPUMemoryAllocatedDesc, prometheus.GaugeValue, mibToBytes(devs.Device.Usedmem), nodeID, devs.Device.ID, fmt.Sprint(devs.Device.Index), fmt.Sprint(devs.Device.Totalcore), devs.Device.Type); err != nil {
				klog.V(4).Infof("Failed to send nodevGPUMemoryAllocatedDesc metric: %v", err)
			}
			if err := sendMetric(ch, nodevGPUSharedNumDesc, prometheus.GaugeValue, float64(devs.Device.Used), nodeID, devs.Device.ID, fmt.Sprint(devs.Device.Index), devs.Device.Type); err != nil {
				klog.V(4).Infof("Failed to send nodevGPUSharedNumDesc metric: %v", err)
			}
			if err := sendMetric(ch, nodeGPUCoreAllocatedDesc, prometheus.GaugeValue, coreAllocated, nodeID, devs.Device.ID, fmt.Sprint(devs.Device.Index), devs.Device.Type); err != nil {
				klog.V(4).Infof("Failed to send nodeGPUCoreAllocatedDesc metric: %v", err)
			}
			if err := sendMetric(ch, nodeGPUOverview, prometheus.GaugeValue, mibToBytes(devs.Device.Usedmem), nodeID, devs.Device.ID, fmt.Sprint(devs.Device.Index), fmt.Sprint(devs.Device.Totalcore), fmt.Sprint(devs.Device.Totalmem), devs.Device.Type); err != nil {
				klog.V(4).Infof("Failed to send nodeGPUOverview metric: %v", err)
			}

			if devs.Device.Totalmem > 0 {
				if err := sendMetric(ch, nodeGPUMemoryPercentage, prometheus.GaugeValue, float64(devs.Device.Usedmem)/float64(devs.Device.Totalmem), nodeID, devs.Device.ID, fmt.Sprint(devs.Device.Index)); err != nil {
					klog.V(4).Infof("Failed to send nodeGPUMemoryPercentage metric: %v", err)
				}
			}

			if lg != nil {
				sendLegacyMetric(ch, lg.memoryLimitDesc, prometheus.GaugeValue, mibToBytes(devs.Device.Totalmem), nodeID, devs.Device.ID, fmt.Sprint(devs.Device.Index), devs.Device.Type)
				sendLegacyMetric(ch, lg.coreLimitDesc, prometheus.GaugeValue, float64(devs.Device.Totalcore), nodeID, devs.Device.ID, fmt.Sprint(devs.Device.Index), devs.Device.Type)
				sendLegacyMetric(ch, lg.memoryAllocatedDesc, prometheus.GaugeValue, mibToBytes(devs.Device.Usedmem), nodeID, devs.Device.ID, fmt.Sprint(devs.Device.Index), fmt.Sprint(devs.Device.Totalcore), devs.Device.Type)
				sendLegacyMetric(ch, lg.sharedNumDesc, prometheus.GaugeValue, float64(devs.Device.Used), nodeID, devs.Device.ID, fmt.Sprint(devs.Device.Index), devs.Device.Type)
				sendLegacyMetric(ch, lg.coreAllocatedDesc, prometheus.GaugeValue, float64(devs.Device.Usedcores), nodeID, devs.Device.ID, fmt.Sprint(devs.Device.Index), devs.Device.Type)
				sendLegacyMetric(ch, lg.overview, prometheus.GaugeValue, mibToBytes(devs.Device.Usedmem), nodeID, devs.Device.ID, fmt.Sprint(devs.Device.Index), fmt.Sprint(devs.Device.Totalcore), fmt.Sprint(devs.Device.Totalmem), devs.Device.Type)
				if devs.Device.Totalmem > 0 {
					sendLegacyMetric(ch, lg.memoryPercentage, prometheus.GaugeValue, float64(devs.Device.Usedmem)/float64(devs.Device.Totalmem), nodeID, devs.Device.ID, fmt.Sprint(devs.Device.Index))
				}
			}
		}
	}
}

// collectQuotaMetrics emits per-namespace resource quota usage.
func (cc ClusterManagerCollector) collectQuotaMetrics(ch chan<- prometheus.Metric, lg *legacyDescriptors) {
	for ns, val := range cc.metricsProvider.GetQuotaManager().GetResourceQuota() {
		for quotaname, q := range *val {
			if err := sendMetric(ch, quotaUsedDesc, prometheus.GaugeValue, float64(q.Used), ns, quotaname, fmt.Sprint(q.Limit)); err != nil {
				klog.V(4).Infof("Failed to send quotaUsedDesc metric: %v", err)
			}
			if lg != nil {
				sendLegacyMetric(ch, lg.quotaUsed, prometheus.GaugeValue, float64(q.Used), ns, quotaname, fmt.Sprint(q.Limit))
			}
		}
	}
}

// collectContainerMetrics emits per-container vGPU metrics for all scheduled
// pods. AMD core allocations are normalized to a percentage via
// normalizeAMDCoreMetrics (issue #2518); legacy metrics keep raw values.
func (cc ClusterManagerCollector) collectContainerMetrics(ch chan<- prometheus.Metric, nu *map[string]*schedulerpkg.NodeUsage, lg *legacyDescriptors) {
	schedpods, _ := cc.metricsProvider.GetPodManager().GetScheduledPods()
	for _, val := range schedpods {
		for _, podSingleDevice := range val.Devices {
			for ctridx, ctrdevs := range podSingleDevice {
				for _, ctrdevval := range ctrdevs {
					klog.V(4).InfoS("Collecting metrics",
						"namespace", val.Namespace,
						"podName", val.Name,
						"deviceUUID", ctrdevval.UUID,
						"usedCores", ctrdevval.Usedcores,
						"usedMem", ctrdevval.Usedmem,
						"nodeID", val.NodeID,
					)
					if len(ctrdevval.UUID) == 0 {
						klog.Warningf("Device UUID is empty, omitting metric collection for namespace=%s, podName=%s, ctridx=%d, nodeID=%s",
							val.Namespace, val.Name, ctridx, val.NodeID)
						continue
					}
					// Resolve the matching node device's total core capacity and type so
					// AMD physical compute-unit (CU) counts in Usedcores can be normalized
					// to the percentage unit used by hami_vgpu_core_allocated_ratio (#2518).
					totalcore, deviceType, found := findNodeDeviceUsage(nu, ctrdevval.UUID)
					klog.V(4).InfoS("Resolved device for container metric",
						"deviceUUID", ctrdevval.UUID,
						"totalCore", totalcore,
						"deviceType", deviceType,
						"found", found,
						"nodeID", val.NodeID,
					)
					containerLabels := []string{val.Namespace, val.NodeID, val.Name, fmt.Sprint(ctridx), ctrdevval.UUID}
					usedMemBytes := mibToBytes(ctrdevval.Usedmem)
					if err := sendMetric(ch, ctrvGPUdeviceAllocatedMemoryDesc, prometheus.GaugeValue, usedMemBytes, containerLabels...); err != nil {
						klog.V(4).Infof("Failed to send ctrvGPUdeviceAllocatedMemoryDesc metric: %v", err)
					}
					_, ctrCoreAllocated := normalizeAMDCoreMetrics(deviceType, totalcore, ctrdevval.Usedcores)
					if err := sendMetric(ch, ctrvGPUdeviceAllocatedCoreDesc, prometheus.GaugeValue, ctrCoreAllocated, containerLabels...); err != nil {
						klog.V(4).Infof("Failed to send ctrvGPUdeviceAllocatedCoreDesc metric: %v", err)
					}
					if lg != nil {
						sendLegacyMetric(ch, lg.allocatedMemory, prometheus.GaugeValue, usedMemBytes, containerLabels...)
						sendLegacyMetric(ch, lg.allocatedCore, prometheus.GaugeValue, float64(ctrdevval.Usedcores), containerLabels...)
					}
				}
			}
		}
	}
}

// NewClusterManager creates a ClusterManager and registers its collector.
func NewClusterManager(zone string, reg prometheus.Registerer, metricsProvider schedulerMetricsProvider, legacyMetrics bool) *ClusterManager {
	c := &ClusterManager{
		Zone:          zone,
		LegacyMetrics: legacyMetrics,
	}
	cc := ClusterManagerCollector{
		ClusterManager:  c,
		metricsProvider: metricsProvider,
	}
	prometheus.WrapRegistererWith(prometheus.Labels{"zone": zone}, reg).MustRegister(cc)
	return c
}

func initMetrics(bindAddress string, metricsProvider schedulerMetricsProvider, legacyMetrics bool) {
	klog.Info("Initializing metrics for scheduler")
	reg := prometheus.NewRegistry()
	reg.MustRegister(versionmetrics.NewBuildInfoCollector())

	NewClusterManager(metricsZone, reg, metricsProvider, legacyMetrics)
	// The extender's own counters go through the same wrapper the collector
	// uses, so every series the scheduler exports carries the same zone label.
	schedulerpkg.RegisterMetrics(prometheus.WrapRegistererWith(prometheus.Labels{"zone": metricsZone}, reg))

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	server := &http.Server{
		Addr:              bindAddress,
		Handler:           mux,
		ReadHeaderTimeout: 15 * time.Second,
		ReadTimeout:       60 * time.Second,
	}
	log.Fatal(server.ListenAndServe())
}

func sendLegacyMetric(ch chan<- prometheus.Metric, desc *prometheus.Desc, valueType prometheus.ValueType, value float64, labels ...string) {
	if desc == nil {
		return
	}
	if err := sendMetric(ch, desc, valueType, value, labels...); err != nil {
		klog.V(4).Infof("Failed to send legacy metric: %v", err)
	}
}

func sendMetric(ch chan<- prometheus.Metric, desc *prometheus.Desc, valueType prometheus.ValueType, value float64, labels ...string) error {
	metric, err := prometheus.NewConstMetric(desc, valueType, value, labels...)
	if err != nil {
		return fmt.Errorf("failed to create metric: %w", err)
	}
	ch <- metric
	return nil
}
