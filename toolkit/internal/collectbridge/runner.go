// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package collectbridge

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	"google.golang.org/protobuf/proto"
)

// Binding captures adapter metadata and callbacks for one collection run.
type Binding struct {
	Build    func([]*observation.Observation) []health.Measurement
	Errored  func(error) []health.Measurement
	Name     string
	Signals  []string
	Category health.Category
}

// Runner coordinates one collection cycle: Register every source, Collect
// once, then Observe the cached batches. Each canonical signal has one
// explicitly selected owner. Callers own source lifetime.
type Runner struct {
	cycle   *collect.Cycle
	owner   map[string]*collectEntry
	entries []*collectEntry
}

// partialSensor publishes independently owned snapshots for one invocation.
// Callbacks may arrive after Measure returns, so the bridge ignores late ones.
type partialSensor interface {
	MeasureWithPartial(context.Context, func(*observation.ObservationBatch), ...collect.OpOption) (*observation.ObservationBatch, error)
}

// collectEntry is one registered source and its collected batch.
type collectEntry struct {
	sensor  collect.Sensor
	batch   *observation.ObservationBatch
	id      string
	signals collect.Signals
}

// NewRunner coordinates sensors under cycle.
func NewRunner(cycle *collect.Cycle) *Runner {
	return &Runner{cycle: cycle, owner: make(map[string]*collectEntry)}
}

// Register assigns the sensor's declared signals matching the requested IDs.
// At least one match is required; overlapping producers are rejected.
func (r *Runner) Register(sensor collect.Sensor, signalIDs ...string) error {
	id := sensor.ID()
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("collect sensor with a nonempty ID is required")
	}
	for _, e := range r.entries {
		if e.id == id {
			return fmt.Errorf("collect sensor %q is already registered", id)
		}
	}
	requested := make(map[string]bool, len(signalIDs))
	for _, signalID := range signalIDs {
		requested[signalID] = true
	}
	declared := sensor.Signals()
	var selectedIDs []string
	for _, definition := range declared {
		if definition == nil {
			return fmt.Errorf("collect sensor %q declares a nil signal", id)
		}
		if requested[definition.ID()] {
			selectedIDs = append(selectedIDs, definition.ID())
		}
	}
	if len(selectedIDs) == 0 {
		return fmt.Errorf("sensor %q supplies no adapter-requested signals", id)
	}
	op := collect.NewOp(collect.WithSignalID(selectedIDs...))
	selected, err := op.Signals(declared)
	if err != nil {
		return fmt.Errorf("register collect sensor %q: %w", id, err)
	}
	owned := make(collect.Signals, 0, len(selected))
	for _, s := range selected {
		if err := validateCollectDefinition(s); err != nil {
			return err
		}
		if previous := r.owner[s.ID()]; previous != nil {
			return fmt.Errorf("signal %q is produced by both %q and %q; select one source explicitly", s.ID(), previous.id, id)
		}
		owned = append(owned, s)
	}
	e := &collectEntry{sensor: sensor, id: id, signals: owned}
	for _, s := range owned {
		r.owner[s.ID()] = e
	}
	r.entries = append(r.entries, e)
	return nil
}

// Owner returns the ID of the sensor registered for signalID, or "".
func (r *Runner) Owner(signalID string) string {
	if e := r.owner[signalID]; e != nil {
		return e.id
	}
	return ""
}

// Collect runs each registered sensor once, in registration order, and caches
// its batch. collect.Run bounds each sensor by the cycle timeout and recovers
// its panics. Sources implementing partialSensor can preserve earlier
// observations after failure; late results are discarded, never cached.
func (r *Runner) Collect() {
	for _, e := range r.entries {
		e.batch = r.measure(e)
	}
}

// Observe returns independently owned copies of the collected observations
// for the requested signals. Unknown signals produce NOT_CONFIGURED errors;
// successful empty collections remain empty so the adapter can apply its own
// absence semantics.
func (r *Runner) Observe(signalIDs ...string) []*observation.Observation {
	requested := make(map[string]bool, len(signalIDs))
	var out []*observation.Observation
	for _, id := range signalIDs {
		if requested[id] {
			continue
		}
		requested[id] = true
		if r.owner[id] == nil {
			s := collect.NewSignal(id, "")
			if rule, ok := canonical[id]; ok {
				s = rule.signal
			}
			failure := collectFailure(s, "toolkit", fmt.Errorf("no collect sensor is registered for signal %q", id))
			failure.GetCollectionError().Category = observation.CollectionErrorCategoryNotConfigured
			out = append(out, failure)
		}
	}
	for _, e := range r.entries {
		needed := false
		for _, s := range e.signals {
			if requested[s.ID()] {
				needed = true
				break
			}
		}
		if !needed {
			continue
		}
		for _, o := range e.batch.GetObservations() {
			if requested[o.GetSignalId()] {
				out = append(out, proto.Clone(o).(*observation.Observation))
			}
		}
	}
	return out
}

// measure runs e and validates its batch as a whole. A top-level source error
// adds a source-scoped failure for each owned signal that lacks one, replacing
// a source-scoped NO_TARGETS; per-target observations are kept, so valid
// partial data survives.
func (r *Runner) measure(e *collectEntry) *observation.ObservationBatch {
	var partial atomic.Pointer[observation.ObservationBatch]
	batch, sourceErr := collect.Run(context.Background(), r.cycle, e.id, func(ctx context.Context) (*observation.ObservationBatch, error) {
		opts := []collect.OpOption{collect.WithCycle(r.cycle), collect.WithSignalID(e.signals.IDs()...)}
		if sensor, ok := e.sensor.(partialSensor); ok {
			return sensor.MeasureWithPartial(ctx, func(batch *observation.ObservationBatch) {
				if batch != nil && ctx.Err() == nil {
					partial.Store(batch)
				}
			}, opts...)
		}
		return e.sensor.Measure(ctx, opts...)
	})
	if batch == nil && sourceErr != nil {
		batch = partial.Load()
	}
	if batch == nil {
		return r.failed(e, errors.Join(sourceErr, errors.New("observation batch is required")))
	}
	batch = proto.Clone(batch).(*observation.ObservationBatch)
	if err := validateCollectBatch(batch, r.cycle.CollectionID(), e.signals); err != nil {
		return r.failed(e, fmt.Errorf("collect sensor %q: %w", e.id, err))
	}
	if sourceErr != nil {
		for _, failure := range r.failed(e, sourceErr).GetObservations() {
			found := false
			for i, current := range batch.Observations {
				if current.GetSignalId() == failure.GetSignalId() && current.GetTarget() == nil {
					if prior := current.GetCollectionError(); prior == nil {
						batch.Observations[i] = failure
					} else if detail := failure.GetCollectionError().GetDetail(); !strings.Contains(prior.Detail, detail) {
						if prior.Detail != "" {
							prior.Detail += "; "
						}
						prior.Detail += detail
					}
					found = true
					break
				}
			}
			if !found {
				batch.Observations = append(batch.Observations, failure)
			}
		}
	}
	return batch
}

func (r *Runner) failed(e *collectEntry, err error) *observation.ObservationBatch {
	batch := &observation.ObservationBatch{CollectionId: r.cycle.CollectionID()}
	for _, s := range e.signals {
		batch.Observations = append(batch.Observations, collectFailure(s, e.id, err))
	}
	return batch
}

// collectFailure builds a source-scoped error observation. collect wraps its
// timeouts without the context error, so NewErrorObservation cannot classify
// them.
func collectFailure(s collect.Signal, source string, err error) *observation.Observation {
	o := observation.NewErrorObservation(s.ID(), s.Unit(), source, "Measure", err)
	if errors.Is(err, collect.ErrCollectionTimeout) {
		o.GetCollectionError().Category = observation.CollectionErrorCategoryTimeout
	}
	return o
}

// Sensor exposes a binding through the toolkit's collection interface. Its
// Measure reads batches cached by [Runner.Collect].
func (r *Runner) Sensor(binding Binding) *Sensor {
	binding.Signals = slices.Clone(binding.Signals)
	return &Sensor{runner: r, binding: binding}
}

// Sensor adapts cached observations to the toolkit collection interface.
type Sensor struct {
	runner  *Runner
	binding Binding
}

func (s *Sensor) Name() string { return s.binding.Name }

// Category is the catalog category of the bound adapter's measurements.
func (s *Sensor) Category() health.Category { return s.binding.Category }

func (s *Sensor) Measure(context.Context) ([]health.Measurement, error) {
	return s.binding.Build(s.runner.Observe(s.binding.Signals...)), nil
}

func (s *Sensor) Errored(err error) []health.Measurement { return s.binding.Errored(err) }
