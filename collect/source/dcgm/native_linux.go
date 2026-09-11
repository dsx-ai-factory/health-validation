//go:build linux && cgo

// SPDX-FileCopyrightText: Copyright (c) 2024, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	godcgm "github.com/NVIDIA/go-dcgm/pkg/dcgm"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
)

type nativeLibrary struct {
	options StandaloneOptions
}

// metricWatchSequence keeps DCGM group names unique within the process.
var metricWatchSequence atomic.Uint64

func newNative(options StandaloneOptions) nativeAPI {
	return &nativeLibrary{options: options}
}

func (library *nativeLibrary) Init() (func(), error) {
	isSocket := "0"
	if library.options.IsSocket {
		isSocket = "1"
	}
	// go-dcgm already reference-counts Init and Shutdown. Keep its cleanup
	// function with this source instead of adding another counter.
	cleanup, err := godcgm.Init(godcgm.Standalone, library.options.Address, isSocket)
	if err != nil {
		return nil, dcgmErrorFromError(err)
	}
	return cleanup, nil
}

func (*nativeLibrary) SupportedDevices() ([]uint, error) {
	devices, err := godcgm.GetSupportedDevices()
	if err != nil {
		return nil, dcgmErrorFromError(err)
	}
	return devices, nil
}

func (*nativeLibrary) DeviceUUID(deviceID uint) (string, error) {
	device, err := godcgm.GetDeviceInfo(deviceID)
	if err != nil {
		return "", dcgmErrorFromError(err)
	}
	return device.UUID, nil
}

func (library *nativeLibrary) WatchMetrics(signalIDs []string) (func() error, error) {
	// One field group lets DCGM watch every configured metric together.
	mappings, err := fieldMappingsFor(signalIDs)
	if err != nil {
		return nil, err
	}
	fieldIDs := uniqueFieldIDs(mappings)

	watchID := metricWatchSequence.Add(1)
	name := metricWatchName(library.options.GroupNamePrefix, os.Getpid(), watchID)

	fieldGroup, err := godcgm.FieldGroupCreate(name+"-fields", fieldIDs)
	if err != nil {
		return nil, dcgmErrorFromError(err)
	}

	// DCGM_GROUP_ALL_GPUS follows GPU attachment changes, matching the source's
	// per-collection enumeration. This group is scoped to GPU field metrics;
	// health and other entity types require separate purpose-specific groups.
	deviceGroup := godcgm.GroupAllGPUs()
	if err := godcgm.WatchFieldsWithGroup(fieldGroup, deviceGroup); err != nil {
		return nil, errors.Join(
			dcgmErrorFromError(err),
			unwatchFields(fieldGroup, deviceGroup),
			destroyFieldGroup(fieldGroup),
		)
	}

	// DCGM owns the special all-GPU group; release only our watch and field group.
	return func() error {
		return errors.Join(
			unwatchFields(fieldGroup, deviceGroup),
			destroyFieldGroup(fieldGroup),
		)
	}, nil
}

func (*nativeLibrary) ReadMetrics(
	deviceID uint,
	signalIDs []string,
) (map[string]FieldValue, error) {
	// Validate and resolve signal IDs before making the read call.
	mappings, err := fieldMappingsFor(signalIDs)
	if err != nil {
		return nil, err
	}
	fieldIDs := uniqueFieldIDs(mappings)
	mappingsByField := make(map[godcgm.Short][]fieldMapping, len(fieldIDs))
	for _, mapping := range mappings {
		mappingsByField[mapping.fieldID] = append(mappingsByField[mapping.fieldID], mapping)
	}

	// Read every selected field for this GPU in one DCGM call.
	values, err := godcgm.GetLatestValuesForFields(deviceID, fieldIDs)
	if err != nil {
		return nil, dcgmErrorFromError(err)
	}
	results := make(map[string]FieldValue, len(mappings))
	for _, value := range values {
		fieldMappings, expected := mappingsByField[value.FieldID]
		if !expected {
			continue
		}
		for _, mapping := range fieldMappings {
			results[mapping.signalID] = decodeMetricValue(mapping, value)
		}
	}
	for _, mapping := range mappings {
		if _, exists := results[mapping.signalID]; !exists {
			failure := fieldErrorFromStatus(
				godcgm.DCGM_ST_NO_DATA,
				fmt.Sprintf("DCGM returned no field for signal %q", mapping.signalID),
			)
			results[mapping.signalID] = FieldValue{CollectionError: &failure}
		}
	}
	return results, nil
}

// uniqueFieldIDs prevents duplicate watches and reads when several canonical
// signals are derived from one DCGM field.
func uniqueFieldIDs(mappings []fieldMapping) []godcgm.Short {
	fieldIDs := make([]godcgm.Short, 0, len(mappings))
	seen := make(map[godcgm.Short]struct{}, len(mappings))
	for _, mapping := range mappings {
		if _, exists := seen[mapping.fieldID]; exists {
			continue
		}
		seen[mapping.fieldID] = struct{}{}
		fieldIDs = append(fieldIDs, mapping.fieldID)
	}
	return fieldIDs
}

// fieldMappingsFor validates signal IDs and preserves their requested order.
func fieldMappingsFor(signalIDs []string) ([]fieldMapping, error) {
	if len(signalIDs) == 0 {
		return nil, fmt.Errorf("at least one DCGM metric signal is required")
	}
	mappings := make([]fieldMapping, 0, len(signalIDs))
	seen := make(map[string]struct{}, len(signalIDs))
	for _, signalID := range signalIDs {
		if _, duplicate := seen[signalID]; duplicate {
			return nil, fmt.Errorf("DCGM metric signal %q is requested more than once", signalID)
		}
		mapping, supported := fieldsBySignal[signalID]
		if !supported {
			return nil, &dcgmError{
				category: observation.CollectionErrorCategoryUnsupported,
				detail:   fmt.Sprintf("DCGM does not collect metric signal %q", signalID),
			}
		}
		seen[signalID] = struct{}{}
		mappings = append(mappings, mapping)
	}
	return mappings, nil
}

// decodeMetricValue checks field status and sentinels before reading the value.
func decodeMetricValue(mapping fieldMapping, value godcgm.FieldValue_v1) FieldValue {
	if value.Status != godcgm.DCGM_ST_OK {
		failure := fieldErrorFromStatus(
			value.Status,
			fmt.Sprintf("DCGM signal %q status %d", mapping.signalID, value.Status),
		)
		return FieldValue{CollectionError: &failure}
	}
	if sentinel, found := checkSentinel(value); found {
		failure := fieldErrorFromSentinel(sentinel)
		return FieldValue{CollectionError: &failure}
	}
	if value.TS <= 0 {
		failure := fieldErrorFromStatus(
			godcgm.DCGM_ST_NO_DATA,
			fmt.Sprintf("DCGM signal %q has no sample timestamp", mapping.signalID),
		)
		return FieldValue{CollectionError: &failure}
	}
	decoded, failure := decodeObservationValue(value)
	if failure != nil {
		return FieldValue{CollectionError: failure}
	}
	if mapping.transform != nil {
		decoded, failure = mapping.transform(&decoded)
		if failure != nil {
			return FieldValue{CollectionError: failure}
		}
	}
	return FieldValue{
		ObservedAt: time.UnixMicro(value.TS).UTC(),
		Value:      &decoded,
	}
}

// decodeObservationValue converts a supported DCGM field type into the common
// observation value type.
func decodeObservationValue(value godcgm.FieldValue_v1) (observation.Value, *observation.CollectionError) {
	switch value.FieldType {
	case godcgm.DCGM_FT_INT64:
		return observation.Value{
			Kind: &observation.Value_IntValue{IntValue: value.Int64()},
		}, nil
	case godcgm.DCGM_FT_DOUBLE:
		return observation.Value{
			Kind: &observation.Value_DoubleValue{DoubleValue: value.Float64()},
		}, nil
	case godcgm.DCGM_FT_STRING:
		return observation.Value{
			Kind: &observation.Value_StringValue{StringValue: value.String()},
		}, nil
	default:
		return observation.Value{}, &observation.CollectionError{
			Category: observation.CollectionErrorCategoryUnsupported,
			Detail:   fmt.Sprintf("DCGM field %d has unsupported type %d", value.FieldID, value.FieldType),
		}
	}
}

func unwatchFields(fieldGroup godcgm.FieldHandle, deviceGroup godcgm.GroupHandle) error {
	if err := godcgm.UnwatchFields(fieldGroup, deviceGroup); err != nil {
		return fmt.Errorf("unwatch DCGM metric fields: %w", err)
	}
	return nil
}

func destroyFieldGroup(fieldGroup godcgm.FieldHandle) error {
	if err := godcgm.FieldGroupDestroy(fieldGroup); err != nil {
		return fmt.Errorf("destroy DCGM metric field group: %w", err)
	}
	return nil
}
