package app

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestJournalDatagram(t *testing.T) {
	tests := []struct {
		name   string
		want   string
		fields []journalField
	}{
		{
			name:   "simple",
			fields: []journalField{{"MESSAGE", "hello"}, {"PRIORITY", "5"}},
			want:   "MESSAGE=hello\nPRIORITY=5\n",
		},
		{
			name:   "multiline value is length-framed",
			fields: []journalField{{"MESSAGE", "a\nb"}, {"PRIORITY", "5"}},
			want:   "MESSAGE\n\x03\x00\x00\x00\x00\x00\x00\x00a\nb\nPRIORITY=5\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(journalDatagram(tt.fields)); got != tt.want {
				t.Errorf("journalDatagram() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDialJournalMissingSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent")
	w, err := dialJournal(path)
	if err == nil {
		w.Close()
		t.Fatal("dialJournal() error = nil, want an error for a missing socket")
	}
	if w != nil {
		t.Errorf("dialJournal() writer = %#v, want nil on error", w)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("dialJournal() error = %q, want it to name %q", err, path)
	}
}
