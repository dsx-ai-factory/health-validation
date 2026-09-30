package collect

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/dsx-ai-factory/health-validation/collect/observation"
)

// Sensor collects one domain and returns normalized observations. It owns
// collection, parsing, signal and entity identity, value type and unit, and
// per-signal collection errors. It does not own scheduling, client lifetime,
// publication, component health, or catalog evaluation.
type Sensor interface {
	ID() string // "gpu.dcgm", "gpu.nvml", "xid.kmsg"
	// Signals returns the definitions this sensor can produce. Canonical signal
	// ownership is separate from source capability: overlapping sources return
	// the same definition rather than defining their own IDs or units.
	Signals() Signals

	// Measure returns one observation per (signal, entity). WithCycle provides
	// collection identity, start time, and collection timeout. WithSignalID
	// restricts the measurement to a signal subset (all signals when unset).
	// WithSignalID() selects no signals. WithGPUInventory requests a GPU
	// inventory snapshot from sensors that support it.
	// WithRefresh requests per-call device re-enumeration from sources that
	// support optional refresh. The DCGM sensor re-enumerates every cycle.
	Measure(ctx context.Context, opts ...OpOption) (*observation.ObservationBatch, error)
}

// NewCycle creates a collection cycle with the default collection timeout.
func NewCycle(collectionID string, startedAt time.Time) (*Cycle, error) {
	return newCycle(collectionID, startedAt, DefaultCollectionTimeout)
}

// NewCycleWithTimeout creates a collection cycle with the given collection
// timeout.
func NewCycleWithTimeout(collectionID string, startedAt time.Time, timeout time.Duration) (*Cycle, error) {
	return newCycle(collectionID, startedAt, timeout)
}

// Op holds options for one Measure call.
type Op struct {
	cycle *Cycle

	// nil means no selection: collect every signal the sensor produces.
	signalIDs map[string]struct{}

	refresh      bool
	gpuInventory bool
}

// OpOption configures a Measure call.
type OpOption func(*Op)

// NewOp applies the given options.
func NewOp(opts ...OpOption) *Op {
	op := &Op{}
	for _, opt := range opts {
		opt(op)
	}
	return op
}

// WithCycle attaches one collection cycle to a Measure call.
func WithCycle(cycle *Cycle) OpOption {
	return func(op *Op) {
		op.cycle = cycle
	}
}

// WithSignalID restricts the measurement to the given signal IDs; repeated
// calls union. When no WithSignalID option is given, the sensor collects
// everything it can produce. WithSignalID() selects no signals.
func WithSignalID(ids ...string) OpOption {
	return func(op *Op) {
		if op.signalIDs == nil {
			op.signalIDs = make(map[string]struct{}, len(ids))
		}
		for _, id := range ids {
			op.signalIDs[id] = struct{}{}
		}
	}
}

// WithGPUInventory requests a GPU inventory snapshot in the returned batch.
// Sensors that cannot produce one reject the request.
func WithGPUInventory() OpOption {
	return func(op *Op) {
		op.gpuInventory = true
	}
}

// WithRefresh requests device re-enumeration from sources that make refresh
// optional. Sources may define stronger behavior; DCGM always re-enumerates
// during each collection cycle.
//
// No-refresh is the default because GPU membership is static for the life
// of a driver session outside rare events (hot-plug, driver reset, MIG
// reconfiguration): per-cycle re-enumeration adds failure surface, not
// information. One unreadable device aborts NVML enumeration today (the
// Xid 79 case), which would otherwise suppress observations for every
// healthy GPU; per-device read failures already surface per signal.
func WithRefresh(refresh bool) OpOption {
	return func(op *Op) {
		op.refresh = refresh
	}
}

// Cycle returns the collection cycle attached to this Measure call.
func (op *Op) Cycle() *Cycle {
	return op.cycle
}

// Refresh reports whether this Measure call re-enumerates devices.
func (op *Op) Refresh() bool {
	return op.refresh
}

// GPUInventory reports whether this call requests a GPU inventory snapshot.
func (op *Op) GPUInventory() bool {
	return op.gpuInventory
}

// Signals returns the subset of the sensor's declared definitions selected by
// WithSignalID, or all of them when no selection was made. It rejects requested
// IDs that the sensor did not declare so configuration mistakes cannot produce
// a successful empty or partial collection.
func (op *Op) Signals(declared Signals) (Signals, error) {
	declaredByID := make(map[string]Signal, len(declared))
	unique := make(Signals, 0, len(declared))
	for _, definition := range declared {
		id := definition.ID()
		if id == "" {
			return nil, fmt.Errorf("sensor declared an empty signal ID")
		}
		if previous, duplicate := declaredByID[id]; duplicate {
			if previous != definition {
				return nil, fmt.Errorf("sensor declared conflicting definitions for signal %q", id)
			}
			continue
		}
		declaredByID[id] = definition
		unique = append(unique, definition)
	}

	if op.signalIDs == nil {
		return unique, nil
	}

	unknown := make([]string, 0)
	for id := range op.signalIDs {
		if _, ok := declaredByID[id]; !ok {
			unknown = append(unknown, id)
		}
	}
	if len(unknown) > 0 {
		slices.Sort(unknown)
		return nil, fmt.Errorf("sensor does not declare requested signal IDs %q", unknown)
	}

	selected := make(Signals, 0, len(unique))
	for _, definition := range unique {
		if _, ok := op.signalIDs[definition.ID()]; ok {
			selected = append(selected, definition)
		}
	}
	return selected, nil
}
