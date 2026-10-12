package toolkit

import (
	"context"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/dsx-ai-factory/health-validation/catalog/xid"
)

// exitOne returns a real *exec.ExitError with code 1 and no output.
func exitOne(t *testing.T) error {
	t.Helper()
	err := exec.Command("false").Run()
	if err == nil {
		t.Fatal("expected `false` to exit non-zero")
	}
	return err
}

func exitOneWithStderr(t *testing.T) error {
	t.Helper()
	_, err := exec.Command("sh", "-c", "echo 'unsupported journalctl flag' >&2; exit 1").Output()
	if err == nil {
		t.Fatal("expected command to exit non-zero")
	}
	return err
}

// fakeJournal dispatches the scoped kernel read and the optional liveness
// probe (carries -n 1) to independently-canned results.
type fakeJournal struct {
	readErr  error
	probeErr error
	readOut  []byte
	probeOut []byte
}

func (f *fakeJournal) run(_ context.Context, args ...string) ([]byte, error) {
	if slices.Contains(args, "-n") {
		return f.probeOut, f.probeErr
	}
	return f.readOut, f.readErr
}

func TestJournalctlBackendMeasure(t *testing.T) {
	xidLine := "NVRM: Xid (PCI:0000:01:00): 79, pid=1234, name=python3"

	tests := []struct {
		name          string
		wantErrorText string
		fake          fakeJournal
		wantEvents    int64
		wantErrored   bool
		emptyRead     bool
	}{
		{
			name:       "xid lines found are parsed",
			fake:       fakeJournal{readOut: []byte("unrelated kernel line\n" + xidLine + "\n")},
			wantEvents: 1,
		},
		{
			name: "clean boot with a readable journal passes",
			fake: fakeJournal{readOut: []byte("some unrelated kernel line\n"), probeOut: []byte("some unrelated log line\n")},
		},
		{
			name:      "empty kernel journal with a readable journal passes",
			fake:      fakeJournal{probeOut: []byte("some unrelated log line\n")},
			emptyRead: true,
		},
		{
			name: "successful empty kernel read with a readable journal passes",
			fake: fakeJournal{probeOut: []byte("some unrelated log line\n")},
		},
		{
			name:          "exit one with stderr errors instead of passing",
			wantErrorText: "unsupported journalctl flag",
			fake:          fakeJournal{},
			wantErrored:   true,
		},
		{
			name:        "empty journal errors instead of passing",
			fake:        fakeJournal{},
			wantErrored: true,
			emptyRead:   true,
		},
		{
			name:        "successful empty read from an empty journal errors",
			fake:        fakeJournal{},
			wantErrored: true,
		},
		{
			name:        "probe read failure errors",
			fake:        fakeJournal{probeErr: exec.ErrNotFound},
			wantErrored: true,
			emptyRead:   true,
		},
		{
			name:        "kernel journal read failure errors",
			fake:        fakeJournal{readErr: exec.ErrNotFound},
			wantErrored: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.wantErrorText != "" {
				tt.fake.readErr = exitOneWithStderr(t)
			} else if tt.emptyRead {
				tt.fake.readErr = exitOne(t)
			}

			b := &journalctlBackend{run: tt.fake.run}
			ms, err := b.Measure(context.Background())
			if err != nil {
				t.Fatalf("Measure returned error: %v", err)
			}
			if len(ms) != 1 {
				t.Fatalf("expected 1 measurement, got %d", len(ms))
			}

			errs := ms[0].Errors()
			errored := len(errs) > 0
			if errored != tt.wantErrored {
				t.Fatalf("errored = %v, want %v (errors: %v)", errored, tt.wantErrored, errs)
			}
			if errored {
				if tt.wantErrorText != "" && !strings.Contains(errs[0].Err().Error(), tt.wantErrorText) {
					t.Errorf("error = %q, want it to contain %q", errs[0].Err(), tt.wantErrorText)
				}
				return
			}
			measurement, ok := ms[0].(xid.XidMeasurement)
			if !ok {
				t.Fatalf("measurement type = %T, want xid.XidMeasurement", ms[0])
			}
			if measurement.EventCount != tt.wantEvents {
				t.Errorf("event count = %d, want %d", measurement.EventCount, tt.wantEvents)
			}
		})
	}
}

// TestJournalReadableProbeArgs guards that the liveness probe reads an entry
// from the complete journal rather than applying a domain filter.
func TestJournalReadableProbeArgs(t *testing.T) {
	var gotArgs []string
	b := &journalctlBackend{run: func(_ context.Context, args ...string) ([]byte, error) {
		gotArgs = args
		return []byte("entry\n"), nil
	}}
	if err := journalReadable(context.Background(), b.run); err != nil {
		t.Fatalf("journalReadable: %v", err)
	}
	for _, a := range gotArgs {
		if strings.HasPrefix(a, "--grep") {
			t.Fatalf("liveness probe must not filter with --grep, got args: %v", gotArgs)
		}
	}
}

// TestXidJournalArgs guards that the Xid read is scoped to kernel-transport
// entries and does not rely on journalctl's optional --grep support.
func TestXidJournalArgs(t *testing.T) {
	var gotArgs []string
	b := &journalctlBackend{run: func(_ context.Context, args ...string) ([]byte, error) {
		gotArgs = args
		return []byte("NVRM: Xid (PCI:0000:01:00): 79, pid=1234, name=python3\n"), nil
	}}
	if _, err := b.Measure(context.Background()); err != nil {
		t.Fatalf("Measure: %v", err)
	}
	if !slices.Contains(gotArgs, "--dmesg") {
		t.Fatalf("Xid read must be scoped to kernel messages with --dmesg, got args: %v", gotArgs)
	}
	for _, arg := range gotArgs {
		if strings.HasPrefix(arg, "--grep") || arg == "-g" {
			t.Fatalf("Xid read must filter in Go, got journalctl args: %v", gotArgs)
		}
	}
}
