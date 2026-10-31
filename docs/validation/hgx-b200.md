# HGX B200 validation

Hardware validation of the B200 architecture support added in
[dsx-ai-factory/health-validation#225](https://github.com/dsx-ai-factory/health-validation/pull/225),
run by Black Forest Labs on an HGX B200 node on 2026-10-25. The native
`health-validation run` output for every run is in [`hgx-b200/`](hgx-b200/);
only `node_id` and GPU UUIDs are replaced.

## Setup

| | |
|---|---|
| Build | `fea1785` (#225 head), `catalog_version` `v0.0.0-20261023185001-fea178518f08`, verdict schema 0.3 |
| Hardware | Supermicro AS-4126GS-NBR-LCC (liquid-cooled), 8 × NVIDIA B200, VBIOS 97.00.E4.00.17 |
| Driver | 580.178.04 (#225 was developed against 615.71) |
| Platform | Kubernetes (RKE2) with GPU Operator v26.7.0; the CLI ran in a privileged host-network pod |
| NVLink fabric | `nvidia-smi -q`: Fabric State `Completed`, Status `Success`, CliqueId 0 |

```
health-validation run --node-id <node> --execution-context OPERATOR_INFRA
health-validation run --node-id <node> --execution-context TENANT_GUEST
```

## Results

| Run | Overall | Ran | Skipped | Output |
|---|---|---|---|---|
| OPERATOR_INFRA | PASS | 153 | 9 | [`b200-operator-infra.json`](hgx-b200/b200-operator-infra.json) |
| TENANT_GUEST | PASS | 161 | 8 | [`b200-tenant-guest.json`](hgx-b200/b200-tenant-guest.json) |
| Negative test | CRITICAL | 153 | 9 | [`b200-negative-overrides.json`](hgx-b200/b200-negative-overrides.json) |

Checks #225 enables for B200; one result per GPU unless noted:

| Check | Result | Observed |
|---|---|---|
| `gpu_count` (node) | PASS | 8; `expected_gpu_count` resolves to 8 from `PerArchitecture` |
| `nvlink_active` | PASS ×8 | 0 inactive |
| `nvlink_p2p` | PASS ×8 | 0 unhealthy peers |
| `nvlink_capabilities` | PASS ×8 | every capability supported |
| `nvlink_bandwidth_count_speed` | PASS ×8 | 18 links × 53125 MB/s |
| `gpu_average_power_draw` | PASS ×8 | about 198 W at idle |
| `gpu_power_state` | PASS ×8 | P0 |
| `gpu_sensor_throttle` | PASS ×8 | no hardware, thermal, or power-brake slowdown |
| `gpu_compute_apps` (TENANT_GUEST) | PASS ×8 | 0 processes |
| `rdma_linkflap_fatal` (node) | PASS | no outages |
| Xid checks | PASS ×94 | includes the 27 checks for Xid 144–150 (NVLink 5) |

Skipped by architecture, as intended: `gpu_power_limit`, `nvlink_fabric`,
`nvlink_fabric_health`, `numa_node_count`, `gpu_xid_142` (GB200/GB300 only),
and `gpu_xid_54`, `gpu_xid_80`, `gpu_xid_136` (Hopper/Ampere). In
OPERATOR_INFRA, `gpu_compute_apps` also skips as
`SIGNAL_UNAVAILABLE_DEPLOYMENT_MODEL`.

### Negative test

To confirm the B200 checks evaluate rather than pass vacuously, the negative
run overrides two thresholds with values the node cannot meet:

```yaml
checks:
  gpu_count:
    expected_gpu_count: 9
  nvlink_bandwidth_count_speed:
    expected_link_count: 19
```

The run exits 1 with overall CRITICAL: `gpu_count` reports "Expected 9 GPUs,
found 8", and `nvlink_bandwidth_count_speed` is CRITICAL on all 8 GPUs
("expected 19 NVLinks, found 18"). Both recommend
`reset_gpu > restart_os > service_required`.

## Observations

1. **Power limit and cooling.** This is the liquid-cooled chassis, yet every
   GPU reports current, default, and maximum power limit all at 1000 W, not
   the 1200 W associated with liquid cooling. The cooling variant cannot be
   inferred from the product, which supports leaving `gpu_power_limit` to a
   per-fleet override for B200. If it ever gets a default, the NVML maximum
   limit is a better reference than the SKU.
2. **RDMA rails.** `rdma_linkflap_fatal` passes on this node although 3 of
   its 8 east-west RoCE rails (400 Gb/s) were administratively down and had
   never come up: nothing flapped. No catalog check reads port state, and the
   `infiniband.sysfs` collect sensor drops Ethernet link-layer ports, so RoCE
   rails never reach a check.
3. **NVSwitch in DCGM.** On this node dcgm-exporter (GPU Operator v26.7.0)
   logs "no switches to monitor", while per-GPU NVLink bandwidth metrics are
   present. That is consistent with skipping `nvlink_fabric_health` on HGX
   B200.
