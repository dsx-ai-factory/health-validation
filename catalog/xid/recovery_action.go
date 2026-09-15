package xid

// TerminalRecoveryActions maps a GPU's PCI address to the last
// recovery-action transition reported for it in the window.
//
// An Xid 154 reports a transition, not a state, and a GPU escalates
// through several in sequence (None -> Drain and Reset -> GPU Reset
// Required), each line's From matching the previous line's To. The
// action actually in effect is therefore the To of the final
// transition for that GPU — which is what a decode-table row resolving
// to XID_154_EVAL needs in order to mean anything.
//
// Attribution is by PCI address rather than by proximity in the log.
// Captured logs show two GPUs reporting the same Xid 145 subtype in one
// window with only one of them escalating to a recovery action, so
// anything coarser — "a 154 appeared somewhere in this window" — would
// hand one GPU's recovery action to another and recommend a reset for a
// GPU the driver never asked to reset.
//
// The result describes the window, not an individual event. Events
// carry no timestamp, so log order is the only ordering available and a
// fault logged after the final 154 for its GPU is attributed the same
// terminal action as one logged before it. Within a single collection
// window that is the intended reading — the driver settles on one
// outcome per GPU — but it is why this is deliberately not presented as
// "the action for this event".
//
// Returns nil when no GPU reported one, so an absent action is
// indistinguishable from an empty map for callers that range over it.
func TerminalRecoveryActions(events []XidEvent) map[string]RecoveryAction {
	var terminal map[string]RecoveryAction
	for _, ev := range events {
		if ev.RecoveryAction == nil {
			continue
		}
		if terminal == nil {
			terminal = make(map[string]RecoveryAction)
		}
		terminal[ev.PCIAddress] = *ev.RecoveryAction
	}
	return terminal
}
