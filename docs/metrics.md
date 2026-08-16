# HAMi metrics reference

<!--
Generated from pkg/metrics/catalog. Do not edit by hand.
Regenerate with: go test ./pkg/metrics/catalog/ -run TestMetricsReferenceIsUpToDate -update
-->

This page lists the metrics HAMi exports, the unit of each value, the labels it
carries, and what makes the series count grow.

The units matter because two metrics whose names both end in `_ratio` do not
share a scale. Check the unit column before writing an alert threshold or setting
a Grafana panel unit.

## Units

| Unit | Value range | Grafana panel unit |
| --- | --- | --- |
| `bytes` | byte count | `bytes` |
| `count` | count of things, dimensionless | `none` |
| `info` | always 1, the information is in the labels | `none` |
| `percent` | 0 to 100 | `percent` |
| `ratio` | 0 to 1 | `percentunit` |
| `seconds` | seconds | `s` |

## Labels added to every series

Both the scheduler and the vGPU monitor register their collector through
`prometheus.WrapRegistererWith`, so every sample carries these on top of the
labels listed for the metric itself.

- `zone`

## scheduler

| Metric | Type | Unit | Labels |
| --- | --- | --- | --- |
| `hami_build_info` | gauge | info | `build_date`, `compiler`, `go_version`, `platform`, `revision`, `version` |
| `hami_gpu_core_allocated_ratio` | gauge | percent | `node`, `device_uuid`, `device_index`, `device_type` |
| `hami_gpu_core_limit_ratio` | gauge | percent | `node`, `device_uuid`, `device_index`, `device_type` |
| `hami_gpu_memory_allocated_bytes` | gauge | bytes | `node`, `device_uuid`, `device_index`, `device_cores`, `device_type` |
| `hami_gpu_memory_limit_bytes` | gauge | bytes | `node`, `device_uuid`, `device_index`, `device_type` |
| `hami_gpu_shared_count` | gauge | count | `node`, `device_uuid`, `device_index`, `device_type` |
| `hami_node_gpu_memory_allocated_ratio` | gauge | ratio | `node`, `device_uuid`, `device_index` |
| `hami_node_gpu_mig_instance_info` | gauge | info | `node`, `device_uuid`, `device_index`, `mig_uuid`, `profile`, `gpu_instance_id`, `compute_instance_id`, `placement_start`, `placement_size` |
| `hami_node_gpu_overview` | gauge | bytes | `node`, `device_uuid`, `device_index`, `device_cores`, `device_memory_limit`, `device_type` |
| `hami_resource_quota_used` | gauge | count | `namespace`, `quota_name`, `limit` |
| `hami_scheduler_bind_total` | counter | count | `result`, `reason` |
| `hami_scheduler_filter_duration_seconds` | histogram | seconds | `result` |
| `hami_scheduler_filter_total` | counter | count | `result`, `reason` |
| `hami_vgpu_core_allocated_ratio` | gauge | percent | `namespace`, `node`, `pod`, `container_index`, `device_uuid` |
| `hami_vgpu_memory_allocated_bytes` | gauge | bytes | `namespace`, `node`, `pod`, `container_index`, `device_uuid` |

### What each metric means

**`hami_build_info`** hami build metadata exposed as labels with a constant value of 1.

- Cardinality: One series per running component version.
- Example: `count by (version) (hami_build_info)`

**`hami_gpu_core_allocated_ratio`** Device core allocated for a certain GPU

- Cardinality: One series per physical device.
- Example: `hami_gpu_core_allocated_ratio > 80`

**`hami_gpu_core_limit_ratio`** Device core limit for a certain GPU

- Cardinality: One series per physical device.
- Example: `hami_gpu_core_limit_ratio - hami_gpu_core_allocated_ratio`

**`hami_gpu_memory_allocated_bytes`** Device memory allocated for a certain GPU

- Cardinality: One series per physical device.
- Example: `sum by (node) (hami_gpu_memory_allocated_bytes)`

**`hami_gpu_memory_limit_bytes`** Device memory limit for a certain GPU

- Cardinality: One series per physical device.
- Example: `sum(hami_gpu_memory_limit_bytes)`

**`hami_gpu_shared_count`** Number of containers sharing this GPU

- Cardinality: One series per physical device.
- Example: `topk(10, hami_gpu_shared_count)`

**`hami_node_gpu_memory_allocated_ratio`** GPU Memory Allocated Percentage on a certain GPU

- Cardinality: One series per physical device.
- Example: `100 * hami_node_gpu_memory_allocated_ratio > 90`

**`hami_node_gpu_mig_instance_info`** Realized MIG instance identity and scheduler placement

- Cardinality: One series per realized MIG instance. Zero on clusters not using MIG.
- Example: `count by (profile) (hami_node_gpu_mig_instance_info)`

**`hami_node_gpu_overview`** GPU overview on a certain node

- Cardinality: One series per physical device, but device_cores and device_memory_limit carry values in labels, so a device whose capacity changes leaves a stale series behind.
- Example: `hami_node_gpu_overview`

**`hami_resource_quota_used`** resourcequota usage for a certain device

- Cardinality: One series per namespace and quota name.
- Example: `hami_resource_quota_used`

**`hami_scheduler_bind_total`** Bind requests handled by the scheduler extender, by outcome

- Cardinality: One series per result and reason pair. The reason set is closed, so this does not grow with the cluster.
- Example: `sum(rate(hami_scheduler_bind_total{result="failed"}[5m])) by (reason)`

**`hami_scheduler_filter_duration_seconds`** Time the scheduler extender spent handling a filter request

- Cardinality: One series per result and bucket, 12 buckets.
- Example: `histogram_quantile(0.99, sum(rate(hami_scheduler_filter_duration_seconds_bucket[5m])) by (le))`

**`hami_scheduler_filter_total`** Filter requests handled by the scheduler extender, by outcome

- Cardinality: One series per result and reason pair. The reason set is closed, so this does not grow with the cluster.
- Example: `sum(rate(hami_scheduler_filter_total{result="failed"}[5m])) by (reason)`

**`hami_vgpu_core_allocated_ratio`** vGPU core allocated from a container

- Cardinality: One series per container and device. Grows with pod churn.
- Example: `sum by (namespace) (hami_vgpu_core_allocated_ratio)`

**`hami_vgpu_memory_allocated_bytes`** vGPU memory allocated from a container

- Cardinality: One series per container and device. Grows with pod churn.
- Example: `sum by (namespace) (hami_vgpu_memory_allocated_bytes)`


## vgpu-monitor

| Metric | Type | Unit | Labels |
| --- | --- | --- | --- |
| `hami_build_info` | gauge | info | `build_date`, `compiler`, `go_version`, `platform`, `revision`, `version` |
| `hami_container_device_memory_bytes` | gauge | bytes | `namespace`, `pod`, `container`, `vdevice_index`, `device_uuid` |
| `hami_container_device_utilization_ratio` | gauge | percent | `namespace`, `pod`, `container`, `vdevice_index`, `device_uuid` |
| `hami_container_last_kernel_elapsed_seconds` | gauge | seconds | `namespace`, `pod`, `container`, `vdevice_index`, `device_uuid` |
| `hami_host_gpu_memory_used_bytes` | gauge | bytes | `device_index`, `device_uuid`, `device_type` |
| `hami_host_gpu_utilization_ratio` | gauge | percent | `device_index`, `device_uuid`, `device_type` |
| `hami_mig_device_info` | gauge | info | `namespace`, `pod`, `container`, `vdevice_index`, `device_uuid`, `mig_uuid`, `profile`, `gpu_instance_id`, `compute_instance_id` |
| `hami_vgpu_memory_buffer_bytes` | gauge | bytes | `namespace`, `pod`, `container`, `vdevice_index`, `device_uuid` |
| `hami_vgpu_memory_context_bytes` | gauge | bytes | `namespace`, `pod`, `container`, `vdevice_index`, `device_uuid` |
| `hami_vgpu_memory_limit_bytes` | gauge | bytes | `namespace`, `pod`, `container`, `vdevice_index`, `device_uuid` |
| `hami_vgpu_memory_module_bytes` | gauge | bytes | `namespace`, `pod`, `container`, `vdevice_index`, `device_uuid` |
| `hami_vgpu_memory_used_bytes` | gauge | bytes | `namespace`, `pod`, `container`, `vdevice_index`, `device_uuid` |

### What each metric means

**`hami_build_info`** hami build metadata exposed as labels with a constant value of 1.

- Cardinality: One series per running component version.
- Example: `count by (version) (hami_build_info)`

**`hami_container_device_memory_bytes`** Container device memory usage in bytes

- Cardinality: One series per container and vdevice. Carries the same value as hami_vgpu_memory_used_bytes.
- Example: `hami_container_device_memory_bytes`

**`hami_container_device_utilization_ratio`** Container device SM utilization ratio

- Cardinality: One series per container and vdevice.
- Example: `avg by (namespace, pod) (hami_container_device_utilization_ratio)`

**`hami_container_last_kernel_elapsed_seconds`** Seconds since last kernel execution in container

- Cardinality: One series per container and vdevice that has run at least one kernel.
- Example: `hami_container_last_kernel_elapsed_seconds > 3600`

**`hami_host_gpu_memory_used_bytes`** GPU device memory usage in bytes

- Cardinality: One series per physical device on the node.
- Example: `hami_host_gpu_memory_used_bytes`

**`hami_host_gpu_utilization_ratio`** GPU core utilization ratio (0-100)

- Cardinality: One series per physical device on the node.
- Example: `avg_over_time(hami_host_gpu_utilization_ratio[1h])`

**`hami_mig_device_info`** MIG runtime identity for a container allocation

- Cardinality: One series per container MIG allocation. Zero on clusters not using MIG.
- Example: `hami_mig_device_info`

**`hami_vgpu_memory_buffer_bytes`** Container device memory buffer size in bytes

- Cardinality: One series per container and vdevice.
- Example: `hami_vgpu_memory_buffer_bytes`

**`hami_vgpu_memory_context_bytes`** Container device memory context size in bytes

- Cardinality: One series per container and vdevice.
- Example: `hami_vgpu_memory_context_bytes`

**`hami_vgpu_memory_limit_bytes`** vGPU device memory limit in bytes

- Cardinality: One series per container and vdevice.
- Example: `hami_vgpu_memory_used_bytes / hami_vgpu_memory_limit_bytes`

**`hami_vgpu_memory_module_bytes`** Container device memory module size in bytes

- Cardinality: One series per container and vdevice.
- Example: `hami_vgpu_memory_module_bytes`

**`hami_vgpu_memory_used_bytes`** vGPU device memory usage in bytes

- Cardinality: One series per container and vdevice.
- Example: `topk(10, hami_vgpu_memory_used_bytes)`

