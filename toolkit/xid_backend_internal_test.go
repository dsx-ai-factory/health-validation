package toolkit

import (
	"context"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// exitOne returns a real *exec.ExitError with code 1 and no output, matching
// journalctl's "no matching entries" signal for the --grep path.
func exitOne(t *testing.T) error {
	t.Helper()
	err := exec.Command("false").Run()
	if err == nil {
		t.Fatal("expected `false` to exit non-zero")
	}
	return err
}

// fakeJournal dispatches the backend's two journalctl invocations — the
// Xid grep (carries --grep=Xid) and the liveness probe (carries -n 1) — to
// independently-canned results.
type fakeJournal struct {
	grepErr  error
	probeErr error
	grepOut  []byte
	probeOut []byte
}

func (f *fakeJournal) run(_ context.Context, args ...string) ([]byte, error) {
	if slices.Contains(args, "--grep=Xid") {
		return f.grepOut, f.grepErr
	}
	return f.probeOut, f.probeErr
}

func TestJournalctlBackendMeasure(t *testing.T) {
	xidLine := "NVRM: Xid (PCI:0000:01:00): 79, pid=1234, name=python3"

	tests := []struct {
		name        string
		fake        fakeJournal
		wantErrored bool
	}{
		{
			name: "xid lines found are parsed",
			fake: fakeJournal{grepOut: []byte(xidLine + "\n")},
		},
		{
			name: "clean boot with a readable journal passes",
			// grep finds nothing (exit 1, empty) but the probe confirms the
			// journal has entries: a genuine Xid-free boot.
			fake: fakeJournal{probeOut: []byte("some unrelated log line\n")},
			// grepErr set below (needs *testing.T for a real ExitError).
		},
		{
			name: "empty journal errors instead of passing",
			// grep finds nothing AND the probe finds nothing: the journal is
			// absent/unreadable, so a clean result must not be trusted.
			fake:        fakeJournal{},
			wantErrored: true,
		},
		{
			name: "probe read failure errors",
			// grep finds nothing and the probe itself fails (e.g. journalctl
			// missing): unobservable, surface UNKNOWN.
			fake:        fakeJournal{probeErr: exec.ErrNotFound},
			wantErrored: true,
		},
		{
			name: "non-empty-match grep error errors",
			// A grep failure that is not the exit-1/empty signal (here a
			// generic exec error) is a hard read failure.
			fake:        fakeJournal{grepErr: exec.ErrNotFound},
			wantErrored: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Cases that rely on the exit-1/empty "no matches" signal need a
			// real *exec.ExitError; set it here where t is available.
			if tt.fake.grepErr == nil && len(tt.fake.grepOut) == 0 {
				tt.fake.grepErr = exitOne(t)
			}

			b := &journalctlBackend{run: tt.fake.run}
			ms, err := b.Measure(context.Background())
			if err != nil {
				t.Fatalf("Measure returned error: %v", err)
			}
			if len(ms) != 1 {
				t.Fatalf("expected 1 measurement, got %d", len(ms))
			}

			errored := len(ms[0].Errors()) > 0
			if errored != tt.wantErrored {
				t.Fatalf("errored = %v, want %v (errors: %v)", errored, tt.wantErrored, ms[0].Errors())
			}
		})
	}
}

// TestJournalReadableProbeArgs guards that the liveness probe does not carry
// --grep (it must count entries, not match Xid) — a regression here would make
// the probe as blind as the grep it validates.
func TestJournalReadableProbeArgs(t *testing.T) {
	var gotArgs []string
	b := &journalctlBackend{run: func(_ context.Context, args ...string) ([]byte, error) {
		gotArgs = args
		return []byte("entry\n"), nil
	}}
	if err := b.journalReadable(context.Background()); err != nil {
		t.Fatalf("journalReadable: %v", err)
	}
	for _, a := range gotArgs {
		if strings.HasPrefix(a, "--grep") {
			t.Fatalf("liveness probe must not filter with --grep, got args: %v", gotArgs)
		}
	}
}
