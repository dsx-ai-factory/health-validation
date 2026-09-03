# Remediation Actions

When a check fails with `operational_state: drain`, its `recommended_action`
carries a `remediation_actions` array: an ordered sequence of steps a cluster
operator (or their automated remediation engine, e.g. Maestro) should work
through to clear the fault. This document describes the contract for
consuming that sequence and catalogs every action NVIDIA's own catalog
recommends.

See [`catalog/health/action.go`](../catalog/health/action.go) for the Go type
(`health.RemediationAction`) and [`catalog/health/node-verdict.schema.json`](../catalog/health/node-verdict.schema.json)
for the JSON contract. For the field's place in the wider verdict shape, see
[`docs/INTEGRATION.md`](INTEGRATION.md).

## How to consume a remediation sequence

`remediation_actions` is ordered, most-preferred/least-disruptive step first.
`NodeVerdict` does not carry a single node-level remediation sequence (see
[`docs/ARCHITECTURE.md`](ARCHITECTURE.md#nodeverdict)) — each failing check
carries its own — so when more than one check is failing, don't work through
them independently in isolation. Instead, re-run the **whole suite** after
every remediation step, and each time pick the single next action to perform
by weighing the current set of failing checks against each other:

1. Run `health-validation` (the full suite).
2. If everything passes, **stop** — the node is healthy.
3. Otherwise, look at every currently-failing check with `operational_state:
   drain` and choose which one to act on next:
   - **A check whose next remediation step is `contact_support` takes
     precedence over everything else**, regardless of the other failing
     checks' severity. There's no further self-service step to try for it,
     so get a human involved before spending time on other checks' actions.
   - Otherwise, **the highest-severity failing check takes precedence**
     (`CRITICAL` before `WARNING`). Among checks tied at the same severity,
     use judgment — e.g. prefer the action that's also recommended by the
     most checks, or the least disruptive one, since clearing it may clear
     others too.
4. Take the next **untried** action from that check's `remediation_actions`
   (the first one, the first time you act on that check; the next one after
   that, if you already tried its earlier steps and it's still failing).
5. Perform that remediation step against the node.
6. Go back to step 1: re-run the **whole suite** again, not just the one
   check you just acted on. A single action can clear more than one check
   (e.g. several checks recommending `reboot` as their first step), and
   re-deriving the failing set and priority from scratch each time — rather
   than tracking a plan up front — keeps you from wasting a later, more
   disruptive step on a check that a different check's remediation already
   fixed.
7. If a check's `remediation_actions` is exhausted and it's still failing,
   treat it the same as reaching a `contact_support` step: escalate, don't
   keep retrying.

**Do not** perform every action in a sequence unconditionally, and do not
skip the whole-suite re-run between steps — later actions are more disruptive
(e.g. restarting a VM/bare-metal host) than earlier ones (e.g. resetting a
GPU), and are only warranted once the cheaper attempt has been confirmed not
to work.

## Action catalog

`RemediationAction` is an **open string type, not a closed enum** — the
constants below are the values NVIDIA's own catalog checks currently use, but
a catalog extended with custom checks may recommend its own remediation
steps. A remediation engine should treat any value it doesn't recognize as an
opaque step (log it, surface it to a human) rather than an error.

### Xid-catalog actions

These map directly to the "immediate action" resolution buckets in NVIDIA's
[Xid Errors documentation](https://docs.nvidia.com/deploy/xid-errors/latest/analyzing-xid-catalog.html),
and are recommended by the per-code `gpu_xid_<code>` checks and the
consolidated `gpu_xid_warning` check.

| Action | Description |
|---|---|
| `reset_gpu` | Reset the GPU. See NVIDIA's GPU debug guidelines for reset capabilities and limitations; a bare-metal restart is not expected to be necessary. |
| `restart_bm` | Restart the bare-metal host the GPU is attached to. |
| `restart_vm` | Restart the VM that owns the affected GPU; a GPU reset or bare-metal restart is not expected to be necessary. |
| `restart_app` | Restart the application using the GPU. The GPU itself is not considered faulted; a GPU reset or bare-metal restart is not expected to be necessary. |
| `update_swfw` | Update GPU driver/firmware and any related system software to the latest supported versions. |
| `check_mechanicals` | Physically inspect the device: confirm it is properly seated and every connection to it (power, PCIe, etc.) is secure. |
| `check_uvm` | If Unified Virtual Memory or vGPU is in use, reset the GPU; otherwise no hardware action is warranted for this specific fault. |
| `contact_support` | Escalate to NVIDIA support (or your organization's support path) for further investigation; this is the end of the line for self-service remediation. |
| `workflow_xid_45` | Follow the Xid 45 (preemptive GPU removal) workflow: restart the fabric manager if the node is solo; otherwise this Xid typically accompanies another Xid and that Xid's guidance applies instead. |
| `workflow_xid_48` | Follow the Xid 48 (double-bit ECC) data-center recovery workflow: reset the GPU if it's the only Xid observed, or drain and reset the node if it accompanies Xid 63 or 64. |
| `workflow_nvlink_err` | Follow the NVLink error workflow for the accompanying Xid (74): decode the hex register values in the error message to determine the specific NVLink remediation. |
| `workflow_nvlink5_err` | Follow the NVLink5 error workflow for the accompanying Xid (144–150): decode the hex register values in the error message to determine the specific NVLink remediation. |
| `xid_154` | Informational: Xid 154 (GPU recovery action changed) always accompanies another Xid and has no remediation of its own — follow that other Xid's guidance. |

### Operational actions

These are not from the Xid catalog; they're this project's own actions,
recommended by non-Xid checks (CPU/NUMA topology, GPU power, NVLink
connectivity, InfiniBand link health) for conditions the Xid catalog doesn't
cover.

| Action | Description |
|---|---|
| `reboot` | Reboot the node. Used for conditions that a driver/topology re-enumeration on boot is expected to resolve (e.g. a CPU/NUMA or GPU-count mismatch, or a GPU stuck outside its expected power state). |
| `retest_hardware` | Re-run hardware diagnostics/self-test on the affected component before deeper remediation, to confirm the condition is real and localize it (used by several NVLink and GPU sensor checks). |
| `check_network` | Inspect the network path implicated by the check: cabling, transceivers, and switch port health (used by the InfiniBand link-flap check). |

### Collection-failure actions

These indicate the check's *rules never ran* — the sensor could not observe
the data needed to evaluate the check — rather than an observed hardware
fault. They are not hardware remediation steps.

| Action | Description |
|---|---|
| `retry_collection` | The measurement could not be collected in time (a sensor timeout). Retry collection — re-run `health-validation` — before assuming a hardware fault; investigate if it keeps timing out. |
| `check_collection` | The measurement could not be collected for a non-timeout reason (a driver, permission, or decode failure). Investigate the collection mechanism itself (driver, permissions, sensor) rather than the hardware, which was never actually observed. |
