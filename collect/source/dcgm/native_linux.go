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
	readFields func([]uint, []godcgm.Short) ([]godcgm.FieldValue_v2, error)
	gpuStatus  func(uint) godcgm.EntityStatus
	dcgmInit   func(sessionMode, ...string) (func(), error)
	// Embedded mode ignores options.Address and options.IsSocket.
	options StandaloneOptions
	mode    sessionMode
}

// metricWatchSequence keeps DCGM group names unique within the process.
var metricWatchSequence atomic.Uint64

func newNative(options StandaloneOptions, mode sessionMode) nativeAPI {
	return &nativeLibrary{
		options:    options,
		mode:       mode,
		readFields: readGPUFields,
		gpuStatus:  godcgm.GetGPUStatus,
		dcgmInit:   initDCGM,
	}
}

// Tests replace goDCGMInit to check the go-dcgm mode, whose type is unexported.
var goDCGMInit = godcgm.Init

func initDCGM(mode sessionMode, args ...string) (func(), error) {
	switch mode {
	case embeddedMode:
		return goDCGMInit(godcgm.Embedded)
	case standaloneMode:
		return goDCGMInit(godcgm.Standalone, args...)
	default:
		return nil, fmt.Errorf("unknown DCGM session mode %d", mode)
	}
}

func (library *nativeLibrary) Init() (func(), error) {
	var args []string
	if library.mode == standaloneMode {
		isSocket := "0"
		if library.options.IsSocket {
			isSocket = "1"
		}
		args = []string{library.options.Address, isSocket}
	}
	// go-dcgm already reference-counts Init and Shutdown. Keep its cleanup
	// function with this source instead of adding another counter.
	cleanup, err := library.dcgmInit(library.mode, args...)
	if err != nil {
		return nil, dcgmErrorFromError(err)
	}
	return cleanup, nil
}

func (*nativeLibrary) AllGPUIndices() ([]uint, error) {
	// No supported-only filter: retain GPUs that DCGM already marks Lost.
	gpuIndices, err := godcgm.GetEntityGroupEntities(godcgm.FE_GPU)
	if err != nil {
		return nil, dcgmErrorFromError(err)
	}
	return gpuIndices, nil
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

	return startMetricWatch(name+"-fields", fieldIDs, metricWatchOperations{
		create:  godcgm.FieldGroupCreate,
		watch:   godcgm.WatchFieldsWithGroup,
		unwatch: godcgm.UnwatchFields,
		destroy: godcgm.FieldGroupDestroy,
	})
}

type metricWatchOperations struct {
	create  func(string, []godcgm.Short) (godcgm.FieldHandle, error)
	watch   func(godcgm.FieldHandle, godcgm.GroupHandle) error
	unwatch func(godcgm.FieldHandle, godcgm.GroupHandle) error
	destroy func(godcgm.FieldHandle) error
}

func (ops metricWatchOperations) unwatchFields(fieldGroup godcgm.FieldHandle, gpuGroup godcgm.GroupHandle) error {
	if err := ops.unwatch(fieldGroup, gpuGroup); err != nil {
		var failure *godcgm.Error
		if errors.As(err, &failure) && failure.Code == godcgm.DCGM_ST_NOT_WATCHED {
			return nil
		}
		return fmt.Errorf("unwatch DCGM metric fields: %w", err)
	}
	return nil
}

func (ops metricWatchOperations) destroyFieldGroup(fieldGroup godcgm.FieldHandle) error {
	if err := ops.destroy(fieldGroup); err != nil {
		return fmt.Errorf("destroy DCGM metric field group: %w", err)
	}
	return nil
}

func startMetricWatch(name string, fieldIDs []godcgm.Short, ops metricWatchOperations) (func() error, error) {
	fieldGroup, err := ops.create(name, fieldIDs)
	if err != nil {
		return nil, dcgmErrorFromError(err)
	}

	// DCGM_GROUP_ALL_GPUS follows GPU attachment changes, matching the source's
	// per-collection enumeration. This group is scoped to GPU field metrics;
	// health and other entity types require separate purpose-specific groups.
	gpuGroup := godcgm.GroupAllGPUs()
	// A failed watch can still have installed part of its state. Retain its
	// cleanup before attempting setup, and do not destroy its group until
	// unwatching has succeeded.
	watch := &metricWatch{
		unwatch: func() error { return ops.unwatchFields(fieldGroup, gpuGroup) },
		destroy: func() error { return ops.destroyFieldGroup(fieldGroup) },
	}
	if err := ops.watch(fieldGroup, gpuGroup); err != nil {
		cleanupErr := watch.close()
		if cleanupErr != nil {
			return watch.close, errors.Join(dcgmErrorFromError(err), cleanupErr)
		}
		return nil, dcgmErrorFromError(err)
	}

	// DCGM owns the special all-GPU group; release only our watch and field group.
	return watch.close, nil
}

func (library *nativeLibrary) ReadGPUMetrics(
	gpuIndex uint,
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
	values, err := library.readMetrics(gpuIndex, fieldIDs)
	if err != nil {
		return nil, err
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
		if sentinel == sentinelNotSupported && mapping.unsupportedAsNoValue {
			return FieldValue{NoValue: &observation.NoValue{
				Reason:       observation.NoValueReasonNotSupported,
				SourceStatus: sentinel.sourceError(),
				Detail:       "DCGM field value is " + sentinel.String(),
			}}
		}
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
		transformed := mapping.transform(&decoded)
		transformed.ObservedAt = time.UnixMicro(value.TS).UTC()
		return transformed
	}
	return FieldValue{
		ObservedAt: time.UnixMicro(value.TS).UTC(),
		Value:      &decoded,
	}
}

// readMetrics reads one GPU's fields from the hostengine cache, or from the
// driver when LiveMetrics is set.
func (library *nativeLibrary) readMetrics(gpuIndex uint, fieldIDs []godcgm.Short) ([]godcgm.FieldValue_v1, error) {
	if !library.options.LiveMetrics {
		values, err := godcgm.GetLatestValuesForFields(gpuIndex, fieldIDs)
		if err != nil {
			return nil, dcgmErrorFromError(err)
		}
		return values, nil
	}
	live, err := library.readFields([]uint{gpuIndex}, fieldIDs)
	if err != nil {
		return nil, err
	}
	values := make([]godcgm.FieldValue_v1, 0, len(live))
	for _, value := range live {
		if value.EntityGroupId != godcgm.FE_GPU || value.EntityID != gpuIndex {
			continue
		}
		values = append(values, godcgm.FieldValue_v1{
			FieldID:   value.FieldID,
			FieldType: value.FieldType,
			Status:    value.Status,
			TS:        value.TS,
			Value:     value.Value,
		})
	}
	return values, nil
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
