// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package xid

import (
	"strings"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
)

// DriverEra distinguishes the two IntrInfo bit layouts NVIDIA's Xid
// 144-150 decode table uses: pre-R575 drivers pack the fault subcode
// into IntrInfo[9:5], R575-and-later drivers use IntrInfo[6:0] instead.
// A verdict decoded under the wrong era would read the wrong bits, so
// DriverEraUnknown is the safe default whenever the version can't be
// determined — callers must not guess.
type DriverEra int

const (
	// DriverEraUnknown means the driver version could not be read or
	// parsed. NVLink5 subcode decoding that depends on IntrInfo bit
	// position must not proceed under this era.
	DriverEraUnknown DriverEra = iota
	// DriverEraV1 is a driver older than R575.
	DriverEraV1
	// DriverEraV2 is R575 or newer.
	DriverEraV2
)

func (e DriverEra) String() string {
	switch e {
	case DriverEraV1:
		return "v1(<R575)"
	case DriverEraV2:
		return "v2(>=R575)"
	default:
		return "unknown"
	}
}

// nvidiaDriverEraThreshold is the first major driver version using the
// V2 IntrInfo[6:0] encoding.
const nvidiaDriverEraThreshold = 575

// DriverEraFrom classifies the host's driver version against NVIDIA's
// Xid 144-150 decode-table threshold. An unknown version is
// DriverEraUnknown, so it narrows what this package will match rather
// than guessing a bit layout.
func DriverEraFrom(v health.DriverVersion) DriverEra {
	if !v.Known() {
		return DriverEraUnknown
	}
	if v.AtLeast(nvidiaDriverEraThreshold, 0, 0) {
		return DriverEraV2
	}
	return DriverEraV1
}

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

// nvlink5DecodeTable is generated from NVIDIA's Xid 144-150
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

// nvlink5resolutionPair is the pair of actions a matched row or the
// fallback resolves to, before [NVLink5Resolution.Matched] is known.
type nvlink5resolutionPair struct {
	immediate     string
	investigatory string
}

// nvlink5FallbackResolution derives a conservative resolution from the
// literal Fatal/Nonfatal text NVRM prints in the log line, used when
// [nvlink5DecodeTable] has no confirmed entry for an event (driver era
// unknown, IntrInfo/ErrorStatus unparseable, or a genuinely
// undocumented combination). Favors the safer direction on ambiguity:
// an unconfirmed Fatal event still recommends RESET_GPU + CONTACT_SUPPORT
// rather than defaulting toward IGNORE.
func nvlink5FallbackResolution(fatal bool) nvlink5resolutionPair {
	if fatal {
		return nvlink5resolutionPair{immediate: "RESET_GPU", investigatory: "CONTACT_SUPPORT"}
	}
	return nvlink5resolutionPair{immediate: "IGNORE", investigatory: "CONTACT_SUPPORT"}
}

// NVLink5Resolution is NVIDIA's documented immediate/investigatory
// guidance for one NVLink5-family (Xid 144-150) occurrence, decoded
// from its raw log-line fields against NVIDIA's Xid 144-150 decode
// table. Checks use this to decide severity and recommended
// remediation action.
type NVLink5Resolution struct {
	Resolution    string
	Investigation string
	// Matched reports whether Resolution/Investigation came from an
	// exact (code, subcode, error-status) table entry NVIDIA's
	// docs stated unambiguously, as opposed to the generic
	// Fatal-flag-derived fallback (RESET_GPU/CONTACT_SUPPORT when
	// Fatal, IGNORE/CONTACT_SUPPORT when not) used when the table has
	// no confirmed entry for this combination. It is also false for a
	// row's escalation taken without a known driver era to confirm it.
	Matched bool
}

// DecodeNVLink5Resolution decodes detail (as parsed straight from the
// log line by toolkit/xid.ParseXid, e.g. via one of
// [xid.XidMeasurement.Events]) against NVIDIA's Xid 144-150 decode
// table for the given Xid code and driver era. era must come from
// classifying the host's driver version (see [DriverEraFrom] and
// [health.Host]) —
// passing [DriverEraUnknown] limits matching to subcodes NVIDIA's
// table disambiguates by ErrorStatus alone, since pre-/post-R575
// drivers place the IntrInfo subcode at different bit positions and
// guessing the wrong one risks matching an unrelated row.
//
// Matching proceeds in two stages against rows sharing detail's Xid
// code and subcode: first by ErrorStatus, which uniquely
// disambiguates most subcodes. If more than one candidate row
// remains (NVIDIA's table differentiates a few subcodes, e.g.
// Xid 149's NETIR_LINK_EVT/NETIR_LINK_DOWN family, purely by IntrInfo
// bit pattern instead of ErrorStatus), pattern-matching against the
// driver-era-appropriate IntrInfo bit layout picks among them. Falls
// back to [nvlink5FallbackResolution] whenever no single row is
// confirmed.
func DecodeNVLink5Resolution(code int64, era DriverEra, detail NVLink5Detail) NVLink5Resolution {
	candidates := make([]nvlink5DecodeRow, 0, 4)
	for _, row := range nvlink5DecodeTable {
		if row.code == code && subcodeMatches(detail.SubComponent, row.subcode) {
			candidates = append(candidates, row)
		}
	}

	switch byStatus := filterByErrorStatus(candidates, detail.ErrorStatus); len(byStatus) {
	case 1:
		return applyResolution(byStatus[0], era, detail.IntrInfo)
	case 0:
		// A row naming a different ErrorStatus describes a different
		// fault, however well its IntrInfo pattern fits.
		candidates = withoutErrorStatus(candidates)
	default:
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
					return applyResolution(row, era, detail.IntrInfo)
				}
			}
		}
	}

	res := nvlink5FallbackResolution(detail.Fatal)
	return NVLink5Resolution{Resolution: res.immediate, Investigation: res.investigatory, Matched: false}
}

// applyResolution builds the matched resolution for row, promoting
// Resolution to row.action2Resolution when row declares one and the
// driver-era-appropriate action2 IntrInfo pattern (parsed from
// intrInfoHex) also matches — the "promoteable" ('*') condition
// NVIDIA's source spreadsheet documents for a few rows (e.g. Xid 145
// RLW_RXPIPE), where a second, more specific bit must also be set to
// escalate past the base resolution.
func applyResolution(row nvlink5DecodeRow, era DriverEra, intrInfoHex string) NVLink5Resolution {
	res := NVLink5Resolution{Resolution: row.resolution, Investigation: row.investigatory, Matched: true}

	if row.action2V1Pattern == "" && row.action2V2Pattern == "" {
		return res
	}
	intrInfo, ok := parseHexUint32(intrInfoHex)
	if !ok {
		return res
	}
	if era == DriverEraUnknown {
		// The escalation bit sits at a different position in each era,
		// so escalate if either layout has it set, unconfirmed: missing
		// a reset costs more than recommending one the table might not.
		if matchesPattern(intrInfo, row.action2V1Pattern) || matchesPattern(intrInfo, row.action2V2Pattern) {
			res.Resolution = row.action2Resolution
			res.Matched = false
		}
		return res
	}
	pattern := row.action2V2Pattern
	if era == DriverEraV1 {
		pattern = row.action2V1Pattern
	}
	if pattern != "" && matchesPattern(intrInfo, pattern) {
		res.Resolution = row.action2Resolution
	}
	return res
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

// withoutErrorStatus returns the rows among candidates that leave
// errorStatus blank, i.e. match any value.
func withoutErrorStatus(candidates []nvlink5DecodeRow) []nvlink5DecodeRow {
	var out []nvlink5DecodeRow
	for _, row := range candidates {
		if row.errorStatus == "" {
			out = append(out, row)
		}
	}
	return out
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
