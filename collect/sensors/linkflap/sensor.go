// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package linkflap is the journal-backed mlx5 RDMA link-transition sensor
// for one-shot collection. It reads the systemd journal for the current boot
// rather than /dev/kmsg: the kernel ring buffer evicts old records, so a kmsg
// snapshot cannot show that no link went down within the lookback window.
//
// It reports raw transitions only. Pairing a down with the next up and
// measuring the outage is the consumer's job, since it needs every
// transition of the window together.
package linkflap

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	ibsignals "github.com/dsx-ai-factory/health-validation/collect/signals/infiniband"
	"github.com/dsx-ai-factory/health-validation/collect/source/journal"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ID identifies the journal link-flap sensor; it is also the observation
// source.
const ID = "infiniband.journal"

// DefaultLookback bounds the scan so a flap from early in a long-lived boot
// does not fail the check for the rest of it.
const DefaultLookback = time.Hour

var (
	errInUse = errors.New("link-flap journal read already in progress")
	// errConfig marks an invalid option, reported as NOT_CONFIGURED.
	errConfig = errors.New("invalid link-flap sensor configuration")
)

// Option configures the sensor.
type Option func(*sensor)

// WithLookback bounds the scan to the last d (default [DefaultLookback]).
func WithLookback(d time.Duration) Option {
	return func(s *sensor) {
		if d < 0 {
			s.configErr = errors.Join(s.configErr, fmt.Errorf("%w: lookback must be non-negative, got %s", errConfig, d))
			return
		}
		s.lookback = d
	}
}

// WithBootOffset ignores transitions logged within d of boot, when a host's
// links are still settling. The window then starts no earlier than boot + d.
// Default 0.
func WithBootOffset(d time.Duration) Option {
	return func(s *sensor) {
		if d < 0 {
			s.configErr = errors.Join(s.configErr, fmt.Errorf("%w: boot offset must be non-negative, got %s", errConfig, d))
			return
		}
		s.bootOffset = d
	}
}

type sensor struct {
	configErr  error
	reader     *journal.Reader
	uptime     func() (time.Duration, error)
	lookback   time.Duration
	bootOffset time.Duration
	mu         sync.Mutex
}

// New returns the link-flap sensor reading through reader, or through
// journalctl on PATH when reader is nil.
func New(reader *journal.Reader, opts ...Option) collect.Sensor {
	if reader == nil {
		reader = journal.New(nil)
	}
	s := &sensor{reader: reader, uptime: func() (time.Duration, error) { return readUptime(os.ReadFile) }, lookback: DefaultLookback}
	for _, o := range opts {
		o(s)
	}
	return s
}

func (*sensor) ID() string               { return ID }
func (*sensor) Signals() collect.Signals { return ibsignals.LinkFlapSignals() }

// Measure reads the kernel journal from the window start and emits each
// transition's fields on its journal-entry
// target, a parse error on the target of a transition that does not parse,
// and the scan marker once the read succeeds. A failed read yields one
// untargeted failure per selected signal and no marker, so it cannot pass as
// a clean scan.
func (s *sensor) Measure(ctx context.Context, opts ...collect.OpOption) (*observation.ObservationBatch, error) {
	op := collect.NewOp(opts...)
	selected, err := op.Signals(s.Signals())
	if err != nil {
		return nil, fmt.Errorf("select link-flap signals: %w", err)
	}
	now := time.Now()
	if cycle := op.Cycle(); cycle != nil {
		now = cycle.StartedAt()
	}
	at := timestamppb.New(now)
	batch := &observation.ObservationBatch{CollectionId: op.Cycle().CollectionID()}
	if len(selected) == 0 {
		return batch, nil
	}
	if s.configErr != nil {
		batch.Observations = scanFailure(selected, at, "configure", s.configErr)
		return batch, nil
	}
	start, err := s.windowStart(now)
	if err != nil {
		batch.Observations = scanFailure(selected, at, "uptime", err)
		return batch, nil
	}
	if !s.mu.TryLock() {
		batch.Observations = scanFailure(selected, at, "ReadKernelBoot", errInUse)
		return batch, nil
	}
	defer s.mu.Unlock()

	scan := &scan{want: map[string]bool{}, at: at, start: start}
	for _, definition := range selected {
		scan.want[definition.ID()] = true
	}
	from := start.Unix()
	if err := s.reader.ReadKernelBootRange(ctx, &from, nil, scan.entry); err != nil {
		batch.Observations = scanFailure(selected, at, "ReadKernelBoot", err)
		return batch, nil
	}
	batch.Observations = scan.observations
	if scan.want[ibsignals.LinkFlapScanSince.ID()] {
		batch.Observations = append(batch.Observations,
			value(ibsignals.LinkFlapScanSince, observation.NodeTarget(""), at, intValue(start.UnixMicro())))
	}
	return batch, nil
}

// windowStart is the earliest transition time the scan reports: the lookback
// before now, moved forward to boot + offset when that is later. It is
// truncated to the second, the precision of journalctl --since, which the
// legacy reader passed it as.
func (s *sensor) windowStart(now time.Time) (time.Time, error) {
	since := s.lookback
	if s.bootOffset > 0 {
		up, err := s.uptime()
		if err != nil {
			return time.Time{}, fmt.Errorf("reading /proc/uptime for boot offset: %w", err)
		}
		if settled := up - s.bootOffset; settled < since {
			since = max(settled, 0)
		}
	}
	return now.Add(-since).Truncate(time.Second), nil
}

type scan struct {
	start        time.Time
	want         map[string]bool
	at           *timestamppb.Timestamp
	observations []*observation.Observation
	entries      int
}

func (s *scan) entry(e journal.Entry) error {
	s.entries++
	if !ibsignals.MatchLinkTransition(e.Message) {
		return nil
	}
	if e.BootID == "" || e.Cursor == "" {
		return fmt.Errorf("journal entry %d has no boot ID or cursor: %w", s.entries, journal.ErrMalformedEntry)
	}
	target := func() *observation.ObservationTarget { return observation.JournalEntryTarget(e.BootID, e.Cursor) }
	// Without a timestamp the entry can be neither placed in the window nor
	// timed, so it fails the scan's consumers rather than being dropped.
	if e.Realtime.IsZero() {
		return s.parseFailure(target(), "missing_timestamp", "the entry has no __REALTIME_TIMESTAMP")
	}
	// journalctl already bounds the read by --since; this keeps a reader
	// that does not from widening the window.
	if e.Realtime.Before(s.start) {
		return nil
	}
	transition, err := ibsignals.ParseLinkTransition(e.Message)
	if err != nil {
		var parseErr *ibsignals.LinkTransitionParseError
		if !errors.As(err, &parseErr) {
			return fmt.Errorf("entry %d: %w", s.entries, err)
		}
		return s.parseFailure(target(), parseErr.Reason, parseErr.Detail)
	}
	state := ibsignals.LinkStateUp
	if transition.Down {
		state = ibsignals.LinkStateDown
	}
	add := func(definition collect.Signal, v *observation.Value) {
		if s.want[definition.ID()] {
			s.observations = append(s.observations, value(definition, target(), s.at, v))
		}
	}
	add(ibsignals.LinkTransitionInterface, stringValue(transition.Interface))
	add(ibsignals.LinkTransitionState, stringValue(state))
	add(ibsignals.LinkTransitionRealtime, intValue(e.Realtime.UnixMicro()))
	return nil
}

// parseFailure records a parse error on the entry's interface. When the
// interface is not selected there is no signal to carry it, so the scan
// fails instead.
func (s *scan) parseFailure(target *observation.ObservationTarget, reason, detail string) error {
	signal := ibsignals.LinkTransitionInterface
	if !s.want[signal.ID()] {
		return fmt.Errorf("entry %d: %w", s.entries, &ibsignals.LinkTransitionParseError{Reason: reason, Detail: detail})
	}
	s.observations = append(s.observations, observation.NewCollectionErrorObservation(
		signal.ID(), signal.Unit(), ID, "parse", target, s.at, &observation.CollectionError{
			Category:    observation.CollectionErrorCategoryParseError,
			SourceError: reason,
			Detail:      fmt.Sprintf("entry %d: %s", s.entries, detail),
		}))
	return nil
}

func scanFailure(selected collect.Signals, at *timestamppb.Timestamp, operation string, err error) []*observation.Observation {
	out := make([]*observation.Observation, 0, len(selected))
	for _, definition := range selected {
		out = append(out, observation.NewCollectionErrorObservation(
			definition.ID(), definition.Unit(), ID, operation, nil, at, &observation.CollectionError{
				Category: category(err),
				Detail:   err.Error(),
			}))
	}
	return out
}

func category(err error) observation.CollectionErrorCategory {
	var parseErr *ibsignals.LinkTransitionParseError
	switch {
	case errors.Is(err, collect.ErrCollectionTimeout), errors.Is(err, context.DeadlineExceeded):
		return observation.CollectionErrorCategoryTimeout
	case errors.Is(err, errConfig):
		return observation.CollectionErrorCategoryNotConfigured
	case errors.Is(err, exec.ErrNotFound), errors.Is(err, journal.ErrNoEntries), errors.Is(err, context.Canceled),
		errors.Is(err, collect.ErrCollectionCanceled), errors.Is(err, errInUse):
		return observation.CollectionErrorCategoryUnavailable
	case errors.Is(err, fs.ErrPermission):
		return observation.CollectionErrorCategoryPermissionDenied
	case errors.Is(err, journal.ErrMalformedEntry), errors.As(err, &parseErr):
		return observation.CollectionErrorCategoryParseError
	default:
		return observation.CollectionErrorCategorySourceError
	}
}

// maxUptime bounds a plausible uptime; converting a larger value to a
// Duration would overflow.
const maxUptime = 100 * 365 * 24 * time.Hour

// readUptime returns the host's uptime from /proc/uptime. ParseFloat also
// accepts NaN, infinities, and negative numbers, none of which is an uptime.
func readUptime(readFile func(string) ([]byte, error)) (time.Duration, error) {
	data, err := readFile("/proc/uptime")
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return 0, errors.New("empty /proc/uptime")
	}
	secs, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, fmt.Errorf("parsing /proc/uptime: %w", err)
	}
	if math.IsNaN(secs) || secs < 0 || secs > maxUptime.Seconds() {
		return 0, fmt.Errorf("/proc/uptime reports an implausible uptime of %g seconds", secs)
	}
	return time.Duration(secs * float64(time.Second)), nil
}

func value(definition collect.Signal, target *observation.ObservationTarget, at *timestamppb.Timestamp, v *observation.Value) *observation.Observation {
	return &observation.Observation{
		SignalId:   definition.ID(),
		Source:     ID,
		Target:     target,
		ObservedAt: at,
		Unit:       definition.Unit(),
		Outcome:    &observation.Observation_Value{Value: v},
	}
}

func intValue(v int64) *observation.Value {
	return &observation.Value{Kind: &observation.Value_IntValue{IntValue: v}}
}

func stringValue(v string) *observation.Value {
	return &observation.Value{Kind: &observation.Value_StringValue{StringValue: v}}
}
