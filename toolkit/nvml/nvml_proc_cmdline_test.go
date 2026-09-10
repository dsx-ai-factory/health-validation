//go:build linux && !android

package nvml

import "testing"

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
