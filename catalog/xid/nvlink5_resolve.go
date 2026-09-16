package xid

import "fmt"

// Immediate-action buckets NVIDIA's Xid 144-150 decode table assigns.
// IGNORE and RESET_GPU are actions; the rest describe what happened when
// a row declined to name one.
const (
	ResolutionIgnore   = "IGNORE"
	ResolutionResetGPU = "RESET_GPU"

	// ResolutionResetGPUUnconfirmed is a reset the Fatal flag implies
	// rather than one NVIDIA's table names. [DecodeNVLink5Resolution]
	// falls back to the flag when no row covers a
	// (subcode, ErrorStatus/IntrInfo) combination, so without this the
	// verdict could not distinguish "the table says reset this GPU" from
	// "the table says nothing and NVRM called it Fatal". Both still
	// escalate -- a Fatal NVLink5 fault is not something to sit on --
	// but only one of them is documented guidance.
	ResolutionResetGPUUnconfirmed = "RESET_GPU_UNCONFIRMED"

	// ResolutionXid154Eval is the table's deferral, not an action: the
	// row says the action is whatever Xid 154 reports for the same GPU.
	// It never survives [NVLink5ResolutionCounts] — every occurrence is
	// replaced by the action the 154 named, or by one of the two
	// buckets below when it could not be.
	ResolutionXid154Eval = "XID_154_EVAL"

	// ResolutionXid154Missing marks a deferral with no Xid 154 to defer
	// to. Expected below CUDA 12.7 / driver R565, where NVIDIA's catalog
	// records no Xid 154 linkage for this family at all, so the fault is
	// real but the driver never published an action for it.
	ResolutionXid154Missing = "XID_154_MISSING"

	// ResolutionXid154Unrecognized marks a deferral whose Xid 154 named
	// a recovery action this code does not map. Surfaced rather than
	// dropped: the action set is not documented as closed, so a new code
	// should be visible instead of silently reading as "no action".
	ResolutionXid154Unrecognized = "XID_154_UNRECOGNIZED"
)

// Recovery-action codes observed in Xid 154 lines. NVRM prints the code
// and the name it expands to; the code is matched because it is what the
// driver commits to, while the name is display text.
//
// Compared numerically rather than as the strings NVRM printed. The log
// format permits any hex rendering, so "0x1", "0x01" and "0X1" are the
// same action, and matching on the printed text read the padded forms as
// unrecognized.
const (
	recoveryActionNone             = 0x0 // None
	recoveryActionGPUResetRequired = 0x1 // GPU Reset Required
	recoveryActionDrainAndReset    = 0x4 // Drain and Reset
)

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

// resolveRecoveryAction maps a terminal Xid 154 action onto the decode
// table's bucket vocabulary.
//
// Drain and Reset collapses onto RESET_GPU along with GPU Reset Required
// because the bucket names the remediation step, not the scheduling
// consequence: a check firing Critical already carries OperationalDrain,
// so the draining half is expressed there rather than duplicated here.
func resolveRecoveryAction(action RecoveryAction) string {
	code, ok := parseHexUint32(action.ToCode)
	if !ok {
		return ResolutionXid154Unrecognized
	}
	switch code {
	case recoveryActionNone:
		return ResolutionIgnore
	case recoveryActionGPUResetRequired, recoveryActionDrainAndReset:
		return ResolutionResetGPU
	default:
		return ResolutionXid154Unrecognized
	}
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
// driverVersion is the host's raw driver version, classified into the
// era that decides which IntrInfo bit layout the table is read against;
// an unparseable or empty version limits matching to rows NVIDIA's table
// disambiguates by ErrorStatus alone rather than guessing a layout.
//
// Counts are per event, not per GPU: two faults on one GPU that both
// resolve to RESET_GPU count twice, because each is a separate
// occurrence to investigate. That differs deliberately from
// recovery_action_counts, which counts GPUs.
func NVLink5ResolutionCounts(events []XidEvent, driverVersion string) map[string]int64 {
	return nvlink5ResolutionCounts(events, driverVersion, TerminalRecoveryActions(events))
}

// nvlink5ResolutionCounts is [NVLink5ResolutionCounts] with the terminal
// recovery actions supplied, so a caller that has already derived them
// does not derive them a second time.
func nvlink5ResolutionCounts(events []XidEvent, driverVersion string, terminal map[string]RecoveryAction) map[string]int64 {
	era := DriverEraFromVersion(driverVersion)

	var counts map[string]int64
	for _, ev := range events {
		if ev.NVLink5 == nil {
			continue
		}
		action, observed := terminal[ev.PCIAddress]
		bucket := resolveNVLink5Event(ev.XidCode, era, *ev.NVLink5, action, observed)
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
