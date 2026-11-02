# Remediation Actions

When a check fails with `operational_state: drain`, its `recommended_action`
carries a `remediation_actions` array: an ordered sequence of steps a cluster
operator (or their automated remediation engine) should work
through to clear the fault. This document describes the contract for
consuming that sequence and catalogs every action this repo's own checks
recommend.

See [`catalog/health/action.go`](https://github.com/dsx-ai-factory/health-validation/blob/main/catalog/health/action.go) for the Go type
(`health.RemediationAction`) and [`catalog/health/node-verdict.schema.json`](https://github.com/dsx-ai-factory/health-validation/blob/main/catalog/health/node-verdict.schema.json)
for the JSON contract. For the field's place in the wider verdict shape, see
[Integrating into a GPU cluster](/health-validation/operating/integration).

## How to consume a remediation sequence

`remediation_actions` is ordered from the most preferred (least disruptive) step to the least.
Each failing check retains its own recommendation; drain recommendations carry
an ordered sequence. `NodeVerdict.recommended_action` also surfaces one complete
drain sequence as the node-level recommendation. Re-run the **whole suite**
after every remediation step so the recommendation is recalculated from the
checks that still fail:

1. Run `health-validation` (the full suite).
2. If everything passes, **stop** — the node is healthy.
3. Otherwise, inspect the node-level `recommended_action`. If it is absent, no
   automated drain remediation is available; inspect `check_results` for
   per-check monitor or no-action findings and stop this remediation flow. Do
   not treat the node as healthy. When present, it is selected from checks with
   `operational_state: drain` and a nonempty sequence by, in order:
   1. higher severity;
   2. each remediation step's rank, compared in sequence order, using this
      priority from highest to lowest: `service_required`, `update_swfw`,
      `restart_os`, `reset_gpu`, `restart_app`, `check_collection`, and
      `retry_collection` (actions not in this list, including `hold`,
      `xid_154`, and custom actions, share the lowest rank);
   3. the check ID; and
   4. action names in sequence order, as a stability fallback when repeated
      check IDs remain tied.
4. Take the next **untried** action from that `remediation_actions` sequence
   based on the steps the consumer has already attempted.
5. Perform that remediation step against the node.
6. Go back to step 1 and re-run the **whole suite**, not just the check you
   acted on. One action can clear several checks (for example, several checks
   recommend `reset_gpu` first). Re-deriving the failing set and priority each
   time, rather than planning up front, keeps you from spending a more
   disruptive step on a check that another check's remediation already fixed.
7. If a check's `remediation_actions` is exhausted and it's still failing,
   treat it the same as reaching a `service_required` step: escalate, don't
   keep retrying.

**Do not** perform every action in a sequence unconditionally, and do not
skip the whole-suite re-run between steps — later actions are more disruptive
(e.g. restarting a VM/bare-metal host) than earlier ones (e.g. resetting a
GPU), and are only warranted once the cheaper attempt has been confirmed not
to work.

### Why actions are ranked this way

A check's first step is the least disruptive step it expects might clear the
fault. If a check starts with a more disruptive step, lighter actions are not
expected to resolve that check, so its sequence ranks higher. Re-running the
suite after each step recalculates the recommendation as findings clear.

- `service_required` ranks highest because no automated step remains.
- `update_swfw` ranks above restarts because a restart alone does not correct a
  software or configuration incompatibility.
- `restart_os`, `reset_gpu`, and `restart_app` follow in order of disruption.
- At equal severity, `check_collection` and `retry_collection` rank last because
  they represent collection failures rather than observed faults.

## Action catalog

`RemediationAction` is an **open string type, not a closed enum** — the
constants in [`catalog/health/action.go`](https://github.com/dsx-ai-factory/health-validation/blob/main/catalog/health/action.go) are
every value this repo's own checks are capable of recommending, but a catalog
extended with custom checks may recommend its own remediation steps. A
remediation engine should treat any value it doesn't recognize as an opaque
step (log it, surface it to a human) rather than an error.

### Generic reset/restart/escalate sequence

Most checks outside the Xid catalog recommend the same default sequence:
CPU/NUMA topology, GPU count and power, most NVLink connectivity checks,
InfiniBand link health, and the CLI's own architecture-discovery failure
(`health.GenericDrain()`):

```json
"remediation_actions": ["reset_gpu", "restart_os", "service_required"]
```

Attempt a GPU-only reset first (the lightest-touch step; a remediation engine
that doesn't implement one simply no-ops through it), fall back to a
host-level restart, then escalate. Several Xid checks (below) use a shorter
two-step variant of this same pattern — `["reset_gpu", "service_required"]`,
skipping the host-restart step — for faults where a host restart isn't
expected to add anything a GPU reset wouldn't already cover.

The `gpu_compute_apps` check omits `reset_gpu`: its failing condition is a
process still holding the GPU, and `reset_gpu` requires that no process is
using it. Its sequence is `restart_os`, then `service_required`.

| Action | Description |
|---|---|
| <nobr>`reset_gpu`</nobr> | Reset the GPU: `nvidia-smi --gpu-reset` (no active processes may be using the GPU). |
| <nobr>`restart_os`</nobr> | Restart the affected node's operating system — a VM reboot on a virtualized GPU, a host-level reboot on bare metal. |
| <nobr>`service_required`</nobr> | A person needs to investigate the node, for a hardware or a software cause, through your cluster's own service path (your CSP if CSP-managed, or your organization's service process if on-prem). No automated step remains after this one. |

### Domain-hold action

`nvlink_fabric_health` is the one exception to the generic sequence's final
`service_required` step:

```json
"remediation_actions": ["reset_gpu", "restart_os", "hold"]
```

| Action | Description |
|---|---|
| <nobr>`hold`</nobr> | Hold the node out of scheduling — drained, but do not individually remediate or escalate it yet. Once a threshold number of other nodes in the **same NVLink/rack domain** have also reached `hold` (recommended: **3**), remediate the whole domain together (e.g. a single switch/rack-level repair) instead of chasing each node independently. |

An NVLink fabric/route problem (`fabric_route_unhealthy` and friends) is often
a symptom of a shared upstream component — a switch or rack-level NVLink
issue — rather than something local to one GPU. Escalating on the first held
node risks opening a service request per node for what turns out to be one
underlying fault. Holding and counting first lets the pattern (multiple nodes
in the same domain failing the same way) confirm itself before anyone
investigates, and gives a remediation engine the chance to fix the whole
domain in one pass once it does.

### Xid-catalog actions

The per-code `gpu_xid_<code>` checks recommend the generic actions above where
they fit.
For example, Xid 79 and Xid 151 recommend `["restart_os", "service_required"]`,
and Xid 142 recommends `service_required` alone. `service_required` is this repo's name for the bucket
NVIDIA's Xid catalog calls `CONTACT_SUPPORT`.

The actions below are specific to individual Xid codes. They map to the
"immediate action" resolution buckets in NVIDIA's
[Xid Errors documentation](https://docs.nvidia.com/deploy/xid-errors/latest/analyzing-xid-catalog.html).

For Xid 159 (CHI non-data error), `gpu_xid_159` recommends
`["reset_gpu", "service_required"]`. NVIDIA's `CHECK_UVM` guidance is to
reset the GPU if UVM or vGPU is in use; otherwise ignore the error.
Health Validation cannot establish whether either was in use when the
error occurred, so it recommends a GPU reset as a conservative default,
followed by service if the problem persists. The verdict message preserves
the conditional guidance for operators.

Xid 144-150 (the NVLink5/NVSwitch fault family, `gpu_xid_144`..`gpu_xid_150`)
are a special case: unlike other Xid codes, NVRM marks each occurrence Fatal
or Nonfatal directly in the log line. Each of these checks fires CRITICAL
with `["reset_gpu", "service_required"]` (the same two-step sequence as
above) on a Fatal occurrence, and WARNING with **no recommended action** on
a Nonfatal one. In NVIDIA's full Xid 144-150 decode table, about 95% of
Fatal rows resolve to a GPU reset, then service if the reset doesn't clear
it, and most Nonfatal rows need no remediation at all.

| Action | Description |
|---|---|
| <nobr>`restart_app`</nobr> | Restart the application using the GPU; the GPU itself is not considered faulted. The default for warning-class per-Xid checks. |
| <nobr>`update_swfw`</nobr> | Bring the GPU driver/firmware and related software to compatible versions, or correct the vGPU configuration. Recommended for Xid 78 (vGPU start error), raised when the guest VM's driver is incompatible with the host vGPU Manager or the vGPU type is not supported for the guest OS or GPU. The guest driver is installed inside the VM; the host vGPU Manager and the vGPU type are managed by the infrastructure operator. |
| <nobr>`xid_154`</nobr> | Follow the reported Drain P2P recovery action: disable new scheduling, quiesce P2P traffic and UVM persistence, then re-query recovery action. Reset only if Drain P2P remains. This action does not request a host reboot. |

`gpu_xid_154` supplements the originating fault; it does not replace that fault's check.
The numeric destination code, not a substring of the printed name, determines guidance:

| Reported code | Severity | Recommendation |
|---|---|---|
| `0x0` None | INFO | No new recovery action. |
| `0x1` GPU Reset Required (PF FLR in R610) | CRITICAL | Drain, terminate GPU processes, then `reset_gpu`; `service_required` if unresolved. |
| `0x2` Node Reboot Required | CRITICAL | Drain and `restart_os`. A warm reboot is sufficient. Do not restart applications before rebooting an inconsistent OS. |
| `0x3` Drain P2P | WARNING | Drain and `xid_154`, with the P2P procedure above. |
| `0x4` Drain and Reset | CRITICAL | Disable new scheduling. Let unaffected work finish or checkpoint, then terminate GPU processes and `reset_gpu`; `service_required` if unresolved. |
| `0x6` Bus Reset | CRITICAL | Drain and `restart_os`. A GPU reset does not reset the PCIe bus. |
| `0x7` System Reboot | CRITICAL | Drain and `restart_os`. |
| Missing, malformed, or unknown (including `0x5` Recover IMEX Domain) | WARNING | Monitor and investigate the transition and originating faults. |

The latest Xid 154 for each PCI device governs only that device's recovery request.
The sensor records a positive sequence from the journal reader's chronological order.
This sequence is local to one scan/measurement, not a cross-run identity or timestamp.
Observation batches can be reordered without changing the recovery state.
With missing or tied sequence evidence, all reports remain active: None cannot erase an uncertain request.
A report whose transition cannot be read is always reported as unknown. It never replaces the device's latest readable request.
None never clears another GPU's recovery request or an independently actionable originating fault.
Both numeric codes and printed names remain in event evidence and observation roundtrips.

One report is sufficient by default. The overridable `occurrences` threshold counts reports
of each device's current recovery action within the window, not failed recovery attempts.
Health Validation performs no recovery and stores no reboot-cycle state.
NVIDIA's `XID_154` guidance follows the reported action. `XID_154_EVAL` follows Xid 154 when present;
otherwise, restart the application. `IGNORE` means non-paging guidance, not meaningless evidence.
See the [Xid catalog](https://docs.nvidia.com/deploy/xid-errors/analyzing-xid-catalog.html)
and [GPU Recovery Action](https://docs.nvidia.com/deploy/nvidia-smi/index.html#gpu-recovery-action).
Codes `0x5` through `0x7` follow `nvmlDeviceGpuRecoveryAction_t` in the public
[NVML header](https://github.com/NVIDIA/go-nvml/blob/main/pkg/nvml/nvml.h).

`gpu_xid_45` (Xid 45, preemptive removal) recommends `["restart_app"]` at
WARNING when Xid 45 is the only Xid seen within the collection window. When
another Xid is present in that window, it is INFO with no action; follow the
accompanying Xid's guidance.
Xid 154 is not counted as another Xid, since it only annotates one.

### Collection-failure actions

These indicate the check's *rules never ran* — the sensor could not observe
the data needed to evaluate the check — rather than an observed hardware
fault. They are not hardware remediation steps.

| Action | Description |
|---|---|
| <nobr>`retry_collection`</nobr> | The measurement could not be collected in time (a sensor timeout). Retry collection — re-run `health-validation` — before assuming a hardware fault; investigate if it keeps timing out. |
| <nobr>`check_collection`</nobr> | The measurement could not be collected for a non-timeout reason (a driver, permission, or decode failure). Investigate the collection mechanism itself (driver, permissions, sensor) rather than the hardware, which was never actually observed. |
