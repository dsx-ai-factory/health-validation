# Remediation Actions

When a check fails with `operational_state: drain`, its `recommended_action`
carries a `remediation_actions` array: an ordered sequence of steps a cluster
operator (or their automated remediation engine) should work
through to clear the fault. This document describes the contract for
consuming that sequence and catalogs every action this repo's own checks
recommend.

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
   (e.g. several checks recommending `reset_gpu` as their first step), and
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
constants in [`catalog/health/action.go`](../catalog/health/action.go) are
every value this repo's own checks are capable of recommending, but a catalog
extended with custom checks may recommend its own remediation steps. A
remediation engine should treat any value it doesn't recognize as an opaque
step (log it, surface it to a human) rather than an error.

### Generic reset/restart/escalate sequence

Most checks outside the Xid catalog — CPU/NUMA topology, GPU count/power,
NVLink connectivity, InfiniBand link health, and the CLI's own
architecture-discovery failure — have no NVIDIA-documented bespoke
remediation, so they all recommend the same default ladder
(`health.GenericDrain()`):

```json
"remediation_actions": ["reset_gpu", "restart_vm", "contact_support"]
```

Attempt a GPU-only reset first (the lightest-touch step; a remediation engine
that doesn't implement one simply no-ops through it), fall back to a
host-level restart, then escalate. Several Xid checks (below) use a shorter
two-step variant of this same pattern — `["reset_gpu", "contact_support"]`,
skipping the host-restart step — for faults where a host restart isn't
expected to add anything a GPU reset wouldn't already cover.

### Xid-catalog actions

These map to the "immediate action" resolution buckets in NVIDIA's
[Xid Errors documentation](https://docs.nvidia.com/deploy/xid-errors/latest/analyzing-xid-catalog.html),
and are recommended by the per-code `gpu_xid_<code>` checks and the
consolidated `gpu_xid_warning` check.

| Action | Description |
|---|---|
| `reset_gpu` | Reset the GPU: `nvidia-smi --gpu-reset` (no active processes may be using the GPU). |
| `restart_vm` | Restart the VM that owns the affected GPU. Recommended for Xid 140 (ECC unrecovered) and Xid 151 (key rotation error), and as the middle step of the [generic sequence](#generic-resetrestartescalate-sequence) above. |
| `restart_app` | Restart the application using the GPU; the GPU itself is not considered faulted. The default for the consolidated `gpu_xid_warning` check and most of the Xid codes it groups under "application-level" faults. |
| `update_swfw` | Update GPU driver/firmware and any related system software to the latest supported versions. Recommended for Xid 78 (vGPU start error). |
| `check_uvm` | If Unified Virtual Memory or vGPU is in use, reset the GPU; otherwise no hardware action is warranted. Recommended for Xid 159 (CHI non-data error). |
| `contact_support` | Escalate to your cluster's support path for further investigation (i.e. your CSP if CSP-managed, or your organization's support path if on-prem); the end of the line for self-service remediation. |
| `workflow_xid_45` | Follow the Xid 45 (preemptive GPU removal) workflow: restart the fabric manager if the node is solo; otherwise this Xid typically accompanies another Xid and that Xid's guidance applies instead. |
| `workflow_nvlink_err` | Follow the NVLink error workflow for the accompanying Xid (74): decode the hex register values in the error message to determine the specific NVLink remediation. |
| `workflow_nvlink5_err` | Follow the NVLink5 error workflow for the accompanying Xid (144–150): decode the hex register values in the error message to determine the specific NVLink remediation. |
| `xid_154` | Informational: Xid 154 (GPU recovery action changed) always accompanies another Xid and has no remediation of its own — follow that other Xid's guidance. |

### Collection-failure actions

These indicate the check's *rules never ran* — the sensor could not observe
the data needed to evaluate the check — rather than an observed hardware
fault. They are not hardware remediation steps.

| Action | Description |
|---|---|
| `retry_collection` | The measurement could not be collected in time (a sensor timeout). Retry collection — re-run `health-validation` — before assuming a hardware fault; investigate if it keeps timing out. |
| `check_collection` | The measurement could not be collected for a non-timeout reason (a driver, permission, or decode failure). Investigate the collection mechanism itself (driver, permissions, sensor) rather than the hardware, which was never actually observed. |
