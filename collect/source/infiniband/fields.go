// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package infiniband

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	ibsignals "github.com/dsx-ai-factory/health-validation/collect/signals/infiniband"
)

// File names and counter units follow GPUd's InfiniBand class collector.
var paths = map[string]string{
	ibsignals.FirmwareVersion.ID():                 "fw_ver",
	ibsignals.BoardID.ID():                         "board_id",
	ibsignals.HCAType.ID():                         "hca_type",
	ibsignals.State.ID():                           "state",
	ibsignals.PhysicalState.ID():                   "phys_state",
	ibsignals.LinkLayer.ID():                       "link_layer",
	ibsignals.Rate.ID():                            "rate",
	ibsignals.ExcessiveBufferOverrunErrors.ID():    "counters/excessive_buffer_overrun_errors",
	ibsignals.LinkDowned.ID():                      "counters/link_downed",
	ibsignals.LinkErrorRecovery.ID():               "counters/link_error_recovery",
	ibsignals.LocalLinkIntegrityErrors.ID():        "counters/local_link_integrity_errors",
	ibsignals.MulticastRcvPackets.ID():             "counters/multicast_rcv_packets",
	ibsignals.MulticastXmitPackets.ID():            "counters/multicast_xmit_packets",
	ibsignals.PortRcvConstraintErrors.ID():         "counters/port_rcv_constraint_errors",
	ibsignals.PortRcvData.ID():                     "counters/port_rcv_data",
	ibsignals.PortRcvDiscards.ID():                 "counters/port_rcv_discards",
	ibsignals.PortRcvErrors.ID():                   "counters/port_rcv_errors",
	ibsignals.PortRcvPackets.ID():                  "counters/port_rcv_packets",
	ibsignals.PortRcvRemotePhysicalErrors.ID():     "counters/port_rcv_remote_physical_errors",
	ibsignals.PortRcvSwitchRelayErrors.ID():        "counters/port_rcv_switch_relay_errors",
	ibsignals.PortXmitConstraintErrors.ID():        "counters/port_xmit_constraint_errors",
	ibsignals.PortXmitData.ID():                    "counters/port_xmit_data",
	ibsignals.PortXmitDiscards.ID():                "counters/port_xmit_discards",
	ibsignals.PortXmitPackets.ID():                 "counters/port_xmit_packets",
	ibsignals.PortXmitWait.ID():                    "counters/port_xmit_wait",
	ibsignals.SymbolError.ID():                     "counters/symbol_error",
	ibsignals.UnicastRcvPackets.ID():               "counters/unicast_rcv_packets",
	ibsignals.UnicastXmitPackets.ID():              "counters/unicast_xmit_packets",
	ibsignals.VL15Dropped.ID():                     "counters/VL15_dropped",
	ibsignals.HardwareDuplicateRequest.ID():        "hw_counters/duplicate_request",
	ibsignals.HardwareImpliedNakSeqErr.ID():        "hw_counters/implied_nak_seq_err",
	ibsignals.HardwareLifespan.ID():                "hw_counters/lifespan",
	ibsignals.HardwareLocalAckTimeoutErr.ID():      "hw_counters/local_ack_timeout_err",
	ibsignals.HardwareNpCnpSent.ID():               "hw_counters/np_cnp_sent",
	ibsignals.HardwareNpEcnMarkedRocePackets.ID():  "hw_counters/np_ecn_marked_roce_packets",
	ibsignals.HardwareOutOfBuffer.ID():             "hw_counters/out_of_buffer",
	ibsignals.HardwareOutOfSequence.ID():           "hw_counters/out_of_sequence",
	ibsignals.HardwarePacketSeqErr.ID():            "hw_counters/packet_seq_err",
	ibsignals.HardwareReqCqeError.ID():             "hw_counters/req_cqe_error",
	ibsignals.HardwareReqCqeFlushError.ID():        "hw_counters/req_cqe_flush_error",
	ibsignals.HardwareReqRemoteAccessErrors.ID():   "hw_counters/req_remote_access_errors",
	ibsignals.HardwareReqRemoteInvalidRequest.ID(): "hw_counters/req_remote_invalid_request",
	ibsignals.HardwareRespCqeError.ID():            "hw_counters/resp_cqe_error",
	ibsignals.HardwareRespCqeFlushError.ID():       "hw_counters/resp_cqe_flush_error",
	ibsignals.HardwareRespLocalLengthError.ID():    "hw_counters/resp_local_length_error",
	ibsignals.HardwareRespRemoteAccessErrors.ID():  "hw_counters/resp_remote_access_errors",
	ibsignals.HardwareRnrNakRetryErr.ID():          "hw_counters/rnr_nak_retry_err",
	ibsignals.HardwareRoceAdpRetrans.ID():          "hw_counters/roce_adp_retrans",
	ibsignals.HardwareRoceAdpRetransTo.ID():        "hw_counters/roce_adp_retrans_to",
	ibsignals.HardwareRoceSlowRestart.ID():         "hw_counters/roce_slow_restart",
	ibsignals.HardwareRoceSlowRestartCnps.ID():     "hw_counters/roce_slow_restart_cnps",
	ibsignals.HardwareRoceSlowRestartTrans.ID():    "hw_counters/roce_slow_restart_trans",
	ibsignals.HardwareRpCnpHandled.ID():            "hw_counters/rp_cnp_handled",
	ibsignals.HardwareRpCnpIgnored.ID():            "hw_counters/rp_cnp_ignored",
	ibsignals.HardwareRxAtomicRequests.ID():        "hw_counters/rx_atomic_requests",
	ibsignals.HardwareRxDctConnect.ID():            "hw_counters/rx_dct_connect",
	ibsignals.HardwareRxIcrcEncapsulated.ID():      "hw_counters/rx_icrc_encapsulated",
	ibsignals.HardwareRxReadRequests.ID():          "hw_counters/rx_read_requests",
	ibsignals.HardwareRxWriteRequests.ID():         "hw_counters/rx_write_requests",
}

func parse(definition collect.Signal, raw string) (*observation.Value, error) {
	raw = strings.TrimSpace(raw)
	switch definition {
	case ibsignals.LinkLayer, ibsignals.FirmwareVersion, ibsignals.BoardID, ibsignals.HCAType:
		if raw == "" {
			return nil, fmt.Errorf("empty text field")
		}
		return &observation.Value{Kind: &observation.Value_StringValue{StringValue: raw}}, nil
	case ibsignals.State, ibsignals.PhysicalState:
		id, name, ok := strings.Cut(raw, ":")
		if !ok || strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("invalid port state %q", raw)
		}
		raw = strings.TrimSpace(id)
	case ibsignals.Rate:
		parts := strings.Fields(raw)
		if len(parts) < 2 || parts[1] != "Gb/sec" {
			return nil, fmt.Errorf("invalid port rate %q", raw)
		}
		rate, err := strconv.ParseFloat(parts[0], 64)
		if err != nil || math.IsNaN(rate) || math.IsInf(rate, 0) || rate < 0 || rate*125000000 >= float64(math.MaxInt64) {
			return nil, fmt.Errorf("invalid port rate %q", raw)
		}
		return &observation.Value{Kind: &observation.Value_IntValue{IntValue: int64(rate * 125000000)}}, nil
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return nil, err
	}
	multiplier := uint64(1)
	if definition == ibsignals.PortRcvData || definition == ibsignals.PortXmitData {
		multiplier = 4
	}
	// The observation wire type is signed. Reject overflow instead of wrapping
	// cumulative counters or silently losing precision through float64.
	if value > math.MaxInt64/multiplier {
		return nil, fmt.Errorf("counter %q exceeds int64 after unit conversion", raw)
	}
	return &observation.Value{Kind: &observation.Value_IntValue{IntValue: int64(value * multiplier)}}, nil
}
