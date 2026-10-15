// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package observation

import (
	"context"
	"errors"
	"fmt"

	healthv1 "github.com/dsx-ai-factory/health-validation/collect/proto/go/health/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// The shared payload types, aliased from the generated proto types so the
// proto stays the single definition.
type (
	ObservationBatch        = healthv1.ObservationBatch
	Observation             = healthv1.Observation
	ObservationTarget       = healthv1.ObservationTarget
	NodeRef                 = healthv1.NodeRef
	GPURef                  = healthv1.GpuRef
	NVLinkEndpointRef       = healthv1.NvLinkEndpointRef
	GPUPairRef              = healthv1.GpuPairRef
	HardwareInventory       = healthv1.HardwareInventory
	GPUInventorySnapshot    = healthv1.GpuInventorySnapshot
	GPUInventory            = healthv1.GpuInventory
	GPUInventoryEntry       = healthv1.GpuInventoryEntry
	GPUDevice               = healthv1.GpuDevice
	InfiniBandDeviceRef     = healthv1.InfiniBandDeviceRef
	InfiniBandPortRef       = healthv1.InfiniBandPortRef
	KernelRecordRef         = healthv1.KernelRecordRef
	ProcessRef              = healthv1.ProcessRef
	GPUProcessRef           = healthv1.GpuProcessRef
	BlockDeviceRef          = healthv1.BlockDeviceRef
	MountRef                = healthv1.MountRef
	PCIRef                  = healthv1.PCIRef
	FUSEConnectionRef       = healthv1.FuseConnectionRef
	PstoreRecordRef         = healthv1.PstoreRecordRef
	Value                   = healthv1.Value
	CollectionError         = healthv1.CollectionError
	CollectionErrorCategory = healthv1.CollectionErrorCategory
	NoValue                 = healthv1.NoValue
	NoValueReason           = healthv1.NoValueReason
)

// Oneof wrappers for Observation.outcome and Value.kind.
type (
	ObservationTarget_Node               = healthv1.ObservationTarget_Node
	ObservationTarget_GPU                = healthv1.ObservationTarget_Gpu
	ObservationTarget_NVLinkEndpoint     = healthv1.ObservationTarget_NvLinkEndpoint
	ObservationTarget_GPUPair            = healthv1.ObservationTarget_GpuPair
	Observation_Value                    = healthv1.Observation_Value
	Observation_CollectionError          = healthv1.Observation_CollectionError
	Observation_NoValue                  = healthv1.Observation_NoValue
	Value_IntValue                       = healthv1.Value_IntValue
	Value_DoubleValue                    = healthv1.Value_DoubleValue
	Value_StringValue                    = healthv1.Value_StringValue
	Value_BoolValue                      = healthv1.Value_BoolValue
	GPUInventorySnapshot_Inventory       = healthv1.GpuInventorySnapshot_Inventory
	GPUInventorySnapshot_CollectionError = healthv1.GpuInventorySnapshot_CollectionError
	ObservationTarget_InfiniBandDevice   = healthv1.ObservationTarget_InfiniBandDevice
	ObservationTarget_InfiniBandPort     = healthv1.ObservationTarget_InfiniBandPort
	ObservationTarget_KernelRecord       = healthv1.ObservationTarget_KernelRecord
	ObservationTarget_Process            = healthv1.ObservationTarget_Process
	ObservationTarget_GPUProcess         = healthv1.ObservationTarget_GpuProcess
	ObservationTarget_BlockDevice        = healthv1.ObservationTarget_BlockDevice
	ObservationTarget_Mount              = healthv1.ObservationTarget_Mount
	ObservationTarget_PCI                = healthv1.ObservationTarget_Pci
	ObservationTarget_FUSEConnection     = healthv1.ObservationTarget_FuseConnection
	ObservationTarget_PstoreRecord       = healthv1.ObservationTarget_PstoreRecord
	Value_UintValue                      = healthv1.Value_UintValue
)

const (
	CollectionErrorCategoryUnspecified      = healthv1.CollectionErrorCategory_COLLECTION_ERROR_CATEGORY_UNSPECIFIED
	CollectionErrorCategoryTimeout          = healthv1.CollectionErrorCategory_COLLECTION_ERROR_CATEGORY_TIMEOUT
	CollectionErrorCategoryUnsupported      = healthv1.CollectionErrorCategory_COLLECTION_ERROR_CATEGORY_UNSUPPORTED
	CollectionErrorCategoryUnavailable      = healthv1.CollectionErrorCategory_COLLECTION_ERROR_CATEGORY_UNAVAILABLE
	CollectionErrorCategoryNotConfigured    = healthv1.CollectionErrorCategory_COLLECTION_ERROR_CATEGORY_NOT_CONFIGURED
	CollectionErrorCategoryPermissionDenied = healthv1.CollectionErrorCategory_COLLECTION_ERROR_CATEGORY_PERMISSION_DENIED
	CollectionErrorCategoryParseError       = healthv1.CollectionErrorCategory_COLLECTION_ERROR_CATEGORY_PARSE_ERROR
	CollectionErrorCategorySourceError      = healthv1.CollectionErrorCategory_COLLECTION_ERROR_CATEGORY_SOURCE_ERROR
	// CollectionErrorCategoryOther is the fallback for source failures that do
	// not fit a more specific, portable category.
	CollectionErrorCategoryOther = CollectionErrorCategorySourceError

	NoValueReasonUnspecified  = healthv1.NoValueReason_NO_VALUE_REASON_UNSPECIFIED
	NoValueReasonNotSupported = healthv1.NoValueReason_NO_VALUE_REASON_NOT_SUPPORTED
	NoValueReasonNoTargets    = healthv1.NoValueReason_NO_VALUE_REASON_NO_TARGETS
)

// NodeTarget identifies one node.
func NodeTarget(id string) *ObservationTarget {
	return &ObservationTarget{Kind: &ObservationTarget_Node{Node: &NodeRef{Id: id}}}
}

// GPUTarget identifies one physical GPU.
func GPUTarget(uuid string) *ObservationTarget {
	return &ObservationTarget{Kind: &ObservationTarget_GPU{Gpu: &GPURef{Uuid: uuid}}}
}

// GPUSourceLocalTarget identifies a GPU when its UUID could not be collected.
func GPUSourceLocalTarget(sourceLocalID string) *ObservationTarget {
	return &ObservationTarget{Kind: &ObservationTarget_GPU{Gpu: &GPURef{SourceLocalId: sourceLocalID}}}
}

// NVLinkEndpointTarget identifies one local NVLink endpoint on a GPU.
func NVLinkEndpointTarget(gpuUUID string, linkIndex uint32) *ObservationTarget {
	return &ObservationTarget{Kind: &ObservationTarget_NVLinkEndpoint{
		NvLinkEndpoint: &NVLinkEndpointRef{
			Gpu:       &GPURef{Uuid: gpuUUID},
			LinkIndex: linkIndex,
		},
	}}
}

// GPUPairTarget identifies an ordered source/peer GPU pair.
func GPUPairTarget(sourceGPUUUID, peerGPUUUID string) *ObservationTarget {
	return &ObservationTarget{Kind: &ObservationTarget_GPUPair{
		GpuPair: &GPUPairRef{
			SourceGpu: &GPURef{Uuid: sourceGPUUUID},
			PeerGpu:   &GPURef{Uuid: peerGPUUUID},
		},
	}}
}

// InfiniBandDeviceTarget identifies one host-local HCA.
func InfiniBandDeviceTarget(name string) *ObservationTarget {
	return &ObservationTarget{Kind: &ObservationTarget_InfiniBandDevice{InfiniBandDevice: &InfiniBandDeviceRef{Name: name}}}
}

// InfiniBandPortTarget identifies one port on a host-local HCA.
func InfiniBandPortTarget(device string, port uint32) *ObservationTarget {
	return &ObservationTarget{Kind: &ObservationTarget_InfiniBandPort{InfiniBandPort: &InfiniBandPortRef{Device: &InfiniBandDeviceRef{Name: device}, Port: port}}}
}

// KernelRecordTarget identifies one boot-scoped kernel record.
func KernelRecordTarget(bootID string, sequence int64) *ObservationTarget {
	return &ObservationTarget{Kind: &ObservationTarget_KernelRecord{KernelRecord: &KernelRecordRef{BootId: bootID, Sequence: sequence}}}
}

// ProcessTarget identifies a process without conflating reused PIDs.
func ProcessTarget(bootID string, pid uint32, startTimeTicks uint64) *ObservationTarget {
	return &ObservationTarget{Kind: &ObservationTarget_Process{Process: &ProcessRef{BootId: bootID, Pid: pid, StartTimeTicks: startTimeTicks}}}
}

// GPUProcessTarget attributes a process reading to its GPU.
func GPUProcessTarget(gpuUUID, bootID string, pid uint32, startTimeTicks uint64) *ObservationTarget {
	return &ObservationTarget{Kind: &ObservationTarget_GPUProcess{GpuProcess: &GPUProcessRef{Gpu: &GPURef{Uuid: gpuUUID}, Process: &ProcessRef{BootId: bootID, Pid: pid, StartTimeTicks: startTimeTicks}}}}
}

// BlockDeviceTarget identifies a kernel block-device number.
func BlockDeviceTarget(major, minor uint32) *ObservationTarget {
	return &ObservationTarget{Kind: &ObservationTarget_BlockDevice{BlockDevice: &BlockDeviceRef{Major: major, Minor: minor}}}
}

// MountTarget identifies a mount within its namespace.
func MountTarget(namespace string, mountID uint64) *ObservationTarget {
	return &ObservationTarget{Kind: &ObservationTarget_Mount{Mount: &MountRef{Namespace: namespace, MountId: mountID}}}
}

// PCITarget identifies a domain-qualified PCI address.
func PCITarget(address string) *ObservationTarget {
	return &ObservationTarget{Kind: &ObservationTarget_PCI{Pci: &PCIRef{Address: address}}}
}

// FUSEConnectionTarget identifies a connection within one boot.
func FUSEConnectionTarget(bootID string, connectionID uint64) *ObservationTarget {
	return &ObservationTarget{Kind: &ObservationTarget_FUSEConnection{FuseConnection: &FUSEConnectionRef{BootId: bootID, ConnectionId: connectionID}}}
}

// PstoreRecordTarget identifies a persistent record by path and content digest.
func PstoreRecordTarget(path, sha256 string) *ObservationTarget {
	return &ObservationTarget{Kind: &ObservationTarget_PstoreRecord{PstoreRecord: &PstoreRecordRef{Path: path, Sha256: sha256}}}
}

// ValidateBatch verifies every observation before a batch crosses a service
// or ingestion boundary.
func ValidateBatch(batch *ObservationBatch) error {
	if batch == nil {
		return fmt.Errorf("observation batch is required")
	}
	for index, current := range batch.GetObservations() {
		if err := validate(current); err != nil {
			return fmt.Errorf("observation %d: %w", index, err)
		}
	}
	if inventory := batch.GetInventory(); inventory != nil {
		for index, snapshot := range inventory.GetGpuSnapshots() {
			if err := validateGPUInventorySnapshot(snapshot); err != nil {
				return fmt.Errorf("GPU inventory snapshot %d: %w", index, err)
			}
		}
	}
	return nil
}

func validateGPUInventorySnapshot(snapshot *GPUInventorySnapshot) error {
	if snapshot == nil {
		return fmt.Errorf("snapshot is required")
	}
	switch outcome := snapshot.GetOutcome().(type) {
	case *GPUInventorySnapshot_Inventory:
		if outcome == nil || outcome.Inventory == nil {
			return fmt.Errorf("source %q has an inventory outcome with no payload", snapshot.GetSource())
		}
		for index, entry := range outcome.Inventory.GetEntries() {
			if err := validateGPUInventoryEntry(entry); err != nil {
				return fmt.Errorf("entry %d: %w", index, err)
			}
		}
	case *GPUInventorySnapshot_CollectionError:
		if outcome == nil || outcome.CollectionError == nil {
			return fmt.Errorf("source %q has a collection-error outcome with no payload", snapshot.GetSource())
		}
	default:
		return fmt.Errorf("source %q has no outcome", snapshot.GetSource())
	}
	return nil
}

func validateGPUInventoryEntry(entry *GPUInventoryEntry) error {
	if entry == nil {
		return fmt.Errorf("entry is required")
	}
	if entry.GetGpu() == nil && entry.GetCollectionError() == nil {
		return fmt.Errorf("source index %d has no GPU record or collection error", entry.GetSourceIndex())
	}
	for field, failure := range entry.GetFieldErrors() {
		if failure == nil {
			return fmt.Errorf("source index %d has a nil error for field %q", entry.GetSourceIndex(), field)
		}
		if entry.GetGpu() == nil {
			return fmt.Errorf("source index %d has field errors without a GPU record", entry.GetSourceIndex())
		}
		gpu := entry.GetGpu().ProtoReflect()
		definition := gpu.Descriptor().Fields().ByTextName(field)
		if field == "" {
			return fmt.Errorf("source index %d has an error without a field name", entry.GetSourceIndex())
		}
		// Preserve errors for fields introduced by newer producers.
		if definition == nil {
			continue
		}
		if gpu.Has(definition) {
			return fmt.Errorf("source index %d has both a value and an error for GPU field %q", entry.GetSourceIndex(), field)
		}
	}
	return nil
}

// validate verifies that an observation contains exactly one
// non-nil outcome. Protobuf oneofs prevent two outcomes from being selected,
// but they do not require either outcome to be present.
func validate(current *Observation) error {
	if current == nil {
		return fmt.Errorf("observation is required")
	}
	if err := validateTarget(current.GetTarget()); err != nil {
		return fmt.Errorf("observation %q: %w", current.GetSignalId(), err)
	}
	start, end := current.GetSampleStartedAt(), current.GetSampleEndedAt()
	if (start == nil) != (end == nil) {
		return fmt.Errorf("observation %q has an incomplete sample interval", current.GetSignalId())
	}
	if start != nil {
		if err := start.CheckValid(); err != nil {
			return fmt.Errorf("observation %q sample start: %w", current.GetSignalId(), err)
		}
		if err := end.CheckValid(); err != nil {
			return fmt.Errorf("observation %q sample end: %w", current.GetSignalId(), err)
		}
		if !end.AsTime().After(start.AsTime()) {
			return fmt.Errorf("observation %q has a nonpositive sample interval", current.GetSignalId())
		}
	}
	switch outcome := current.GetOutcome().(type) {
	case *Observation_Value:
		if outcome == nil || outcome.Value == nil {
			return fmt.Errorf("observation %q has an empty value outcome", current.GetSignalId())
		}
	case *Observation_CollectionError:
		if outcome == nil || outcome.CollectionError == nil {
			return fmt.Errorf("observation %q has an empty collection-error outcome", current.GetSignalId())
		}
	case *Observation_NoValue:
		if outcome == nil || outcome.NoValue == nil {
			return fmt.Errorf("observation %q has an empty no-value outcome", current.GetSignalId())
		}
	default:
		return fmt.Errorf("observation %q has no outcome", current.GetSignalId())
	}
	return nil
}

func validateTarget(target *ObservationTarget) error {
	if target == nil {
		return nil
	}
	switch kind := target.GetKind().(type) {
	case *ObservationTarget_Node:
		if kind == nil || kind.Node == nil {
			return fmt.Errorf("target has an empty node payload")
		}
	case *ObservationTarget_GPU:
		if kind == nil || kind.Gpu == nil {
			return fmt.Errorf("target has an empty GPU payload")
		}
	case *ObservationTarget_NVLinkEndpoint:
		if kind == nil || kind.NvLinkEndpoint == nil || kind.NvLinkEndpoint.Gpu == nil {
			return fmt.Errorf("target has an incomplete NVLink endpoint payload")
		}
	case *ObservationTarget_GPUPair:
		if kind == nil || kind.GpuPair == nil || kind.GpuPair.SourceGpu == nil || kind.GpuPair.PeerGpu == nil {
			return fmt.Errorf("target has an incomplete GPU pair payload")
		}
	case *ObservationTarget_InfiniBandDevice:
		if kind == nil || kind.InfiniBandDevice == nil {
			return fmt.Errorf("target has an empty InfiniBand device payload")
		}
	case *ObservationTarget_InfiniBandPort:
		if kind == nil || kind.InfiniBandPort == nil {
			return fmt.Errorf("target has an empty InfiniBand port payload")
		}
		if kind.InfiniBandPort.Device == nil {
			return fmt.Errorf("target has an incomplete InfiniBand port payload")
		}
	case *ObservationTarget_KernelRecord:
		if kind == nil || kind.KernelRecord == nil {
			return fmt.Errorf("target has an empty kernel record payload")
		}
	case *ObservationTarget_Process:
		if kind == nil {
			return fmt.Errorf("target has an empty process payload")
		}
		return validateProcessRef(kind.Process)
	case *ObservationTarget_GPUProcess:
		if kind == nil {
			return fmt.Errorf("target has an incomplete GPU process payload")
		}
		return validateGPUProcessRef(kind.GpuProcess)
	case *ObservationTarget_BlockDevice:
		if kind == nil || kind.BlockDevice == nil {
			return fmt.Errorf("target has an empty block device payload")
		}
	case *ObservationTarget_Mount:
		if kind == nil {
			return fmt.Errorf("target has an empty mount payload")
		}
		return validateMountRef(kind.Mount)
	case *ObservationTarget_PCI:
		if kind == nil {
			return fmt.Errorf("target has an empty PCI payload")
		}
		return validatePCIRef(kind.Pci)
	case *ObservationTarget_FUSEConnection:
		if kind == nil {
			return fmt.Errorf("target has an empty FUSE connection payload")
		}
		return validateFUSEConnectionRef(kind.FuseConnection)
	case *ObservationTarget_PstoreRecord:
		if kind == nil {
			return fmt.Errorf("target has an empty pstore record payload")
		}
		return validatePstoreRecordRef(kind.PstoreRecord)
	default:
		return fmt.Errorf("target has no kind")
	}
	return nil
}

// NewNoValueObservation builds an observation for a successful collection
// that produced no value for the requested signal or target.
func NewNoValueObservation(
	signalID string,
	unit *string,
	source string,
	target *ObservationTarget,
	observedAt *timestamppb.Timestamp,
	noValue *NoValue,
) *Observation {
	if observedAt == nil {
		observedAt = timestamppb.Now()
	}
	ownedNoValue := &NoValue{}
	if noValue != nil {
		ownedNoValue = proto.Clone(noValue).(*NoValue)
	}
	return &Observation{
		SignalId:   signalID,
		Source:     source,
		Target:     target,
		ObservedAt: observedAt,
		Unit:       unit,
		Outcome: &Observation_NoValue{
			NoValue: ownedNoValue,
		},
	}
}

// NewErrorObservation builds an observation carrying a CollectionError for
// one failed signal: collection status never lives in a value. A context
// deadline is reported as a timeout, not described as cancellation. The unit
// is nil when the signal has no canonical unit.
func NewErrorObservation(signalID string, unit *string, source, operation string, err error) *Observation {
	category := CollectionErrorCategorySourceError
	if errors.Is(err, context.DeadlineExceeded) {
		category = CollectionErrorCategoryTimeout
	}
	return NewCollectionErrorObservation(
		signalID,
		unit,
		source,
		operation,
		nil,
		timestamppb.Now(),
		&CollectionError{
			Category: category,
			Detail:   err.Error(),
		},
	)
}

// NewCollectionErrorObservation builds an observation from a source error
// that has already been normalized. It preserves source-specific details such
// as native status codes while applying the common observation envelope. The
// The returned observation owns a clone of failure. This prevents defaulting
// source metadata, or later mutation by a caller, from changing another
// observation built from the same source failure.
func NewCollectionErrorObservation(
	signalID string,
	unit *string,
	source, operation string,
	target *ObservationTarget,
	observedAt *timestamppb.Timestamp,
	failure *CollectionError,
) *Observation {
	if observedAt == nil {
		observedAt = timestamppb.Now()
	}
	ownedFailure := &CollectionError{}
	if failure != nil {
		ownedFailure = proto.Clone(failure).(*CollectionError)
	}
	if ownedFailure.Source == "" {
		ownedFailure.Source = source
	}
	if ownedFailure.Operation == "" {
		ownedFailure.Operation = operation
	}
	return &Observation{
		SignalId:   signalID,
		Source:     source,
		Target:     target,
		ObservedAt: observedAt,
		Unit:       unit,
		Outcome: &Observation_CollectionError{
			CollectionError: ownedFailure,
		},
	}
}
