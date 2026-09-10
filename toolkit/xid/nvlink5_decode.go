package xid

import (
	"fmt"
	"strconv"
	"strings"
)

// nvlink5SubcodeMask isolates IntrInfo[25:20], the 6-bit fault-subtype
// field NVRM packs into the IntrInfo register logged for the NVLink5/
// NVSwitch fault family (Xid 144-150). This bit position is fixed
// regardless of driver version.
const nvlink5SubcodeMask = 0x3F00000

// nvlink5Subcode formats "<code>.<hex digit>" (e.g. "149.4") from code
// and the raw IntrInfo hex string logged for an NVLink5-family event.
// Returns ok=false when intrInfoHex isn't parseable (e.g. empty, for a
// non-NVLink5 event).
func nvlink5Subcode(code int64, intrInfoHex string) (string, bool) {
	v, ok := parseHexUint32(intrInfoHex)
	if !ok {
		return "", false
	}
	digit := (v & nvlink5SubcodeMask) >> 20
	return fmt.Sprintf("%d.%X", code, digit), true
}

// parseHexUint32 parses a "0x..."-prefixed hex string into a uint32.
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
