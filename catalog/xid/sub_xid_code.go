package xid

import (
	"fmt"
	"strconv"
	"strings"
)

// nvlink5SubXidCodeMask isolates IntrInfo[25:20], the 6-bit fault-subtype
// field NVRM packs into the IntrInfo register it logs for the NVLink5/
// NVSwitch fault family (Xid 144-150). Both driver eras place it here.
// Not to be confused with the decode-table guide's "Subcode", a
// bit-encoding of SubComponent that repeats text NVRM already prints.
const nvlink5SubXidCodeMask = 0x3F00000

// SubXidCode identifies which fault subtype within its Xid code an
// NVLink5-family event is, formatted "<code>.<hex digit>" (e.g. "149.4",
// "149.A") from IntrInfo[25:20]. Returns ok=false for an event with no
// NVLink5 detail or an IntrInfo that does not parse.
//
// Derived here rather than by whatever collected the event, so every
// source of Xid events keys subtypes identically, and the digit sits
// beside the decode table that says which digits name one fault and
// which are shared (see [nvlink5SubXidCodeResolvesAlike]).
func (e XidEvent) SubXidCode() (string, bool) {
	if e.NVLink5 == nil {
		return "", false
	}
	v, ok := parseHexUint32(e.NVLink5.IntrInfo)
	if !ok {
		return "", false
	}
	return fmt.Sprintf("%d.%X", e.XidCode, (v&nvlink5SubXidCodeMask)>>20), true
}

// subXidCodeCounts tallies events by [XidEvent.SubXidCode].
func subXidCodeCounts(events []XidEvent) map[string]int64 {
	counts := make(map[string]int64)
	for _, ev := range events {
		if code, ok := ev.SubXidCode(); ok {
			counts[code]++
		}
	}
	return counts
}

// parseHexUint32 parses a "0x"-prefixed hex string into a uint32.
func parseHexUint32(s string) (uint32, bool) {
	s = strings.TrimPrefix(strings.TrimPrefix(s, "0x"), "0X")
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return 0, false
	}
	return uint32(v), true
}
