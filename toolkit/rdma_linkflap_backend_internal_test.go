package toolkit

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

func sinceArg(args []string) string {
	for _, a := range args {
		if strings.HasPrefix(a, "--since=") {
			return a
		}
	}
	return ""
}

// TestRDMALinkFlapBackend_Window exercises the lookback + boot-offset window
// that bounds the journalctl read.
func TestRDMALinkFlapBackend_Window(t *testing.T) {
	fixedNow := time.Date(2026, 8, 13, 12, 0, 0, 0, time.Local)
	at := func(d time.Duration) string { return "--since=" + fixedNow.Add(-d).Format(journalSinceLayout) }

	tests := []struct {
		name        string
		uptimeErr   error
		wantSince   string
		lookback    time.Duration
		bootOffset  time.Duration
		uptime      time.Duration
		wantErrored bool
	}{
		{
			name:      "default lookback bounds to the last hour",
			lookback:  time.Hour, // bootOffset 0 -> uptime not read
			wantSince: at(time.Hour),
		},
		{
			name:     "boot offset shortens a recent-boot window",
			lookback: time.Hour, bootOffset: 10 * time.Minute, uptime: 15 * time.Minute,
			// effective start = boot + 10m = now - (15m - 10m) = now - 5m
			wantSince: at(5 * time.Minute),
		},
		{
			name:     "boot offset irrelevant on a long-lived boot",
			lookback: time.Hour, bootOffset: 10 * time.Minute, uptime: 5 * time.Hour,
			// 5h - 10m > 1h, so the lookback still wins
			wantSince: at(time.Hour),
		},
		{
			name:     "boot offset exceeding uptime excludes the whole window",
			lookback: time.Hour, bootOffset: 10 * time.Minute, uptime: 3 * time.Minute,
			// max(3m - 10m, 0) = 0 -> --since = now
			wantSince: at(0),
		},
		{
			name:     "uptime failure with a boot offset errors",
			lookback: time.Hour, bootOffset: 10 * time.Minute, uptimeErr: errors.New("no /proc/uptime"),
			wantErrored: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotArgs []string
			uptimeFn := func() (time.Duration, error) { return tt.uptime, tt.uptimeErr }
			if tt.bootOffset == 0 {
				uptimeFn = func() (time.Duration, error) {
					t.Fatal("uptime must not be read when bootOffset is 0")
					return 0, nil
				}
			}
			b := &rdmaLinkFlapBackend{
				run: func(_ context.Context, args ...string) ([]byte, error) {
					gotArgs = args
					return []byte(""), nil // success, no matches
				},
				now:        func() time.Time { return fixedNow },
				uptime:     uptimeFn,
				lookback:   tt.lookback,
				bootOffset: tt.bootOffset,
			}

			ms, err := b.Measure(context.Background())
			if err != nil {
				t.Fatalf("Measure: %v", err)
			}
			if len(ms) != 1 {
				t.Fatalf("got %d measurements, want 1", len(ms))
			}
			if errored := len(ms[0].Errors()) > 0; errored != tt.wantErrored {
				t.Fatalf("errored = %v, want %v (errors: %v)", errored, tt.wantErrored, ms[0].Errors())
			}
			if tt.wantErrored {
				if gotArgs != nil {
					t.Errorf("journalctl must not run when uptime fails; got %v", gotArgs)
				}
				return
			}
			if got := sinceArg(gotArgs); got != tt.wantSince {
				t.Errorf("--since = %q, want %q (all args: %v)", got, tt.wantSince, gotArgs)
			}
			if !slices.Contains(gotArgs, "--output=short-full") || !slices.Contains(gotArgs, "--grep="+linkFlapGrep) {
				t.Errorf("read is missing output/grep args: %v", gotArgs)
			}
		})
	}
}

// TestJournalctlRDMALinkFlap_Options confirms the production defaults mirror
// the source config (1h lookback, no offset) and that the options apply.
func TestJournalctlRDMALinkFlap_Options(t *testing.T) {
	b := JournalctlRDMALinkFlap().(*rdmaLinkFlapBackend)
	if b.lookback != time.Hour || b.bootOffset != 0 {
		t.Errorf("defaults: lookback=%v bootOffset=%v, want 1h / 0", b.lookback, b.bootOffset)
	}
	b2 := JournalctlRDMALinkFlap(
		WithLinkFlapLookback(30*time.Minute),
		WithLinkFlapBootOffset(90*time.Second),
	).(*rdmaLinkFlapBackend)
	if b2.lookback != 30*time.Minute || b2.bootOffset != 90*time.Second {
		t.Errorf("options not applied: lookback=%v bootOffset=%v", b2.lookback, b2.bootOffset)
	}
}

// A negative lookback or boot offset is a configuration error, not a value
// to hand to journalctl: Measure must fail before touching the journal.
func TestRDMALinkFlapBackend_InvalidOptions(t *testing.T) {
	tests := []struct {
		name string
		opt  RDMALinkFlapOption
	}{
		{"negative lookback", WithLinkFlapLookback(-time.Minute)},
		{"negative boot offset", WithLinkFlapBootOffset(-time.Second)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := JournalctlRDMALinkFlap(tt.opt).(*rdmaLinkFlapBackend)
			b.run = func(_ context.Context, args ...string) ([]byte, error) {
				t.Fatal("journalctl must not run with an invalid option")
				return nil, nil
			}

			ms, err := b.Measure(context.Background())
			if err != nil {
				t.Fatalf("Measure: %v", err)
			}
			if len(ms) != 1 {
				t.Fatalf("got %d measurements, want 1", len(ms))
			}
			if len(ms[0].Errors()) == 0 {
				t.Error("want an errored measurement for an invalid option")
			}
		})
	}
}
