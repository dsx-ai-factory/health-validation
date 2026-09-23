package app

import (
	"encoding/binary"
	"io"
	"net"
	"strings"
)

const journalSocket = "/run/systemd/journal/socket"

type journalField struct {
	name, value string
}

// journalDatagram encodes fields as one systemd-journald native-protocol
// entry. A value containing a newline cannot use the NAME=value form, so it is
// sent as NAME, a newline, the value's little-endian 64-bit length, the value,
// and a newline.
func journalDatagram(fields []journalField) []byte {
	var b []byte
	for _, f := range fields {
		b = append(b, f.name...)
		if strings.Contains(f.value, "\n") {
			b = append(b, '\n')
			b = binary.LittleEndian.AppendUint64(b, uint64(len(f.value)))
		} else {
			b = append(b, '=')
		}
		b = append(b, f.value...)
		b = append(b, '\n')
	}
	return b
}

// dialJournal connects to the journald socket at path. Each Write on the
// returned connection sends one journal entry.
func dialJournal(path string) (io.WriteCloser, error) {
	conn, err := net.DialUnix("unixgram", nil, &net.UnixAddr{Name: path, Net: "unixgram"})
	if err != nil {
		return nil, err
	}
	return conn, nil
}
