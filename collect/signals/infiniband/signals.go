// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package infiniband defines port signals shared by InfiniBand collectors.
package infiniband

import (
	"slices"

	"github.com/dsx-ai-factory/health-validation/collect"
)

// Counter signals are cumulative; consumers compute deltas and handle resets.
// Rate is link capacity, not measured traffic throughput.
var (
	FirmwareVersion                 = collect.NewSignal("infiniband.firmware_version", "")
	BoardID                         = collect.NewSignal("infiniband.board_id", "")
	HCAType                         = collect.NewSignal("infiniband.hca_type", "")
	StateName                       = collect.NewSignal("infiniband.state_name", "")
	PhysicalStateName               = collect.NewSignal("infiniband.phys_state_name", "")
	RateGbps                        = collect.NewSignal("infiniband.rate_gbps", "gigabits_per_second")
	State                           = collect.NewSignal("infiniband.state", "1")
	PhysicalState                   = collect.NewSignal("infiniband.phys_state", "1")
	LinkLayer                       = collect.NewSignal("infiniband.link_layer", "")
	Rate                            = collect.NewSignal("infiniband.rate", "bytes_per_second")
	ExcessiveBufferOverrunErrors    = collect.NewSignal("infiniband.counters.excessive_buffer_overrun_errors", "1")
	LinkDowned                      = collect.NewSignal("infiniband.counters.link_downed", "1")
	LinkErrorRecovery               = collect.NewSignal("infiniband.counters.link_error_recovery", "1")
	LocalLinkIntegrityErrors        = collect.NewSignal("infiniband.counters.local_link_integrity_errors", "1")
	MulticastRcvPackets             = collect.NewSignal("infiniband.counters.multicast_rcv_packets", "1")
	MulticastXmitPackets            = collect.NewSignal("infiniband.counters.multicast_xmit_packets", "1")
	PortRcvConstraintErrors         = collect.NewSignal("infiniband.counters.port_rcv_constraint_errors", "1")
	PortRcvData                     = collect.NewSignal("infiniband.counters.port_rcv_data", "bytes")
	PortRcvDiscards                 = collect.NewSignal("infiniband.counters.port_rcv_discards", "1")
	PortRcvErrors                   = collect.NewSignal("infiniband.counters.port_rcv_errors", "1")
	PortRcvPackets                  = collect.NewSignal("infiniband.counters.port_rcv_packets", "1")
	PortRcvRemotePhysicalErrors     = collect.NewSignal("infiniband.counters.port_rcv_remote_physical_errors", "1")
	PortRcvSwitchRelayErrors        = collect.NewSignal("infiniband.counters.port_rcv_switch_relay_errors", "1")
	PortXmitConstraintErrors        = collect.NewSignal("infiniband.counters.port_xmit_constraint_errors", "1")
	PortXmitData                    = collect.NewSignal("infiniband.counters.port_xmit_data", "bytes")
	PortXmitDiscards                = collect.NewSignal("infiniband.counters.port_xmit_discards", "1")
	PortXmitPackets                 = collect.NewSignal("infiniband.counters.port_xmit_packets", "1")
	PortXmitWait                    = collect.NewSignal("infiniband.counters.port_xmit_wait", "ticks")
	SymbolError                     = collect.NewSignal("infiniband.counters.symbol_error", "1")
	UnicastRcvPackets               = collect.NewSignal("infiniband.counters.unicast_rcv_packets", "1")
	UnicastXmitPackets              = collect.NewSignal("infiniband.counters.unicast_xmit_packets", "1")
	VL15Dropped                     = collect.NewSignal("infiniband.counters.VL15_dropped", "1")
	HardwareDuplicateRequest        = collect.NewSignal("infiniband.hw_counters.duplicate_request", "1")
	HardwareImpliedNakSeqErr        = collect.NewSignal("infiniband.hw_counters.implied_nak_seq_err", "1")
	HardwareLifespan                = collect.NewSignal("infiniband.hw_counters.lifespan", "milliseconds")
	HardwareLocalAckTimeoutErr      = collect.NewSignal("infiniband.hw_counters.local_ack_timeout_err", "1")
	HardwareNpCnpSent               = collect.NewSignal("infiniband.hw_counters.np_cnp_sent", "1")
	HardwareNpEcnMarkedRocePackets  = collect.NewSignal("infiniband.hw_counters.np_ecn_marked_roce_packets", "1")
	HardwareOutOfBuffer             = collect.NewSignal("infiniband.hw_counters.out_of_buffer", "1")
	HardwareOutOfSequence           = collect.NewSignal("infiniband.hw_counters.out_of_sequence", "1")
	HardwarePacketSeqErr            = collect.NewSignal("infiniband.hw_counters.packet_seq_err", "1")
	HardwareReqCqeError             = collect.NewSignal("infiniband.hw_counters.req_cqe_error", "1")
	HardwareReqCqeFlushError        = collect.NewSignal("infiniband.hw_counters.req_cqe_flush_error", "1")
	HardwareReqRemoteAccessErrors   = collect.NewSignal("infiniband.hw_counters.req_remote_access_errors", "1")
	HardwareReqRemoteInvalidRequest = collect.NewSignal("infiniband.hw_counters.req_remote_invalid_request", "1")
	HardwareRespCqeError            = collect.NewSignal("infiniband.hw_counters.resp_cqe_error", "1")
	HardwareRespCqeFlushError       = collect.NewSignal("infiniband.hw_counters.resp_cqe_flush_error", "1")
	HardwareRespLocalLengthError    = collect.NewSignal("infiniband.hw_counters.resp_local_length_error", "1")
	HardwareRespRemoteAccessErrors  = collect.NewSignal("infiniband.hw_counters.resp_remote_access_errors", "1")
	HardwareRnrNakRetryErr          = collect.NewSignal("infiniband.hw_counters.rnr_nak_retry_err", "1")
	HardwareRoceAdpRetrans          = collect.NewSignal("infiniband.hw_counters.roce_adp_retrans", "1")
	HardwareRoceAdpRetransTo        = collect.NewSignal("infiniband.hw_counters.roce_adp_retrans_to", "1")
	HardwareRoceSlowRestart         = collect.NewSignal("infiniband.hw_counters.roce_slow_restart", "1")
	HardwareRoceSlowRestartCnps     = collect.NewSignal("infiniband.hw_counters.roce_slow_restart_cnps", "1")
	HardwareRoceSlowRestartTrans    = collect.NewSignal("infiniband.hw_counters.roce_slow_restart_trans", "1")
	HardwareRpCnpHandled            = collect.NewSignal("infiniband.hw_counters.rp_cnp_handled", "1")
	HardwareRpCnpIgnored            = collect.NewSignal("infiniband.hw_counters.rp_cnp_ignored", "1")
	HardwareRxAtomicRequests        = collect.NewSignal("infiniband.hw_counters.rx_atomic_requests", "1")
	HardwareRxDctConnect            = collect.NewSignal("infiniband.hw_counters.rx_dct_connect", "1")
	HardwareRxIcrcEncapsulated      = collect.NewSignal("infiniband.hw_counters.rx_icrc_encapsulated", "1")
	HardwareRxReadRequests          = collect.NewSignal("infiniband.hw_counters.rx_read_requests", "1")
	HardwareRxWriteRequests         = collect.NewSignal("infiniband.hw_counters.rx_write_requests", "1")
)

// All returns every supported InfiniBand signal.
func All() collect.Signals {
	return collect.Signals{
		FirmwareVersion, BoardID, HCAType,
		State,
		StateName,
		PhysicalState,
		PhysicalStateName,
		LinkLayer,
		Rate,
		RateGbps,
		ExcessiveBufferOverrunErrors,
		LinkDowned,
		LinkErrorRecovery,
		LocalLinkIntegrityErrors,
		MulticastRcvPackets,
		MulticastXmitPackets,
		PortRcvConstraintErrors,
		PortRcvData,
		PortRcvDiscards,
		PortRcvErrors,
		PortRcvPackets,
		PortRcvRemotePhysicalErrors,
		PortRcvSwitchRelayErrors,
		PortXmitConstraintErrors,
		PortXmitData,
		PortXmitDiscards,
		PortXmitPackets,
		PortXmitWait,
		SymbolError,
		UnicastRcvPackets,
		UnicastXmitPackets,
		VL15Dropped,
		HardwareDuplicateRequest,
		HardwareImpliedNakSeqErr,
		HardwareLifespan,
		HardwareLocalAckTimeoutErr,
		HardwareNpCnpSent,
		HardwareNpEcnMarkedRocePackets,
		HardwareOutOfBuffer,
		HardwareOutOfSequence,
		HardwarePacketSeqErr,
		HardwareReqCqeError,
		HardwareReqCqeFlushError,
		HardwareReqRemoteAccessErrors,
		HardwareReqRemoteInvalidRequest,
		HardwareRespCqeError,
		HardwareRespCqeFlushError,
		HardwareRespLocalLengthError,
		HardwareRespRemoteAccessErrors,
		HardwareRnrNakRetryErr,
		HardwareRoceAdpRetrans,
		HardwareRoceAdpRetransTo,
		HardwareRoceSlowRestart,
		HardwareRoceSlowRestartCnps,
		HardwareRoceSlowRestartTrans,
		HardwareRpCnpHandled,
		HardwareRpCnpIgnored,
		HardwareRxAtomicRequests,
		HardwareRxDctConnect,
		HardwareRxIcrcEncapsulated,
		HardwareRxReadRequests,
		HardwareRxWriteRequests,
	}
}

// DeviceSignals returns the signals read once per HCA rather than per port.
func DeviceSignals() collect.Signals {
	return collect.Signals{FirmwareVersion, BoardID, HCAType}
}

// PortSignals returns the signals read per port, each targeting an
// InfiniBand port.
func PortSignals() collect.Signals {
	device := DeviceSignals()
	return slices.DeleteFunc(All(), func(s collect.Signal) bool { return slices.Contains(device, s) })
}
