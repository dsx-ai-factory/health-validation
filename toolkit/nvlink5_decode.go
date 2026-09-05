package toolkit

import (
	"strconv"
	"strings"

	"github.com/dsx-ai-factory/health-validation/catalog/xid"
)

// nvlink5DecodeRow is one entry from NVIDIA's Xid 144-150 decode table:
// the resolution NVIDIA documents for a given (Xid code, sub-component,
// ErrorStatus) combination, plus the IntrInfo bit pattern identifying
// it. v1Pattern/v2Pattern are 32-character strings, one character per
// bit (MSB first): '0'/'1' means that bit must match exactly, '-' is
// "don't care." Pre-R575 (v1) and R575+ (v2) drivers pack the fault
// subcode into different IntrInfo bit positions, hence two patterns per
// row. errorStatus is "" when the row applies regardless of
// ErrorStatus (disambiguated by pattern alone instead), or a
// "/"-separated list of hex values that all mean the same thing.
type nvlink5DecodeRow struct {
	code          int64
	subComponent  string
	v1Pattern     string
	v2Pattern     string
	errorStatus   string
	resolution    string
	investigatory string
}

// nvlink5DecodeTable is generated from NVIDIA's internal Xid 144-150
// decode spreadsheet ("Xid 144-150 Decode" sheet), scripted directly
// out of the source file to avoid hand-transcription of the bit
// patterns. Regenerate from the source spreadsheet rather than
// hand-editing if NVIDIA revises the table.
var nvlink5DecodeTable = []nvlink5DecodeRow{
	{code: 144, subComponent: "SAW_MVB", v1Pattern: "------000000----------0000100001", v2Pattern: "------000000-------------0000001", errorStatus: "0x00000001", resolution: "IGNORE", investigatory: "CONTACT_SUPPORT"},
	{code: 144, subComponent: "SAW_MVB", v1Pattern: "------000000----------0000100001", v2Pattern: "------000000-------------0000001", errorStatus: "0x00000002", resolution: "RESET_GPU", investigatory: "CONTACT_SUPPORT"},
	{code: 144, subComponent: "SAW_MVB", v1Pattern: "------000000----------0000100001", v2Pattern: "------000000-------------0000001", errorStatus: "0x00000004", resolution: "IGNORE", investigatory: "IGNORE"},
	{code: 144, subComponent: "SAW_MVB", v1Pattern: "------000000----------0000100001", v2Pattern: "------000000-------------0000001", errorStatus: "0x00000008", resolution: "IGNORE", investigatory: "CONTACT_SUPPORT"},
	{code: 144, subComponent: "SAW_MVB", v1Pattern: "------000000----------0000100001", v2Pattern: "------000000-------------0000001", errorStatus: "0x00000010", resolution: "RESET_GPU", investigatory: "CONTACT_SUPPORT"},
	{code: 144, subComponent: "SAW_MVB", v1Pattern: "------000000----------0000100001", v2Pattern: "------000000-------------0000001", errorStatus: "0x00000020", resolution: "IGNORE", investigatory: "IGNORE"},
	{code: 145, subComponent: "RLW_CTRL", v1Pattern: "------000000----------0001100010", v2Pattern: "------000000-------------0000011", errorStatus: "0x80000000", resolution: "IGNORE", investigatory: "CONTACT_SUPPORT"},
	{code: 145, subComponent: "RLW_REMAP", v1Pattern: "------000000----------0010000010", v2Pattern: "------000000-------------0000100", errorStatus: "0x00000001", resolution: "XID_154_EVAL", investigatory: "CONTACT_SUPPORT"},
	{code: 145, subComponent: "RLW_REMAP", v1Pattern: "------000000----------0010000010", v2Pattern: "------000000-------------0000100", errorStatus: "0x00000002", resolution: "XID_154_EVAL", investigatory: "CONTACT_SUPPORT"},
	{code: 145, subComponent: "RLW_REMAP", v1Pattern: "------000000----------0010000010", v2Pattern: "------000000-------------0000100", errorStatus: "0x00000004", resolution: "XID_154_EVAL", investigatory: "CHECK_NVLINK_FAILURE_FLOW"},
	{code: 145, subComponent: "RLW_REMAP", v1Pattern: "------000000----------0010000010", v2Pattern: "------000000-------------0000100", errorStatus: "0x00000008", resolution: "XID_154_EVAL", investigatory: "CHECK_NVLINK_FAILURE_FLOW"},
	{code: 145, subComponent: "RLW_REMAP", v1Pattern: "------000000----------0010000010", v2Pattern: "------000000-------------0000100", errorStatus: "0x00000010", resolution: "XID_154_EVAL", investigatory: "CHECK_NVLINK_FAILURE_FLOW"},
	{code: 145, subComponent: "RLW_REMAP", v1Pattern: "------000000----------0010000010", v2Pattern: "------000000-------------0000100", errorStatus: "0x00000020", resolution: "XID_154_EVAL", investigatory: "CHECK_NVLINK_FAILURE_FLOW"},
	{code: 145, subComponent: "RLW_REMAP", v1Pattern: "------000000----------0010000010", v2Pattern: "------000000-------------0000100", errorStatus: "0x00000040", resolution: "RESET_GPU", investigatory: "CONTACT_SUPPORT"},
	{code: 145, subComponent: "RLW_REMAP", v1Pattern: "------000000----------0010000010", v2Pattern: "------000000-------------0000100", errorStatus: "0x00000080", resolution: "RESET_GPU", investigatory: "CONTACT_SUPPORT"},
	{code: 145, subComponent: "RLW_REMAP", v1Pattern: "------000000----------0010000010", v2Pattern: "------000000-------------0000100", errorStatus: "0x00000100", resolution: "IGNORE", investigatory: "IGNORE"},
	{code: 145, subComponent: "RLW_REMAP", v1Pattern: "------000000----------0010000010", v2Pattern: "------000000-------------0000100", errorStatus: "0x00000200", resolution: "IGNORE", investigatory: "IGNORE"},
	{code: 145, subComponent: "RLW_REMAP", v1Pattern: "------000000----------0010000010", v2Pattern: "------000000-------------0000100", errorStatus: "0x80000000", resolution: "IGNORE", investigatory: "CONTACT_SUPPORT"},
	{code: 145, subComponent: "RLW_RSPCOL", v1Pattern: "------000000----------0010100010", v2Pattern: "------000000-------------0000101", errorStatus: "0x00000001", resolution: "IGNORE", investigatory: "IGNORE"},
	{code: 145, subComponent: "RLW_RSPCOL", v1Pattern: "------000000----------0010100010", v2Pattern: "------000000-------------0000101", errorStatus: "0x00000002", resolution: "RESET_GPU", investigatory: "CONTACT_SUPPORT"},
	{code: 145, subComponent: "RLW_RSPCOL", v1Pattern: "------000000----------0010100010", v2Pattern: "------000000-------------0000101", errorStatus: "0x80000000", resolution: "IGNORE", investigatory: "CONTACT_SUPPORT"},
	{code: 145, subComponent: "RLW_RXPIPE", v1Pattern: "------000000---------00011000010", v2Pattern: "------000000------------00000110", errorStatus: "0x00000001", resolution: "IGNORE", investigatory: "CONTACT_SUPPORT"},
	{code: 145, subComponent: "RLW_RXPIPE", v1Pattern: "------000000---------00011000010", v2Pattern: "------000000------------00000110", errorStatus: "0x00000002", resolution: "IGNORE", investigatory: "CONTACT_SUPPORT"},
	{code: 145, subComponent: "RLW_RXPIPE", v1Pattern: "------000000---------00011000010", v2Pattern: "------000000------------00000110", errorStatus: "0x00000004", resolution: "IGNORE", investigatory: "CONTACT_SUPPORT"},
	{code: 145, subComponent: "RLW_RXPIPE", v1Pattern: "------000000----------0011000010", v2Pattern: "------000000-------------0000110", errorStatus: "0x00000008", resolution: "IGNORE", investigatory: "CONTACT_SUPPORT"},
	{code: 145, subComponent: "RLW_RXPIPE", v1Pattern: "------000000----------0011000010", v2Pattern: "------000000-------------0000110", errorStatus: "0x80000000", resolution: "IGNORE", investigatory: "CONTACT_SUPPORT"},
	{code: 145, subComponent: "RLW_SRC_TRACK", v1Pattern: "------000000----------0011100010", v2Pattern: "------000000-------------0000111", errorStatus: "0x00000001", resolution: "RESET_GPU", investigatory: "CONTACT_SUPPORT"},
	{code: 145, subComponent: "RLW_SRC_TRACK", v1Pattern: "------000000----------0011100010", v2Pattern: "------000000-------------0000111", errorStatus: "0x00000002", resolution: "IGNORE", investigatory: "IGNORE"},
	{code: 145, subComponent: "RLW_SRC_TRACK", v1Pattern: "------000000----------0011100010", v2Pattern: "------000000-------------0000111", errorStatus: "0x00000004", resolution: "XID_154_EVAL", investigatory: "IGNORE"},
	{code: 145, subComponent: "RLW_SRC_TRACK", v1Pattern: "------000000----------0011100010", v2Pattern: "------000000-------------0000111", errorStatus: "0x00000008", resolution: "XID_154_EVAL", investigatory: "IGNORE"},
	{code: 145, subComponent: "RLW_SRC_TRACK", v1Pattern: "------000000----------0011100010", v2Pattern: "------000000-------------0000111", errorStatus: "0x00000010", resolution: "RESET_GPU", investigatory: "CONTACT_SUPPORT"},
	{code: 145, subComponent: "RLW_SRC_TRACK", v1Pattern: "------000000----------0011100010", v2Pattern: "------000000-------------0000111", errorStatus: "0x00000020", resolution: "RESET_GPU", investigatory: "CONTACT_SUPPORT"},
	{code: 145, subComponent: "RLW_SRC_TRACK", v1Pattern: "------000000----------0011100010", v2Pattern: "------000000-------------0000111", errorStatus: "0x80000000", resolution: "IGNORE", investigatory: "CONTACT_SUPPORT"},
	{code: 145, subComponent: "RLW_TAGSTATE", v1Pattern: "------000000----------0100000010", v2Pattern: "------000000-------------0001000", errorStatus: "0x00000001", resolution: "IGNORE", investigatory: "IGNORE"},
	{code: 145, subComponent: "RLW_TAGSTATE", v1Pattern: "------000000----------0100000010", v2Pattern: "------000000-------------0001000", errorStatus: "0x00000002", resolution: "RESET_GPU", investigatory: "CONTACT_SUPPORT"},
	{code: 145, subComponent: "RLW_TAGSTATE", v1Pattern: "------000000----------0100000010", v2Pattern: "------000000-------------0001000", errorStatus: "0x00010000", resolution: "IGNORE", investigatory: "IGNORE"},
	{code: 145, subComponent: "RLW_TAGSTATE", v1Pattern: "------000000----------0100000010", v2Pattern: "------000000-------------0001000", errorStatus: "0x00020000", resolution: "RESET_GPU", investigatory: "CONTACT_SUPPORT"},
	{code: 145, subComponent: "RLW_TAGSTATE", v1Pattern: "------000000----------0100000010", v2Pattern: "------000000-------------0001000", errorStatus: "0x00100000", resolution: "RESET_GPU", investigatory: "CONTACT_SUPPORT"},
	{code: 145, subComponent: "RLW_TAGSTATE", v1Pattern: "------000000----------0100000010", v2Pattern: "------000000-------------0001000", errorStatus: "0x80000000", resolution: "IGNORE", investigatory: "CONTACT_SUPPORT"},
	{code: 146, subComponent: "TLW_CTRL", v1Pattern: "------000000----------0100100011", v2Pattern: "------000000-------------0001001", errorStatus: "0x00000001", resolution: "IGNORE", investigatory: "IGNORE"},
	{code: 146, subComponent: "TLW_CTRL", v1Pattern: "------000000----------0100100011", v2Pattern: "------000000-------------0001001", errorStatus: "0x00000002", resolution: "IGNORE", investigatory: "CONTACT_SUPPORT"},
	{code: 146, subComponent: "TLW_CTRL", v1Pattern: "------000000----------0100100011", v2Pattern: "------000000-------------0001001", errorStatus: "0x00000004", resolution: "RESET_GPU", investigatory: "CONTACT_SUPPORT"},
	{code: 146, subComponent: "TLW_CTRL", v1Pattern: "------000000----------0100100011", v2Pattern: "------000000-------------0001001", errorStatus: "0x80000000", resolution: "IGNORE", investigatory: "CONTACT_SUPPORT"},
	{code: 146, subComponent: "TLW_RX/TLW_RX_PIPE0", v1Pattern: "------000000----------0101000011", v2Pattern: "------000000-------------0001010", errorStatus: "0x00000001", resolution: "IGNORE", investigatory: "IGNORE"},
	{code: 146, subComponent: "TLW_RX/TLW_RX_PIPE0", v1Pattern: "------000000----------0101000011", v2Pattern: "------000000-------------0001010", errorStatus: "0x00000002", resolution: "IGNORE", investigatory: "CONTACT_SUPPORT"},
	{code: 146, subComponent: "TLW_RX/TLW_RX_PIPE0", v1Pattern: "------000000----------0101000011", v2Pattern: "------000000-------------0001010", errorStatus: "0x00000004", resolution: "RESET_GPU", investigatory: "CONTACT_SUPPORT"},
	{code: 146, subComponent: "TLW_RX/TLW_RX_PIPE0", v1Pattern: "------000000----------0101000011", v2Pattern: "------000000-------------0001010", errorStatus: "0x80000000", resolution: "IGNORE", investigatory: "CONTACT_SUPPORT"},
	{code: 146, subComponent: "TLW_RX/TLW_RX_PIPE1", v1Pattern: "------000000----------0101000011", v2Pattern: "------000000-------------0001011", errorStatus: "0x00000001", resolution: "IGNORE", investigatory: "IGNORE"},
	{code: 146, subComponent: "TLW_RX/TLW_RX_PIPE1", v1Pattern: "------000000----------0101000011", v2Pattern: "------000000-------------0001011", errorStatus: "0x00000002", resolution: "IGNORE", investigatory: "CONTACT_SUPPORT"},
	{code: 146, subComponent: "TLW_RX/TLW_RX_PIPE1", v1Pattern: "------000000----------0101000011", v2Pattern: "------000000-------------0001011", errorStatus: "0x00000004", resolution: "RESET_GPU", investigatory: "CONTACT_SUPPORT"},
	{code: 146, subComponent: "TLW_RX/TLW_RX_PIPE1", v1Pattern: "------000000----------0101000011", v2Pattern: "------000000-------------0001011", errorStatus: "0x80000000", resolution: "IGNORE", investigatory: "CONTACT_SUPPORT"},
	{code: 146, subComponent: "TLW_TX/TLW_TX_PIPE0", v1Pattern: "------000000----------0101100011", v2Pattern: "------000000-------------0001100", errorStatus: "0x00000001", resolution: "IGNORE", investigatory: "IGNORE"},
	{code: 146, subComponent: "TLW_TX/TLW_TX_PIPE0", v1Pattern: "------000000----------0101100011", v2Pattern: "------000000-------------0001100", errorStatus: "0x00000002", resolution: "IGNORE", investigatory: "CONTACT_SUPPORT"},
	{code: 146, subComponent: "TLW_TX/TLW_TX_PIPE0", v1Pattern: "------000000----------0101100011", v2Pattern: "------000000-------------0001100", errorStatus: "0x00000004", resolution: "RESET_GPU", investigatory: "CONTACT_SUPPORT"},
	{code: 146, subComponent: "TLW_TX/TLW_TX_PIPE0", v1Pattern: "------000000----------0101100011", v2Pattern: "------000000-------------0001100", errorStatus: "0x80000000", resolution: "IGNORE", investigatory: "CONTACT_SUPPORT"},
	{code: 146, subComponent: "TLW_TX/TLW_TX_PIPE1", v1Pattern: "------000000----------0101100011", v2Pattern: "------000000-------------0001101", errorStatus: "0x00000001", resolution: "IGNORE", investigatory: "IGNORE"},
	{code: 146, subComponent: "TLW_TX/TLW_TX_PIPE1", v1Pattern: "------000000----------0101100011", v2Pattern: "------000000-------------0001101", errorStatus: "0x00000002", resolution: "IGNORE", investigatory: "CONTACT_SUPPORT"},
	{code: 146, subComponent: "TLW_TX/TLW_TX_PIPE1", v1Pattern: "------000000----------0101100011", v2Pattern: "------000000-------------0001101", errorStatus: "0x00000004", resolution: "RESET_GPU", investigatory: "CONTACT_SUPPORT"},
	{code: 146, subComponent: "TLW_TX/TLW_TX_PIPE1", v1Pattern: "------000000----------0101100011", v2Pattern: "------000000-------------0001101", errorStatus: "0x80000000", resolution: "IGNORE", investigatory: "CONTACT_SUPPORT"},
	{code: 147, subComponent: "TREX", v1Pattern: "------000000----------0110000100", v2Pattern: "------000000-------------0001110", errorStatus: "0x00000001", resolution: "IGNORE", investigatory: "CONTACT_SUPPORT"},
	{code: 147, subComponent: "TREX", v1Pattern: "------000000----------0110000100", v2Pattern: "------000000-------------0001110", errorStatus: "0x80000000", resolution: "IGNORE", investigatory: "CONTACT_SUPPORT"},
	{code: 148, subComponent: "NVLPW_CTRL/NVLPW", v1Pattern: "------000000----------0000000101", v2Pattern: "------000000-------------0001111", errorStatus: "0x80000000", resolution: "IGNORE", investigatory: "CONTACT_SUPPORT"},
	{code: 149, subComponent: "NETIR/NETIR_INT", v1Pattern: "------000000----------0000000110", v2Pattern: "------000000-------------0011000", errorStatus: "", resolution: "RESET_GPU", investigatory: "SYMPATHETIC_REPORT_SOLO"},
	{code: 149, subComponent: "NETIR_LINK_EVT/NETIR_LINK_DOWN", v1Pattern: "------000000----------0111000110", v2Pattern: "------000000-------------0010001", errorStatus: "", resolution: "RESET_GPU", investigatory: "SYMPATHETIC_REPORT_SOLO"},
	{code: 149, subComponent: "NETIR_LINK_EVT/NETIR_LINK_DOWN", v1Pattern: "------000001----------0111000110", v2Pattern: "------000001-------------0010001", errorStatus: "", resolution: "RESET_GPU", investigatory: "REPORT_ISSUE (if seen >1 per day)"},
	{code: 149, subComponent: "NETIR_LINK_EVT/NETIR_LINK_DOWN", v1Pattern: "------000010----------0111000110", v2Pattern: "------000010-------------0010001", errorStatus: "", resolution: "RESET_GPU", investigatory: "INVESTIGATE_LINK_SI"},
	{code: 149, subComponent: "NETIR_LINK_EVT/NETIR_LINK_DOWN", v1Pattern: "------000100----------0111000110", v2Pattern: "------000100-------------0010001", errorStatus: "", resolution: "RESET_GPU", investigatory: "INVESTIGATE_LINK_SI"},
	{code: 149, subComponent: "NETIR_LINK_EVT/NETIR_LINK_DOWN", v1Pattern: "------001010----------0111000110", v2Pattern: "------001010-------------0010001", errorStatus: "", resolution: "RESET_GPU", investigatory: "INVESTIGATE_LINK_SI"},
	{code: 149, subComponent: "NETIR_LINK_EVT/NETIR_LINK_DOWN", v1Pattern: "------001111----------0111000110", v2Pattern: "------001111-------------0010001", errorStatus: "", resolution: "RESET_GPU", investigatory: "INVESTIGATE_SW_USER"},
	{code: 149, subComponent: "NETIR_LINK_EVT/NETIR_LINK_DOWN", v1Pattern: "------010000----------0111000110", v2Pattern: "------010000-------------0010001", errorStatus: "", resolution: "RESET_GPU", investigatory: "INVESTIGATE_SW_USER_LINK_SI"},
	{code: 149, subComponent: "NETIR_LINK_EVT/NETIR_LINK_DOWN", v1Pattern: "------010001----------0111000110", v2Pattern: "------010001-------------0010001", errorStatus: "", resolution: "RESET_GPU", investigatory: "INVESTIGATE_SW_USER"},
	{code: 149, subComponent: "NETIR_LINK_EVT/NETIR_LINK_DOWN", v1Pattern: "------010010----------0111000110", v2Pattern: "------010010-------------0010001", errorStatus: "", resolution: "RESET_GPU", investigatory: "INVESTIGATE_SW_USER_LINK_SI"},
	{code: 149, subComponent: "NETIR_LINK_EVT/NETIR_LINK_DOWN", v1Pattern: "------010101----------0111000110", v2Pattern: "------010101-------------0010001", errorStatus: "", resolution: "RESET_GPU", investigatory: "INVESTIGATE_PEER_DEVICE"},
	{code: 149, subComponent: "NETIR_LINK_EVT/NETIR_LINK_DOWN", v1Pattern: "------010110----------0111000110", v2Pattern: "------010110-------------0010001", errorStatus: "", resolution: "RESET_GPU", investigatory: "INVESTIGATE_SW_USER"},
	{code: 149, subComponent: "NETIR_LINK_EVT/NETIR_LINK_DOWN", v1Pattern: "------100000----------0111000110", v2Pattern: "------100000-------------0010001", errorStatus: "", resolution: "RESET_GPU", investigatory: "INVESTIGATE_PEER_DEVICE"},
	{code: 149, subComponent: "NETIR_LINK_EVT/NETIR_LINK_DOWN", v1Pattern: "------100001----------0111000110", v2Pattern: "------100001-------------0010001", errorStatus: "", resolution: "RESET_GPU", investigatory: "INVESTIGATE_PEER_DEVICE"},
	{code: 149, subComponent: "NETIR_LINK_EVT/NETIR_LINK_DOWN", v1Pattern: "------100010----------0111000110", v2Pattern: "------100010-------------0010001", errorStatus: "", resolution: "RESET_GPU", investigatory: "INVESTIGATE_PEER_DEVICE"},
	{code: 149, subComponent: "NETIR_LINK_EVT/NETIR_LINK_DOWN", v1Pattern: "------100011----------0111000110", v2Pattern: "------100011-------------0010001", errorStatus: "", resolution: "RESET_GPU", investigatory: "INVESTIGATE_PEER_DEVICE"},
	{code: 149, subComponent: "NETIR_LINK_EVT/NETIR_LINK_DOWN", v1Pattern: "------100100----------0111000110", v2Pattern: "------100100-------------0010001", errorStatus: "", resolution: "RESET_GPU", investigatory: "INVESTIGATE_PEER_DEVICE"},
	{code: 149, subComponent: "NETIR_LINK_EVT/NETIR_LINK_DOWN", v1Pattern: "------100101----------0111000110", v2Pattern: "------100101-------------0010001", errorStatus: "", resolution: "RESET_GPU", investigatory: "INVESTIGATE_PEER_DEVICE"},
	{code: 149, subComponent: "NETIR_LINK_EVT/NETIR_LINK_DOWN", v1Pattern: "------100110----------0111000110", v2Pattern: "------100110-------------0010001", errorStatus: "", resolution: "IGNORE", investigatory: "INVESTIGATE_SW/USER"},
	{code: 149, subComponent: "NETIR_LINK_EVT/NETIR_LINK_DOWN", v1Pattern: "------101000----------0111000110", v2Pattern: "------101000-------------0010001", errorStatus: "", resolution: "IGNORE", investigatory: "INVESTIGATE_HOST"},
	{code: 149, subComponent: "NETIR_LINK_EVT/NETIR_LINK_DOWN", v1Pattern: "------101010----------0111000110", v2Pattern: "------101010-------------0010001", errorStatus: "", resolution: "RESET_GPU", investigatory: "INVESTIGATE_LINK_SI_AND_CABLES"},
	{code: 149, subComponent: "NETIR_LINK_EVT/NETIR_LINK_DOWN", v1Pattern: "------101011----------0111000110", v2Pattern: "------101011-------------0010001", errorStatus: "", resolution: "RESET_GPU", investigatory: "INVESTIGATE_LINK_SI_AND_CABLES"},
	{code: 149, subComponent: "NETIR_BER_EVENT", v1Pattern: "------000000----------1000100110", v2Pattern: "------000000-------------0010011", errorStatus: "0x00000000", resolution: "IGNORE", investigatory: "INVESTIGATE_LINK_SI_AND_CABLES"},
	{code: 149, subComponent: "NETIR_BER_EVENT", v1Pattern: "------000000----------1000100110", v2Pattern: "------000000-------------0010011", errorStatus: "0x00000001", resolution: "IGNORE", investigatory: "INVESTIGATE_LINK_SI_AND_CABLES"},
	{code: 149, subComponent: "NETIR_BER_EVENT", v1Pattern: "------000000----------1000100110", v2Pattern: "------000000-------------0010011", errorStatus: "0x00000002", resolution: "IGNORE", investigatory: "INVESTIGATE_LINK_SI_AND_CABLES"},
	{code: 149, subComponent: "NETIR_BER_EVENT", v1Pattern: "------000000----------1000100110", v2Pattern: "------000000-------------0010011", errorStatus: "0x00000003", resolution: "IGNORE", investigatory: "INVESTIGATE_LINK_SI_AND_CABLES"},
	{code: 149, subComponent: "NETIR_MFDE_EVENT", v1Pattern: "------000000----------1001000110", v2Pattern: "------000000-------------0010100", errorStatus: "0x00000001", resolution: "RESET_GPU", investigatory: "CONTACT_SUPPORT"},
	{code: 149, subComponent: "NETIR_MFDE_EVENT", v1Pattern: "------000000----------1001000110", v2Pattern: "------000000-------------0010100", errorStatus: "0x00000003", resolution: "IGNORE", investigatory: "IGNORE"},
	{code: 149, subComponent: "NETIR_MFDE_EVENT", v1Pattern: "------000000----------1001000110", v2Pattern: "------000000-------------0010100", errorStatus: "0x00000004", resolution: "RESET_GPU", investigatory: "CONTACT_SUPPORT"},
	{code: 149, subComponent: "NETIR_MFDE_EVENT", v1Pattern: "------000000----------1001000110", v2Pattern: "------000000-------------0010100", errorStatus: "0x00000005", resolution: "RESET_GPU", investigatory: "CONTACT_SUPPORT"},
	{code: 149, subComponent: "NETIR_MFDE_EVENT", v1Pattern: "------000000----------1001000110", v2Pattern: "------000000-------------0010100", errorStatus: "0x00000007", resolution: "RESET_GPU", investigatory: "CONTACT_SUPPORT"},
	{code: 150, subComponent: "MSE Degraded", v1Pattern: "------000000----------0000000000", v2Pattern: "------000000-------------0000000", errorStatus: "0x00000000/0xFFFFFFFF", resolution: "RESET_GPU", investigatory: "CONTACT_SUPPORT"},
	{code: 150, subComponent: "MSE_WATCHDOG", v1Pattern: "------000000----------0000000000", v2Pattern: "------000000-------------0000000", errorStatus: "0x00000000", resolution: "RESET_GPU", investigatory: "CONTACT_SUPPORT"},
}

// nvlink5FallbackResolution derives a conservative resolution from the
// literal Fatal/Nonfatal text NVRM prints in the log line, used when
// [nvlink5DecodeTable] has no confirmed entry for an event (driver era
// unknown, IntrInfo/ErrorStatus unparseable, or a genuinely
// undocumented combination). Favors the safer direction on ambiguity:
// an unconfirmed Fatal event still recommends RESET_GPU + CONTACT_SUPPORT
// rather than defaulting toward IGNORE.
func nvlink5FallbackResolution(fatal bool) nvlink5Resolution {
	if fatal {
		return nvlink5Resolution{immediate: "RESET_GPU", investigatory: "CONTACT_SUPPORT"}
	}
	return nvlink5Resolution{immediate: "IGNORE", investigatory: "CONTACT_SUPPORT"}
}

// nvlink5Resolution is the pair of actions decodeNVLink5Resolution
// resolves to, from either an exact table match or the fallback.
type nvlink5Resolution struct {
	immediate     string
	investigatory string
}

// decodeNVLink5Resolution fills detail.Resolution, detail.Investigation,
// detail.DriverEra, and detail.ResolutionMatched.
//
// Matching proceeds in two stages against rows sharing detail's Xid
// code and sub-component: first by ErrorStatus, which uniquely
// disambiguates most sub-components. If more than one candidate row
// remains (NVIDIA's table differentiates a few sub-components, e.g.
// Xid 149's NETIR_LINK_EVT/NETIR_LINK_DOWN family, purely by IntrInfo
// bit pattern instead of ErrorStatus), pattern-matching against the
// driver-era-appropriate IntrInfo bit layout picks among them — and is
// skipped entirely when era is [DriverEraUnknown], since pre-/post-R575
// drivers place the subcode at different bit positions and guessing
// the wrong one risks matching an unrelated row. Falls back to
// [nvlink5FallbackResolution] whenever no single row is confirmed.
func decodeNVLink5Resolution(code int64, era DriverEra, detail *xid.NVLink5Detail) {
	detail.DriverEra = era.String()

	candidates := make([]nvlink5DecodeRow, 0, 4)
	for _, row := range nvlink5DecodeTable {
		if row.code == code && subComponentMatches(detail.SubComponent, row.subComponent) {
			candidates = append(candidates, row)
		}
	}

	if byStatus := filterByErrorStatus(candidates, detail.ErrorStatus); len(byStatus) == 1 {
		applyResolution(detail, byStatus[0], true)
		return
	} else if len(byStatus) > 0 {
		candidates = byStatus
	}

	if era != DriverEraUnknown {
		if intrInfo, ok := parseHexUint32(detail.IntrInfo); ok {
			for _, row := range candidates {
				pattern := row.v2Pattern
				if era == DriverEraV1 {
					pattern = row.v1Pattern
				}
				if matchesPattern(intrInfo, pattern) {
					applyResolution(detail, row, true)
					return
				}
			}
		}
	}

	res := nvlink5FallbackResolution(detail.Fatal)
	detail.Resolution = res.immediate
	detail.Investigation = res.investigatory
	detail.ResolutionMatched = false
}

func applyResolution(detail *xid.NVLink5Detail, row nvlink5DecodeRow, matched bool) {
	detail.Resolution = row.resolution
	detail.Investigation = row.investigatory
	detail.ResolutionMatched = matched
}

// filterByErrorStatus returns the rows among candidates whose
// errorStatus field (a "/"-separated alternative list, or "" to match
// any) matches actual.
func filterByErrorStatus(candidates []nvlink5DecodeRow, actual string) []nvlink5DecodeRow {
	var out []nvlink5DecodeRow
	for _, row := range candidates {
		if row.errorStatus == "" {
			continue // matches any, but doesn't uniquely disambiguate
		}
		for _, alt := range strings.Split(row.errorStatus, "/") {
			if strings.EqualFold(strings.TrimSpace(alt), actual) {
				out = append(out, row)
				break
			}
		}
	}
	return out
}

// subComponentMatches reports whether the literal sub-component name
// NVRM printed in the log line (log) matches one of the "/"-separated
// alternative names in a decode-table row (table), e.g. table
// "TLW_RX/TLW_RX_PIPE0" matches log "TLW_RX_PIPE0". Comparison
// normalizes spaces to underscores and is case-insensitive, since the
// source spreadsheet uses "MSE Degraded" where a log line would print
// an underscore-joined identifier.
func subComponentMatches(log, table string) bool {
	normalize := func(s string) string {
		return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(s), " ", "_"))
	}
	log = normalize(log)
	for _, alt := range strings.Split(table, "/") {
		if normalize(alt) == log {
			return true
		}
	}
	return false
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

// matchesPattern reports whether value's bits satisfy pattern: a
// 32-character string, one character per bit (MSB first, i.e.
// pattern[0] is bit 31), where '0'/'1' requires an exact match and '-'
// is "don't care." A pattern of any other length never matches.
func matchesPattern(value uint32, pattern string) bool {
	if len(pattern) != 32 {
		return false
	}
	for i := 0; i < 32; i++ {
		c := pattern[i]
		if c == '-' {
			continue
		}
		bit := (value >> uint(31-i)) & 1
		want := uint32(0)
		if c == '1' {
			want = 1
		} else if c != '0' {
			return false // malformed pattern character
		}
		if bit != want {
			return false
		}
	}
	return true
}
