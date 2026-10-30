// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package xid

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParse(t *testing.T) {
	// want.Message is filled from message, or from input when message is
	// empty, because the line has no prefix before its marker.
	tests := []struct {
		name    string
		input   string
		message string
		want    Event
	}{
		{
			name:  "ordinary full",
			input: "NVRM: Xid (PCI:0000:01:00): 79, pid=1234, name=python3, GPU has fallen off the bus.",
			want:  Event{PCIAddress: "0000:01:00", Code: 79, PID: 1234, HasPID: true, ProcessName: "python3"},
		},
		{
			name:  "code only",
			input: "NVRM: Xid (PCI:0000:01:00): 31, Ch 0000000a, intr 10000000",
			want:  Event{PCIAddress: "0000:01:00", Code: 31},
		},
		{
			name:  "no trailing comma",
			input: "NVRM: Xid (PCI:0000:01:00): 79",
			want:  Event{PCIAddress: "0000:01:00", Code: 79},
		},
		{
			name:    "journal prefix",
			input:   "Jun 09 12:00:00 node1 kernel: NVRM: Xid (PCI:0000:65:00): 48, pid=999, name=cuda-app",
			message: "NVRM: Xid (PCI:0000:65:00): 48, pid=999, name=cuda-app",
			want:    Event{PCIAddress: "0000:65:00", Code: 48, PID: 999, HasPID: true, ProcessName: "cuda-app"},
		},
		{
			name:    "unknown attribution",
			input:   "[111111111.111] NVRM: Xid (PCI:0000:05:00): 79, pid='<unknown>', name=<unknown>, GPU has fallen off the bus.",
			message: "NVRM: Xid (PCI:0000:05:00): 79, pid='<unknown>', name=<unknown>, GPU has fallen off the bus.",
			want:    Event{PCIAddress: "0000:05:00", Code: 79},
		},
		{
			name:  "numeric pid unknown name",
			input: "NVRM: Xid (PCI:0000:01:00): 79, pid=42, name=<unknown>, x",
			want:  Event{PCIAddress: "0000:01:00", Code: 79, PID: 42, HasPID: true},
		},
		{
			name:  "unknown pid stops attribution",
			input: "NVRM: Xid (PCI:0000:01:00): 79, pid='<unknown>', name=python3, x",
			want:  Event{PCIAddress: "0000:01:00", Code: 79},
		},
		{
			name:  "NVLink5 with unknown pid stays ordinary",
			input: "NVRM: Xid (PCI:0018:01:00): 149, pid='<unknown>', name=<unknown>, NETIR_LINK_EVT Fatal XC0 i0 Link 08 (0x004505c6 0x00000000)",
			want:  Event{PCIAddress: "0018:01:00", Code: 149},
		},
		{
			name:  "pid overflow",
			input: "NVRM: Xid (PCI:0000:01:00): 79, pid=999999999999999999999999999999, name=a, x",
			want:  Event{PCIAddress: "0000:01:00", Code: 79, ProcessName: "a"},
		},
		{
			name:  "MIG GPU instance",
			input: "NVRM: Xid (PCI:0000:01:00 GPU-I:01): 79, pid=1, name=a, x",
			want:  Event{PCIAddress: "0000:01:00", Code: 79, PID: 1, HasPID: true, ProcessName: "a"},
		},
		{
			name:  "MIG compute instance",
			input: "NVRM: Xid (PCI:0000:01:00 GPU-I:01 GPU-CI:02): 79, x",
			want:  Event{PCIAddress: "0000:01:00", Code: 79},
		},
		{
			name:  "no PCI token",
			input: "NVRM: Xid (0000:03:00): 14, Channel 00000001",
			want:  Event{PCIAddress: "0000:03:00", Code: 14},
		},
		{
			name:  "R580 root cause suffix",
			input: "NVRM: Xid (PCI:0000:01:00): 45, pid=1, name=a, Ch 00000001 caused by previous Xid 79",
			want:  Event{PCIAddress: "0000:01:00", Code: 45, PID: 1, HasPID: true, ProcessName: "a"},
		},
		{
			name:  "NVLink5 fatal with tail",
			input: "NVRM: Xid (PCI:0018:01:00): 149, NETIR_LINK_EVT Fatal XC0 i0 Link 08 (0x004505c6 0x00000000 0x00000001 0x00000002)",
			want: Event{PCIAddress: "0018:01:00", Code: 149, NVLink5: &NVLink5{
				SubComponent: "NETIR_LINK_EVT", Fatal: true, Link: 8,
				IntrInfo: "0x004505c6", ErrorStatus: "0x00000000", DebugData: "0x00000001 0x00000002",
			}},
		},
		{
			name:  "NVLink5 nonfatal no tail",
			input: "NVRM: Xid (PCI:0000:5f:00): 145, RLW_REMAP Nonfatal XC0 i0 Link 02",
			want:  Event{PCIAddress: "0000:5f:00", Code: 145, NVLink5: &NVLink5{SubComponent: "RLW_REMAP", Link: 2}},
		},
		{
			name:  "NVLink5 pid and name first",
			input: "NVRM: Xid (PCI:0009:01:00): 145, pid=2348952, name=cache_mgr_main, RLW_SRC_TRACK Fatal XC1 i0 Link 17 (0x040a28e2 0x00000020 0x00000000)",
			want: Event{PCIAddress: "0009:01:00", Code: 145, PID: 2348952, HasPID: true, ProcessName: "cache_mgr_main", NVLink5: &NVLink5{
				SubComponent: "RLW_SRC_TRACK", Fatal: true, Link: 17,
				IntrInfo: "0x040a28e2", ErrorStatus: "0x00000020", DebugData: "0x00000000",
			}},
		},
		{
			name:  "NVLink5 link minus one",
			input: "NVRM: Xid (PCI:0009:01:00): 149, NETIR  Fatal   XC0 i0 Link -1 (0x000fe406 0x00000000 0x00000000)",
			want: Event{PCIAddress: "0009:01:00", Code: 149, NVLink5: &NVLink5{
				SubComponent: "NETIR", Fatal: true, Link: -1,
				IntrInfo: "0x000fe406", ErrorStatus: "0x00000000", DebugData: "0x00000000",
			}},
		},
		{
			name:  "NVLink5 two-word tail",
			input: "NVRM: Xid (PCI:0018:01:00): 149, NETIR_LINK_EVT Fatal XC0 i0 Link 08 (0x004505c6 0x00000000)",
			want: Event{PCIAddress: "0018:01:00", Code: 149, NVLink5: &NVLink5{
				SubComponent: "NETIR_LINK_EVT", Fatal: true, Link: 8, IntrInfo: "0x004505c6", ErrorStatus: "0x00000000",
			}},
		},
		{
			name:  "NVLink5 unterminated tail",
			input: "NVRM: Xid (PCI:0018:01:00): 149, NETIR_LINK_EVT Fatal XC0 i0 Link 08 (0x004505c6 0x00000000",
			want:  Event{PCIAddress: "0018:01:00", Code: 149, NVLink5: &NVLink5{SubComponent: "NETIR_LINK_EVT", Fatal: true, Link: 8}},
		},
		{
			name:  "MSE Degraded",
			input: "NVRM: Xid (PCI:0000:01:00): 150, MSE Degraded Fatal XC0 i0 Link 01 (0x1 0x2)",
			want:  Event{PCIAddress: "0000:01:00", Code: 150},
		},
		{
			name:  "154 R580",
			input: "NVRM: Xid (PCI:0000:01:00): 154, GPU recovery action changed from 0x0 (None) to 0x1 (GPU Reset Required)",
			want: Event{PCIAddress: "0000:01:00", Code: 154, RecoveryAction: &RecoveryAction{
				FromCode: "0x0", FromName: "None", ToCode: "0x1", ToName: "GPU Reset Required",
			}},
		},
		{
			name:  "154 R610",
			input: "NVRM: Xid (PCI:0000:01:00): 154, GPU recovery action changed from 0x0 (None) to 0x1 (PF FLR)",
			want: Event{PCIAddress: "0000:01:00", Code: 154, RecoveryAction: &RecoveryAction{
				FromCode: "0x0", FromName: "None", ToCode: "0x1", ToName: "PF FLR",
			}},
		},
		{
			name:  "13 multiple warp errors",
			input: "NVRM: Xid (PCI:0000:85:00): 13, Graphics SM Global Exception on (GPC 11, TPC 2, SM 1): Multiple Warp Errors",
			want:  Event{PCIAddress: "0000:85:00", Code: 13, GPC: new(int64(11)), TPC: new(int64(2)), SM: new(int64(1))},
		},
		{
			name:  "13 out of range register",
			input: "NVRM: Xid (PCI:0000:85:00): 13, Graphics SM Warp Exception on (GPC 2, TPC 0, SM 0): Out Of Range Register",
			want:  Event{PCIAddress: "0000:85:00", Code: 13, GPC: new(int64(2)), TPC: new(int64(0)), SM: new(int64(0))},
		},
		{
			name:  "13 out of range address with attribution",
			input: "NVRM: Xid (PCI:0000:89:00): 13, pid=1582259, name=nvc:[driver], Graphics SM Warp Exception on (GPC 1, TPC 3, SM 0): Out Of Range Address",
			want: Event{
				PCIAddress: "0000:89:00", Code: 13, PID: 1582259, HasPID: true, ProcessName: "nvc:[driver]",
				GPC: new(int64(1)), TPC: new(int64(3)), SM: new(int64(0)),
			},
		},
		{
			name:  "13 SM exception with uneven spacing",
			input: "NVRM: Xid (PCI:0000:85:00): 13, Graphics SM Global Exception on (GPC  9,TPC 2,  SM 1): Multiple Warp Errors",
			want:  Event{PCIAddress: "0000:85:00", Code: 13, GPC: new(int64(9)), TPC: new(int64(2)), SM: new(int64(1))},
		},
		{
			name:  "13 SM exception with unknown attribution",
			input: "NVRM: Xid (PCI:0000:ae:00): 13, pid='<unknown>', name=<unknown>, Graphics SM Warp Exception on (GPC 2, TPC 0, SM 0): Illegal Instruction Encoding",
			want:  Event{PCIAddress: "0000:ae:00", Code: 13, GPC: new(int64(2)), TPC: new(int64(0)), SM: new(int64(0))},
		},
		{
			name:  "13 ESR without unit",
			input: "NVRM: Xid (PCI:0000:c1:00): 13, Graphics Exception: ESR 0x5697b0=0x0 0x5697b4=0x20 0x5697a8=0x1f81fb60 0x5697ac=0x1174",
			want:  Event{PCIAddress: "0000:c1:00", Code: 13},
		},
		{
			name:  "13 partial unit",
			input: "NVRM: Xid (PCI:0000:85:00): 13, Graphics Exception on (GPC 0, TPC 2): x",
			want:  Event{PCIAddress: "0000:85:00", Code: 13},
		},
		{
			name:  "unit outside Xid 13",
			input: "NVRM: Xid (PCI:0000:3b:00): 31, pid=1, name=a, (GPC 1, TPC 3, SM 0)",
			want:  Event{PCIAddress: "0000:3b:00", Code: 31, PID: 1, HasPID: true, ProcessName: "a"},
		},
		{
			name:  "154 unparsed transition",
			input: "NVRM: Xid (PCI:0000:01:00): 154, something else",
			want:  Event{PCIAddress: "0000:01:00", Code: 154},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := Parse(test.input)
			require.NoError(t, err)
			test.want.Message = test.message
			if test.want.Message == "" {
				test.want.Message = test.input
			}
			require.Equal(t, test.want, got)
		})
	}
}

var parseErrorInputs = []struct {
	name   string
	input  string
	reason string
}{
	{name: "no device group", input: "NVRM: Xid error: xyz, invalid data", reason: ReasonUnrecognizedFormat},
	{name: "no address", input: "NVRM: Xid (invalid): some error", reason: ReasonUnrecognizedFormat},
	{name: "malformed device group", input: "NVRM: Xid (PCI:0000:01:00 junk): 79", reason: ReasonMissingPCIAddress},
	{name: "code overflow", input: "NVRM: Xid (PCI:0000:01:00): 999999999999999999999999999999999, overflow", reason: ReasonInvalidCode},
	{name: "code not a number", input: "NVRM: Xid (PCI:0000:01:00): abc", reason: ReasonInvalidCode},
	{name: "attribution in an invalid line", input: "NVRM: Xid (PCI:0000:01:00): x, pid=4242, name=secretproc", reason: ReasonInvalidCode},
}

func TestParseErrors(t *testing.T) {
	for _, test := range parseErrorInputs {
		t.Run(test.name, func(t *testing.T) {
			_, err := Parse(test.input)
			var parseErr *ParseError
			require.True(t, errors.As(err, &parseErr), "error %v", err)
			require.Equal(t, test.reason, parseErr.Reason)
		})
	}
}

func TestParseErrorDetailOmitsLineContent(t *testing.T) {
	for _, test := range parseErrorInputs {
		_, err := Parse(test.input)
		var parseErr *ParseError
		require.True(t, errors.As(err, &parseErr))
		require.LessOrEqual(t, len(parseErr.Detail), 256)
		for _, secret := range []string{test.input, "4242", "secretproc", "0000:01:00"} {
			require.False(t, strings.Contains(parseErr.Error(), secret), "%q leaks %q", parseErr.Error(), secret)
		}
	}
}
