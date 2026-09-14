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
	Entity                  = healthv1.Entity
	Value                   = healthv1.Value
	CollectionError         = healthv1.CollectionError
	CollectionErrorCategory = healthv1.CollectionErrorCategory
	CollectionErrorReason   = healthv1.CollectionErrorReason
)

// Oneof wrappers for Observation.outcome and Value.kind.
type (
	Observation_Value           = healthv1.Observation_Value
	Observation_CollectionError = healthv1.Observation_CollectionError
	Value_IntValue              = healthv1.Value_IntValue
	Value_DoubleValue           = healthv1.Value_DoubleValue
	Value_StringValue           = healthv1.Value_StringValue
	Value_BoolValue             = healthv1.Value_BoolValue
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

	CollectionErrorReasonUnspecified              = healthv1.CollectionErrorReason_COLLECTION_ERROR_REASON_UNSPECIFIED
	CollectionErrorReasonValueReportedUnsupported = healthv1.CollectionErrorReason_COLLECTION_ERROR_REASON_VALUE_REPORTED_UNSUPPORTED
)

// EntityTypeGPU identifies a whole physical GPU. Its stable ID is the GPU
// UUID; source-local IDs are reserved for identity-collection errors.
const EntityTypeGPU = "gpu"

// GPUEntity returns the canonical identity envelope for a whole GPU.
func GPUEntity(id string) *Entity {
	return &Entity{Type: EntityTypeGPU, Id: id}
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
	return nil
}

// validate verifies that an observation contains exactly one
// non-nil outcome. Protobuf oneofs prevent two outcomes from being selected,
// but they do not require either outcome to be present.
func validate(current *Observation) error {
	if current == nil {
		return fmt.Errorf("observation is required")
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
	default:
		return fmt.Errorf("observation %q has no outcome", current.GetSignalId())
	}
	return nil
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
	entity *Entity,
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
		Entity:     entity,
		ObservedAt: observedAt,
		Unit:       unit,
		Outcome: &Observation_CollectionError{
			CollectionError: ownedFailure,
		},
	}
}
