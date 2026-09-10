//go:build linux && !android

package nvml

import (
	"strings"
	"testing"
)

func TestDecodeProcCmdline(t *testing.T) {
	if got, want := decodeProcCmdline([]byte("python\x00train.py\x00"), 250), "python train.py"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := decodeProcCmdline(nil, 250); got != "" {
		t.Errorf("nil = %q, want empty", got)
	}
	long := make([]byte, 300)
	for i := range long {
		long[i] = 'a'
	}
	got := decodeProcCmdline(long, 250)
	if len(got) != 250 {
		t.Errorf("len = %d, want 250", len(got))
	}
}

func TestFormatComputeProcessLine(t *testing.T) {
	if got, want := formatComputeProcessLine(7, ""), "pid=7"; got != want {
		t.Errorf("empty cmdline = %q, want %q", got, want)
	}
	if got, want := formatComputeProcessLine(1, "python -c a"), `pid=1 "python -c a"`; got != want {
		t.Errorf("plain cmdline = %q, want %q", got, want)
	}
	got := formatComputeProcessLine(2, "python -c a\nb")
	if strings.Contains(got, "\n") {
		t.Errorf("quoted cmdline still contains a newline: %q", got)
	}
	if want := `pid=2 "python -c a\nb"`; got != want {
		t.Errorf("newline cmdline = %q, want %q", got, want)
	}
}
