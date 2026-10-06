package xid

import (
	"fmt"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
)

// Immediate-action buckets NVIDIA's Xid 144-150 decode table assigns.
// IGNORE and RESET_GPU are actions; the rest describe what happened when
// a row declined to name one.
const (
	ResolutionIgnore   = "IGNORE"
	ResolutionResetGPU = "RESET_GPU"

	// ResolutionResetGPUUnconfirmed is a reset NVIDIA's table does not
	// confirm: either the Fatal flag implies it because no row covers a
	// (subcode, ErrorStatus/IntrInfo) combination, or a row escalates to
	// it on a register bit that cannot be located without the driver
	// era. Without this the verdict could not distinguish "the table
	// says reset this GPU" from a conservative guess. Both still
	// escalate, but only one of them is documented guidance.
	ResolutionResetGPUUnconfirmed = "RESET_GPU_UNCONFIRMED"

	// ResolutionUndecoded marks an NVLink5 event whose log line carried
	// no fields to decode, such as one in a format the sensor does not
	// parse. The fault still happened, so it is counted rather than
	// dropped.
	ResolutionUndecoded = "UNDECODED"

	// ResolutionXid154Eval is the table's deferral, not an action: the
	// row says the action is whatever Xid 154 reports for the same GPU.
	// It never survives [NVLink5ResolutionCounts] — every occurrence is
	// replaced by the action the 154 named, or by one of the two
	// buckets below when it could not be.
	ResolutionXid154Eval = "XID_154_EVAL"

	// ResolutionXid154Missing marks a deferral with no Xid 154 to defer
	// to. The driver logs a 154 only when its recovery action changes, so
	// this normally means it requested none. It stays a bucket of its own
	// rather than folding into a restart: a 154 that was logged but not
	// read lands here too, and should remain visible.
	ResolutionXid154Missing = "XID_154_MISSING"

	// ResolutionRestartOS and ResolutionDrainP2P are recovery actions an
	// Xid 154 can name that the decode table itself never does.
	ResolutionRestartOS = "RESTART_OS"
	ResolutionDrainP2P  = "DRAIN_P2P"

	// ResolutionXid154Unrecognized marks a deferral whose Xid 154 named
	// a recovery action [recoveryActionBuckets] does not map. Surfaced
	// rather than dropped, so a new or unmapped code is visible instead
	// of silently reading as "no action".
	ResolutionXid154Unrecognized = "XID_154_UNRECOGNIZED"
)

// recoveryActionBuckets maps the recovery-action codes an Xid 154
// reports onto the bucket vocabulary. The codes are the driver's
// NV2080_CTRL_GPU_RECOVERY_ACTION values, which NVML's
// nvmlDeviceGpuRecoveryAction_t numbers identically.
//
// Recover IMEX Domain (0x5) and Bus Reset (0x6) are left unmapped: no
// remediation step here matches either, and an unmapped code still
// reports as unrecognized rather than as no action.
//
// Keyed numerically rather than by the string NVRM printed, so "0x1",
// "0x01" and "0X1" are the same action.
var recoveryActionBuckets = map[uint32]string{
	0x0: ResolutionIgnore,    // None
	0x1: ResolutionResetGPU,  // GPU reset (PF FLR)
	0x2: ResolutionRestartOS, // OS Reboot
	0x3: ResolutionDrainP2P,  // Drain P2P
	0x4: ResolutionResetGPU,  // Drain and Reset
	0x7: ResolutionRestartOS, // System Reboot
}

// canonicalRecoveryActionCode renders a recovery-action code one way, so
// the same action keys the same signal entry however NVRM spelled it. An
// unparseable value passes through rather than being dropped, keeping a
// malformed line visible instead of silently absent.
func canonicalRecoveryActionCode(raw string) string {
	v, ok := parseHexUint32(raw)
	if !ok {
		return raw
	}
	return fmt.Sprintf("0x%x", v)
}

// resolveRecoveryAction maps a terminal Xid 154 action onto the bucket
// vocabulary.
//
// Drain and Reset collapses onto RESET_GPU along with a plain GPU reset
// because the bucket names the remediation step, not the scheduling
// consequence: a check firing Critical already carries OperationalDrain,
// so the draining half is expressed there rather than duplicated here.
func resolveRecoveryAction(action RecoveryAction) string {
	code, ok := parseHexUint32(action.ToCode)
	if !ok {
		return ResolutionXid154Unrecognized
	}
	if bucket, ok := recoveryActionBuckets[code]; ok {
		return bucket
	}
	return ResolutionXid154Unrecognized
}

// resolveNVLink5Event returns the immediate-action bucket for one
// NVLink5 event, having already substituted the GPU's terminal recovery
// action for the table's XID_154_EVAL deferral.
func resolveNVLink5Event(code int64, era DriverEra, detail NVLink5Detail, action RecoveryAction, observed bool) string {
	decoded := DecodeNVLink5Resolution(code, era, detail)

	// A deferral only ever comes from a confirmed row: the Fatal-flag
	// fallback yields RESET_GPU or IGNORE and never XID_154_EVAL, so
	// anything resolved through the Xid 154 below is table-backed.
	if decoded.Resolution == ResolutionXid154Eval {
		if !observed {
			return ResolutionXid154Missing
		}
		return resolveRecoveryAction(action)
	}

	if !decoded.Matched && decoded.Resolution == ResolutionResetGPU {
		return ResolutionResetGPUUnconfirmed
	}
	return decoded.Resolution
}

// NVLink5ResolutionKey is the nvlink5_resolution_counts key for one Xid
// code resolving to one bucket, e.g. "145.IGNORE".
//
// Keyed per code, not per bucket alone, so a per-code check can ask what
// its own code resolved to. A bucket-only key cannot answer that, which
// would leave every Xid 144-150 check firing on the bare occurrence and
// make the table's IGNORE rows unreachable.
func NVLink5ResolutionKey(code int64, bucket string) string {
	return fmt.Sprintf("%d.%s", code, bucket)
}

// NVLink5ResolutionCounts returns how many NVLink5 events in the window
// resolved to each immediate-action bucket, keyed by
// [NVLink5ResolutionKey].
//
// This is the join the decode table cannot perform alone. A row
// resolving to XID_154_EVAL names no action by itself; the action lives
// in the Xid 154 the driver logged for that same GPU, which is why
// resolution needs both the table and [TerminalRecoveryActions] rather
// than the table alone.
//
// driverVersion is the host's driver version (see [health.Host]),
// classified into the era that decides which IntrInfo bit layout the
// table is read against; an unknown version limits matching to rows
// NVIDIA's table disambiguates by ErrorStatus alone rather than guessing
// a layout.
//
// Counts are per event, not per GPU: two faults on one GPU that both
// resolve to RESET_GPU count twice, because each is a separate
// occurrence to investigate. That differs deliberately from
// recovery_action_counts, which counts GPUs.
func NVLink5ResolutionCounts(events []XidEvent, driverVersion health.DriverVersion) map[string]int64 {
	return nvlink5ResolutionCounts(events, driverVersion, TerminalRecoveryActions(events))
}

// nvlink5ResolutionCounts is [NVLink5ResolutionCounts] with the terminal
// recovery actions supplied, so a caller that has already derived them
// does not derive them a second time.
func nvlink5ResolutionCounts(events []XidEvent, driverVersion health.DriverVersion, terminal map[string]RecoveryAction) map[string]int64 {
	era := DriverEraFrom(driverVersion)

	var counts map[string]int64
	for _, ev := range events {
		var bucket string
		switch {
		case ev.NVLink5 != nil:
			action, observed := terminal[ev.PCIAddress]
			bucket = resolveNVLink5Event(ev.XidCode, era, *ev.NVLink5, action, observed)
		case ev.XidCode >= 144 && ev.XidCode <= 150:
			bucket = ResolutionUndecoded
		default:
			continue
		}
		if bucket == "" {
			continue
		}
		if counts == nil {
			counts = make(map[string]int64)
		}
		counts[NVLink5ResolutionKey(ev.XidCode, bucket)]++
	}
	return counts
}
