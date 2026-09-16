package toolkit

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"

	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/catalog/infiniband"
)

// IBStatBackend is a single data source for the current state of every
// InfiniBand port on the host. The production backend shells out to ibstat;
// tests inject canned output via [MockIBStat]. It returns one
// [infiniband.PortMeasurement] per port, wrapped in [health.Measurement].
//
// Measure honors the caller's context: ibstat is a cancellable command, so
// Collect's per-sensor watchdog bounds it directly.
type IBStatBackend interface {
	Measure(ctx context.Context) ([]health.Measurement, error)
}

// IBStat returns a backend that collects per-port InfiniBand state by
// running ibstat (from infiniband-diags). It reads /sys/class/infiniband
// and needs no elevated privileges.
//
// Absence of InfiniBand is not a fault: ibstat not being installed, or
// reporting no channel adapters, yields no measurements at all. Any other
// failure (ibstat present but exiting non-zero) yields a single errored
// measurement, so a consuming check fails rather than the host being dropped
// from the verdict.
func IBStat() IBStatBackend {
	return &ibstatBackend{run: runIBStat}
}

// ibstatBackend reads port state from ibstat. run is a seam so tests can
// exercise parsing and error handling without the host tool.
type ibstatBackend struct {
	run runFunc
}

func (b *ibstatBackend) Measure(ctx context.Context) ([]health.Measurement, error) {
	out, err := b.run(ctx)
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			slog.Debug("ibstat not installed; InfiniBand port state was not collected")
			return nil, nil
		}
		return []health.Measurement{
			infiniband.ErroredPort("", 0, fmt.Errorf("ibstat: %w", err), infiniband.PortMeasurementSignals...),
		}, nil
	}
	return ibstatMeasurements(ParseIBStat(splitLines(out))), nil
}

// runIBStat executes ibstat and returns its stdout.
func runIBStat(ctx context.Context, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, "ibstat", args...).Output()
}

// ibstatMeasurements widens the parsed ports to the measurement interface,
// returning nil for a host with no InfiniBand ports.
func ibstatMeasurements(ports []infiniband.PortMeasurement) []health.Measurement {
	if len(ports) == 0 {
		return nil
	}
	measurements := make([]health.Measurement, 0, len(ports))
	for _, p := range ports {
		measurements = append(measurements, p)
	}
	return measurements
}

// MockIBStat returns a backend that parses the provided output lines with
// the real parser, as if ibstat had printed them. Pass no lines to simulate
// a host with no InfiniBand adapters.
//
//	sensor := toolkit.NewIBStatSensor(toolkit.MockIBStat(
//	    "CA 'mlx5_0'",
//	    "	Port 1:",
//	    "		State: Active",
//	    "		Physical state: LinkUp",
//	    "		Rate: 400",
//	    "		Link layer: InfiniBand",
//	))
func MockIBStat(lines ...string) IBStatBackend {
	return &mockIBStatBackend{lines: lines}
}

type mockIBStatBackend struct {
	lines []string
}

func (m *mockIBStatBackend) Measure(context.Context) ([]health.Measurement, error) {
	return ibstatMeasurements(ParseIBStat(m.lines)), nil
}
