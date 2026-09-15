package xid

// This file carries NVIDIA's Xid 144-150 decode table as data only.
// Matching an individual event against it -- which needs the host's
// driver era to pick an IntrInfo bit layout -- arrives with the code
// that consumes it.

// nvlink5DecodeRow is one entry from NVIDIA's Xid 144-150 decode table:
// the resolution NVIDIA documents for a given (Xid code, subcode,
// ErrorStatus) combination, plus the IntrInfo bit pattern identifying
// it. v1Pattern/v2Pattern are 32-character strings, one character per
// bit (MSB first): '0'/'1' means that bit must match exactly, '-' is
// "don't care." Pre-R575 (v1) and R575+ (v2) drivers pack the fault
// subcode into different IntrInfo bit positions, hence two patterns per
// row. errorStatus is "" when the row applies regardless of
// ErrorStatus (disambiguated by pattern alone instead), or a
// "/"-separated list of hex values that all mean the same thing.
//
// A handful of rows carry a second, more specific IntrInfo pattern
// (action2V1Pattern/action2V2Pattern) representing a "promoteable"
// condition NVIDIA's source spreadsheet flags with a '*': if the
// driver-era-appropriate pattern also matches, the resolution escalates
// from `resolution` to `action2Resolution` (investigatory action is
// unchanged). Empty when the row has no such escalation.
type nvlink5DecodeRow struct {
	subcode           string
	v1Pattern         string
	v2Pattern         string
	errorStatus       string
	resolution        string
	investigatory     string
	action2V1Pattern  string
	action2V2Pattern  string
	action2Resolution string
	code              int64
}

// nvlink5DecodeTable is generated from NVIDIA's internal Xid 144-150
// decode spreadsheet ("Xid 144-150 Decode" sheet), scripted directly
// out of the source file to avoid hand-transcription of the bit
// patterns. Regenerate from the source spreadsheet rather than
// hand-editing if NVIDIA revises the table.
var nvlink5DecodeTable = []nvlink5DecodeRow{
	{code: 144, subcode: "SAW_MVB", v1Pattern: "------000000----------0000100001", v2Pattern: "------000000-------------0000001", errorStatus: "0x00000001", resolution: "IGNORE", investigatory: "CONTACT_SUPPORT"},
	{code: 144, subcode: "SAW_MVB", v1Pattern: "------000000----------0000100001", v2Pattern: "------000000-------------0000001", errorStatus: "0x00000002", resolution: "RESET_GPU", investigatory: "CONTACT_SUPPORT"},
	{code: 144, subcode: "SAW_MVB", v1Pattern: "------000000----------0000100001", v2Pattern: "------000000-------------0000001", errorStatus: "0x00000004", resolution: "IGNORE", investigatory: "IGNORE"},
	{code: 144, subcode: "SAW_MVB", v1Pattern: "------000000----------0000100001", v2Pattern: "------000000-------------0000001", errorStatus: "0x00000008", resolution: "IGNORE", investigatory: "CONTACT_SUPPORT"},
	{code: 144, subcode: "SAW_MVB", v1Pattern: "------000000----------0000100001", v2Pattern: "------000000-------------0000001", errorStatus: "0x00000010", resolution: "RESET_GPU", investigatory: "CONTACT_SUPPORT"},
	{code: 144, subcode: "SAW_MVB", v1Pattern: "------000000----------0000100001", v2Pattern: "------000000-------------0000001", errorStatus: "0x00000020", resolution: "IGNORE", investigatory: "IGNORE"},
	{code: 145, subcode: "RLW_CTRL", v1Pattern: "------000000----------0001100010", v2Pattern: "------000000-------------0000011", errorStatus: "0x80000000", resolution: "IGNORE", investigatory: "CONTACT_SUPPORT"},
	{code: 145, subcode: "RLW_REMAP", v1Pattern: "------000000----------0010000010", v2Pattern: "------000000-------------0000100", errorStatus: "0x00000001", resolution: "XID_154_EVAL", investigatory: "CONTACT_SUPPORT"},
	{code: 145, subcode: "RLW_REMAP", v1Pattern: "------000000----------0010000010", v2Pattern: "------000000-------------0000100", errorStatus: "0x00000002", resolution: "XID_154_EVAL", investigatory: "CONTACT_SUPPORT"},
	{code: 145, subcode: "RLW_REMAP", v1Pattern: "------000000----------0010000010", v2Pattern: "------000000-------------0000100", errorStatus: "0x00000004", resolution: "XID_154_EVAL", investigatory: "CHECK_NVLINK_FAILURE_FLOW"},
	{code: 145, subcode: "RLW_REMAP", v1Pattern: "------000000----------0010000010", v2Pattern: "------000000-------------0000100", errorStatus: "0x00000008", resolution: "XID_154_EVAL", investigatory: "CHECK_NVLINK_FAILURE_FLOW"},
	{code: 145, subcode: "RLW_REMAP", v1Pattern: "------000000----------0010000010", v2Pattern: "------000000-------------0000100", errorStatus: "0x00000010", resolution: "XID_154_EVAL", investigatory: "CHECK_NVLINK_FAILURE_FLOW"},
	{code: 145, subcode: "RLW_REMAP", v1Pattern: "------000000----------0010000010", v2Pattern: "------000000-------------0000100", errorStatus: "0x00000020", resolution: "XID_154_EVAL", investigatory: "CHECK_NVLINK_FAILURE_FLOW"},
	{code: 145, subcode: "RLW_REMAP", v1Pattern: "------000000----------0010000010", v2Pattern: "------000000-------------0000100", errorStatus: "0x00000040", resolution: "RESET_GPU", investigatory: "CONTACT_SUPPORT"},
	{code: 145, subcode: "RLW_REMAP", v1Pattern: "------000000----------0010000010", v2Pattern: "------000000-------------0000100", errorStatus: "0x00000080", resolution: "RESET_GPU", investigatory: "CONTACT_SUPPORT"},
	{code: 145, subcode: "RLW_REMAP", v1Pattern: "------000000----------0010000010", v2Pattern: "------000000-------------0000100", errorStatus: "0x00000100", resolution: "IGNORE", investigatory: "IGNORE"},
	{code: 145, subcode: "RLW_REMAP", v1Pattern: "------000000----------0010000010", v2Pattern: "------000000-------------0000100", errorStatus: "0x00000200", resolution: "IGNORE", investigatory: "IGNORE"},
	{code: 145, subcode: "RLW_REMAP", v1Pattern: "------000000----------0010000010", v2Pattern: "------000000-------------0000100", errorStatus: "0x80000000", resolution: "IGNORE", investigatory: "CONTACT_SUPPORT"},
	{code: 145, subcode: "RLW_RSPCOL", v1Pattern: "------000000----------0010100010", v2Pattern: "------000000-------------0000101", errorStatus: "0x00000001", resolution: "IGNORE", investigatory: "IGNORE"},
	{code: 145, subcode: "RLW_RSPCOL", v1Pattern: "------000000----------0010100010", v2Pattern: "------000000-------------0000101", errorStatus: "0x00000002", resolution: "RESET_GPU", investigatory: "CONTACT_SUPPORT"},
	{code: 145, subcode: "RLW_RSPCOL", v1Pattern: "------000000----------0010100010", v2Pattern: "------000000-------------0000101", errorStatus: "0x80000000", resolution: "IGNORE", investigatory: "CONTACT_SUPPORT"},
	{code: 145, subcode: "RLW_RXPIPE", v1Pattern: "------000000---------00011000010", v2Pattern: "------000000------------00000110", errorStatus: "0x00000001", resolution: "IGNORE", investigatory: "CONTACT_SUPPORT", action2V1Pattern: "------000000---------10011000010", action2V2Pattern: "------000000------------10000110", action2Resolution: "RESET_GPU"},
	{code: 145, subcode: "RLW_RXPIPE", v1Pattern: "------000000---------00011000010", v2Pattern: "------000000------------00000110", errorStatus: "0x00000002", resolution: "IGNORE", investigatory: "CONTACT_SUPPORT", action2V1Pattern: "------000000---------10011000010", action2V2Pattern: "------000000------------10000110", action2Resolution: "RESET_GPU"},
	{code: 145, subcode: "RLW_RXPIPE", v1Pattern: "------000000---------00011000010", v2Pattern: "------000000------------00000110", errorStatus: "0x00000004", resolution: "IGNORE", investigatory: "CONTACT_SUPPORT", action2V1Pattern: "------000000---------10011000010", action2V2Pattern: "------000000------------10000110", action2Resolution: "RESET_GPU"},
	{code: 145, subcode: "RLW_RXPIPE", v1Pattern: "------000000----------0011000010", v2Pattern: "------000000-------------0000110", errorStatus: "0x00000008", resolution: "IGNORE", investigatory: "CONTACT_SUPPORT"},
	{code: 145, subcode: "RLW_RXPIPE", v1Pattern: "------000000----------0011000010", v2Pattern: "------000000-------------0000110", errorStatus: "0x80000000", resolution: "IGNORE", investigatory: "CONTACT_SUPPORT"},
	{code: 145, subcode: "RLW_SRC_TRACK", v1Pattern: "------000000----------0011100010", v2Pattern: "------000000-------------0000111", errorStatus: "0x00000001", resolution: "RESET_GPU", investigatory: "CONTACT_SUPPORT"},
	{code: 145, subcode: "RLW_SRC_TRACK", v1Pattern: "------000000----------0011100010", v2Pattern: "------000000-------------0000111", errorStatus: "0x00000002", resolution: "IGNORE", investigatory: "IGNORE"},
	{code: 145, subcode: "RLW_SRC_TRACK", v1Pattern: "------000000----------0011100010", v2Pattern: "------000000-------------0000111", errorStatus: "0x00000004", resolution: "XID_154_EVAL", investigatory: "IGNORE"},
	{code: 145, subcode: "RLW_SRC_TRACK", v1Pattern: "------000000----------0011100010", v2Pattern: "------000000-------------0000111", errorStatus: "0x00000008", resolution: "XID_154_EVAL", investigatory: "IGNORE"},
	{code: 145, subcode: "RLW_SRC_TRACK", v1Pattern: "------000000----------0011100010", v2Pattern: "------000000-------------0000111", errorStatus: "0x00000010", resolution: "RESET_GPU", investigatory: "CONTACT_SUPPORT"},
	{code: 145, subcode: "RLW_SRC_TRACK", v1Pattern: "------000000----------0011100010", v2Pattern: "------000000-------------0000111", errorStatus: "0x00000020", resolution: "RESET_GPU", investigatory: "CONTACT_SUPPORT"},
	{code: 145, subcode: "RLW_SRC_TRACK", v1Pattern: "------000000----------0011100010", v2Pattern: "------000000-------------0000111", errorStatus: "0x80000000", resolution: "IGNORE", investigatory: "CONTACT_SUPPORT"},
	{code: 145, subcode: "RLW_TAGSTATE", v1Pattern: "------000000----------0100000010", v2Pattern: "------000000-------------0001000", errorStatus: "0x00000001", resolution: "IGNORE", investigatory: "IGNORE"},
	{code: 145, subcode: "RLW_TAGSTATE", v1Pattern: "------000000----------0100000010", v2Pattern: "------000000-------------0001000", errorStatus: "0x00000002", resolution: "RESET_GPU", investigatory: "CONTACT_SUPPORT"},
	{code: 145, subcode: "RLW_TAGSTATE", v1Pattern: "------000000----------0100000010", v2Pattern: "------000000-------------0001000", errorStatus: "0x00010000", resolution: "IGNORE", investigatory: "IGNORE"},
	{code: 145, subcode: "RLW_TAGSTATE", v1Pattern: "------000000----------0100000010", v2Pattern: "------000000-------------0001000", errorStatus: "0x00020000", resolution: "RESET_GPU", investigatory: "CONTACT_SUPPORT"},
	{code: 145, subcode: "RLW_TAGSTATE", v1Pattern: "------000000----------0100000010", v2Pattern: "------000000-------------0001000", errorStatus: "0x00100000", resolution: "RESET_GPU", investigatory: "CONTACT_SUPPORT"},
	{code: 145, subcode: "RLW_TAGSTATE", v1Pattern: "------000000----------0100000010", v2Pattern: "------000000-------------0001000", errorStatus: "0x80000000", resolution: "IGNORE", investigatory: "CONTACT_SUPPORT"},
	{code: 146, subcode: "TLW_CTRL", v1Pattern: "------000000----------0100100011", v2Pattern: "------000000-------------0001001", errorStatus: "0x00000001", resolution: "IGNORE", investigatory: "IGNORE"},
	{code: 146, subcode: "TLW_CTRL", v1Pattern: "------000000----------0100100011", v2Pattern: "------000000-------------0001001", errorStatus: "0x00000002", resolution: "IGNORE", investigatory: "CONTACT_SUPPORT"},
	{code: 146, subcode: "TLW_CTRL", v1Pattern: "------000000----------0100100011", v2Pattern: "------000000-------------0001001", errorStatus: "0x00000004", resolution: "RESET_GPU", investigatory: "CONTACT_SUPPORT"},
	{code: 146, subcode: "TLW_CTRL", v1Pattern: "------000000----------0100100011", v2Pattern: "------000000-------------0001001", errorStatus: "0x80000000", resolution: "IGNORE", investigatory: "CONTACT_SUPPORT"},
	{code: 146, subcode: "TLW_RX/TLW_RX_PIPE0", v1Pattern: "------000000----------0101000011", v2Pattern: "------000000-------------0001010", errorStatus: "0x00000001", resolution: "IGNORE", investigatory: "IGNORE"},
	{code: 146, subcode: "TLW_RX/TLW_RX_PIPE0", v1Pattern: "------000000----------0101000011", v2Pattern: "------000000-------------0001010", errorStatus: "0x00000002", resolution: "IGNORE", investigatory: "CONTACT_SUPPORT"},
	{code: 146, subcode: "TLW_RX/TLW_RX_PIPE0", v1Pattern: "------000000----------0101000011", v2Pattern: "------000000-------------0001010", errorStatus: "0x00000004", resolution: "RESET_GPU", investigatory: "CONTACT_SUPPORT"},
	{code: 146, subcode: "TLW_RX/TLW_RX_PIPE0", v1Pattern: "------000000----------0101000011", v2Pattern: "------000000-------------0001010", errorStatus: "0x80000000", resolution: "IGNORE", investigatory: "CONTACT_SUPPORT"},
	{code: 146, subcode: "TLW_RX/TLW_RX_PIPE1", v1Pattern: "------000000----------0101000011", v2Pattern: "------000000-------------0001011", errorStatus: "0x00000001", resolution: "IGNORE", investigatory: "IGNORE"},
	{code: 146, subcode: "TLW_RX/TLW_RX_PIPE1", v1Pattern: "------000000----------0101000011", v2Pattern: "------000000-------------0001011", errorStatus: "0x00000002", resolution: "IGNORE", investigatory: "CONTACT_SUPPORT"},
	{code: 146, subcode: "TLW_RX/TLW_RX_PIPE1", v1Pattern: "------000000----------0101000011", v2Pattern: "------000000-------------0001011", errorStatus: "0x00000004", resolution: "RESET_GPU", investigatory: "CONTACT_SUPPORT"},
	{code: 146, subcode: "TLW_RX/TLW_RX_PIPE1", v1Pattern: "------000000----------0101000011", v2Pattern: "------000000-------------0001011", errorStatus: "0x80000000", resolution: "IGNORE", investigatory: "CONTACT_SUPPORT"},
	{code: 146, subcode: "TLW_TX/TLW_TX_PIPE0", v1Pattern: "------000000----------0101100011", v2Pattern: "------000000-------------0001100", errorStatus: "0x00000001", resolution: "IGNORE", investigatory: "IGNORE"},
	{code: 146, subcode: "TLW_TX/TLW_TX_PIPE0", v1Pattern: "------000000----------0101100011", v2Pattern: "------000000-------------0001100", errorStatus: "0x00000002", resolution: "IGNORE", investigatory: "CONTACT_SUPPORT"},
	{code: 146, subcode: "TLW_TX/TLW_TX_PIPE0", v1Pattern: "------000000----------0101100011", v2Pattern: "------000000-------------0001100", errorStatus: "0x00000004", resolution: "RESET_GPU", investigatory: "CONTACT_SUPPORT"},
	{code: 146, subcode: "TLW_TX/TLW_TX_PIPE0", v1Pattern: "------000000----------0101100011", v2Pattern: "------000000-------------0001100", errorStatus: "0x80000000", resolution: "IGNORE", investigatory: "CONTACT_SUPPORT"},
	{code: 146, subcode: "TLW_TX/TLW_TX_PIPE1", v1Pattern: "------000000----------0101100011", v2Pattern: "------000000-------------0001101", errorStatus: "0x00000001", resolution: "IGNORE", investigatory: "IGNORE"},
	{code: 146, subcode: "TLW_TX/TLW_TX_PIPE1", v1Pattern: "------000000----------0101100011", v2Pattern: "------000000-------------0001101", errorStatus: "0x00000002", resolution: "IGNORE", investigatory: "CONTACT_SUPPORT"},
	{code: 146, subcode: "TLW_TX/TLW_TX_PIPE1", v1Pattern: "------000000----------0101100011", v2Pattern: "------000000-------------0001101", errorStatus: "0x00000004", resolution: "RESET_GPU", investigatory: "CONTACT_SUPPORT"},
	{code: 146, subcode: "TLW_TX/TLW_TX_PIPE1", v1Pattern: "------000000----------0101100011", v2Pattern: "------000000-------------0001101", errorStatus: "0x80000000", resolution: "IGNORE", investigatory: "CONTACT_SUPPORT"},
	{code: 147, subcode: "TREX", v1Pattern: "------000000----------0110000100", v2Pattern: "------000000-------------0001110", errorStatus: "0x00000001", resolution: "IGNORE", investigatory: "CONTACT_SUPPORT"},
	{code: 147, subcode: "TREX", v1Pattern: "------000000----------0110000100", v2Pattern: "------000000-------------0001110", errorStatus: "0x80000000", resolution: "IGNORE", investigatory: "CONTACT_SUPPORT"},
	{code: 148, subcode: "NVLPW_CTRL/NVLPW", v1Pattern: "------000000----------0000000101", v2Pattern: "------000000-------------0001111", errorStatus: "0x80000000", resolution: "IGNORE", investigatory: "CONTACT_SUPPORT"},
	{code: 149, subcode: "NETIR/NETIR_INT", v1Pattern: "------000000----------0000000110", v2Pattern: "------000000-------------0011000", errorStatus: "", resolution: "RESET_GPU", investigatory: "SYMPATHETIC_REPORT_SOLO"},
	{code: 149, subcode: "NETIR_LINK_EVT/NETIR_LINK_DOWN", v1Pattern: "------000000----------0111000110", v2Pattern: "------000000-------------0010001", errorStatus: "", resolution: "RESET_GPU", investigatory: "SYMPATHETIC_REPORT_SOLO"},
	{code: 149, subcode: "NETIR_LINK_EVT/NETIR_LINK_DOWN", v1Pattern: "------000001----------0111000110", v2Pattern: "------000001-------------0010001", errorStatus: "", resolution: "RESET_GPU", investigatory: "REPORT_ISSUE (if seen >1 per day)"},
	{code: 149, subcode: "NETIR_LINK_EVT/NETIR_LINK_DOWN", v1Pattern: "------000010----------0111000110", v2Pattern: "------000010-------------0010001", errorStatus: "", resolution: "RESET_GPU", investigatory: "INVESTIGATE_LINK_SI"},
	{code: 149, subcode: "NETIR_LINK_EVT/NETIR_LINK_DOWN", v1Pattern: "------000100----------0111000110", v2Pattern: "------000100-------------0010001", errorStatus: "", resolution: "RESET_GPU", investigatory: "INVESTIGATE_LINK_SI"},
	{code: 149, subcode: "NETIR_LINK_EVT/NETIR_LINK_DOWN", v1Pattern: "------001010----------0111000110", v2Pattern: "------001010-------------0010001", errorStatus: "", resolution: "RESET_GPU", investigatory: "INVESTIGATE_LINK_SI"},
	{code: 149, subcode: "NETIR_LINK_EVT/NETIR_LINK_DOWN", v1Pattern: "------001111----------0111000110", v2Pattern: "------001111-------------0010001", errorStatus: "", resolution: "RESET_GPU", investigatory: "INVESTIGATE_SW_USER"},
	{code: 149, subcode: "NETIR_LINK_EVT/NETIR_LINK_DOWN", v1Pattern: "------010000----------0111000110", v2Pattern: "------010000-------------0010001", errorStatus: "", resolution: "RESET_GPU", investigatory: "INVESTIGATE_SW_USER_LINK_SI"},
	{code: 149, subcode: "NETIR_LINK_EVT/NETIR_LINK_DOWN", v1Pattern: "------010001----------0111000110", v2Pattern: "------010001-------------0010001", errorStatus: "", resolution: "RESET_GPU", investigatory: "INVESTIGATE_SW_USER"},
	{code: 149, subcode: "NETIR_LINK_EVT/NETIR_LINK_DOWN", v1Pattern: "------010010----------0111000110", v2Pattern: "------010010-------------0010001", errorStatus: "", resolution: "RESET_GPU", investigatory: "INVESTIGATE_SW_USER_LINK_SI"},
	{code: 149, subcode: "NETIR_LINK_EVT/NETIR_LINK_DOWN", v1Pattern: "------010101----------0111000110", v2Pattern: "------010101-------------0010001", errorStatus: "", resolution: "RESET_GPU", investigatory: "INVESTIGATE_PEER_DEVICE"},
	{code: 149, subcode: "NETIR_LINK_EVT/NETIR_LINK_DOWN", v1Pattern: "------010110----------0111000110", v2Pattern: "------010110-------------0010001", errorStatus: "", resolution: "RESET_GPU", investigatory: "INVESTIGATE_SW_USER"},
	{code: 149, subcode: "NETIR_LINK_EVT/NETIR_LINK_DOWN", v1Pattern: "------100000----------0111000110", v2Pattern: "------100000-------------0010001", errorStatus: "", resolution: "RESET_GPU", investigatory: "INVESTIGATE_PEER_DEVICE"},
	{code: 149, subcode: "NETIR_LINK_EVT/NETIR_LINK_DOWN", v1Pattern: "------100001----------0111000110", v2Pattern: "------100001-------------0010001", errorStatus: "", resolution: "RESET_GPU", investigatory: "INVESTIGATE_PEER_DEVICE"},
	{code: 149, subcode: "NETIR_LINK_EVT/NETIR_LINK_DOWN", v1Pattern: "------100010----------0111000110", v2Pattern: "------100010-------------0010001", errorStatus: "", resolution: "RESET_GPU", investigatory: "INVESTIGATE_PEER_DEVICE"},
	{code: 149, subcode: "NETIR_LINK_EVT/NETIR_LINK_DOWN", v1Pattern: "------100011----------0111000110", v2Pattern: "------100011-------------0010001", errorStatus: "", resolution: "RESET_GPU", investigatory: "INVESTIGATE_PEER_DEVICE"},
	{code: 149, subcode: "NETIR_LINK_EVT/NETIR_LINK_DOWN", v1Pattern: "------100100----------0111000110", v2Pattern: "------100100-------------0010001", errorStatus: "", resolution: "RESET_GPU", investigatory: "INVESTIGATE_PEER_DEVICE"},
	{code: 149, subcode: "NETIR_LINK_EVT/NETIR_LINK_DOWN", v1Pattern: "------100101----------0111000110", v2Pattern: "------100101-------------0010001", errorStatus: "", resolution: "RESET_GPU", investigatory: "INVESTIGATE_PEER_DEVICE"},
	{code: 149, subcode: "NETIR_LINK_EVT/NETIR_LINK_DOWN", v1Pattern: "------100110----------0111000110", v2Pattern: "------100110-------------0010001", errorStatus: "", resolution: "IGNORE", investigatory: "INVESTIGATE_SW/USER"},
	{code: 149, subcode: "NETIR_LINK_EVT/NETIR_LINK_DOWN", v1Pattern: "------101000----------0111000110", v2Pattern: "------101000-------------0010001", errorStatus: "", resolution: "IGNORE", investigatory: "INVESTIGATE_HOST"},
	{code: 149, subcode: "NETIR_LINK_EVT/NETIR_LINK_DOWN", v1Pattern: "------101010----------0111000110", v2Pattern: "------101010-------------0010001", errorStatus: "", resolution: "RESET_GPU", investigatory: "INVESTIGATE_LINK_SI_AND_CABLES"},
	{code: 149, subcode: "NETIR_LINK_EVT/NETIR_LINK_DOWN", v1Pattern: "------101011----------0111000110", v2Pattern: "------101011-------------0010001", errorStatus: "", resolution: "RESET_GPU", investigatory: "INVESTIGATE_LINK_SI_AND_CABLES"},
	{code: 149, subcode: "NETIR_BER_EVENT", v1Pattern: "------000000----------1000100110", v2Pattern: "------000000-------------0010011", errorStatus: "0x00000000", resolution: "IGNORE", investigatory: "INVESTIGATE_LINK_SI_AND_CABLES"},
	{code: 149, subcode: "NETIR_BER_EVENT", v1Pattern: "------000000----------1000100110", v2Pattern: "------000000-------------0010011", errorStatus: "0x00000001", resolution: "IGNORE", investigatory: "INVESTIGATE_LINK_SI_AND_CABLES"},
	{code: 149, subcode: "NETIR_BER_EVENT", v1Pattern: "------000000----------1000100110", v2Pattern: "------000000-------------0010011", errorStatus: "0x00000002", resolution: "IGNORE", investigatory: "INVESTIGATE_LINK_SI_AND_CABLES"},
	{code: 149, subcode: "NETIR_BER_EVENT", v1Pattern: "------000000----------1000100110", v2Pattern: "------000000-------------0010011", errorStatus: "0x00000003", resolution: "IGNORE", investigatory: "INVESTIGATE_LINK_SI_AND_CABLES"},
	{code: 149, subcode: "NETIR_MFDE_EVENT", v1Pattern: "------000000----------1001000110", v2Pattern: "------000000-------------0010100", errorStatus: "0x00000001", resolution: "RESET_GPU", investigatory: "CONTACT_SUPPORT"},
	{code: 149, subcode: "NETIR_MFDE_EVENT", v1Pattern: "------000000----------1001000110", v2Pattern: "------000000-------------0010100", errorStatus: "0x00000003", resolution: "IGNORE", investigatory: "IGNORE"},
	{code: 149, subcode: "NETIR_MFDE_EVENT", v1Pattern: "------000000----------1001000110", v2Pattern: "------000000-------------0010100", errorStatus: "0x00000004", resolution: "RESET_GPU", investigatory: "CONTACT_SUPPORT"},
	{code: 149, subcode: "NETIR_MFDE_EVENT", v1Pattern: "------000000----------1001000110", v2Pattern: "------000000-------------0010100", errorStatus: "0x00000005", resolution: "RESET_GPU", investigatory: "CONTACT_SUPPORT"},
	{code: 149, subcode: "NETIR_MFDE_EVENT", v1Pattern: "------000000----------1001000110", v2Pattern: "------000000-------------0010100", errorStatus: "0x00000007", resolution: "RESET_GPU", investigatory: "CONTACT_SUPPORT"},
	{code: 150, subcode: "MSE Degraded", v1Pattern: "------000000----------0000000000", v2Pattern: "------000000-------------0000000", errorStatus: "0x00000000/0xFFFFFFFF", resolution: "RESET_GPU", investigatory: "CONTACT_SUPPORT"},
	{code: 150, subcode: "MSE_WATCHDOG", v1Pattern: "------000000----------0000000000", v2Pattern: "------000000-------------0000000", errorStatus: "0x00000000", resolution: "RESET_GPU", investigatory: "CONTACT_SUPPORT"},
}
