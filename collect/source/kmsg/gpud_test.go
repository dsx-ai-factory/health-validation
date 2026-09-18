// SPDX-License-Identifier: Apache-2.0
// Adapted from GPUd pkg/kmsg/watcher_test.go at 9606bb8f6813fdd944cc9a349566e35075917805 (Apache-2.0).

package kmsg

import (
	"bufio"
	"context"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGPUdFixtures(t *testing.T) {
	bootTime := time.Unix(0xb100, 0x5ea1).Round(time.Microsecond)
	for _, name := range []string{"testdata/kmsg.1.log", "testdata/kmsg.2.peermem.log"} {
		t.Run(name, func(t *testing.T) {
			f, err := os.Open(name)
			require.NoError(t, err)
			defer func() { _ = f.Close() }()
			scanner := bufio.NewScanner(f)
			var expected []Message
			var lines []string
			for scanner.Scan() {
				if scanner.Text() == "" {
					continue
				}
				msg, parseErr := Parse(bootTime, scanner.Text())
				require.NoError(t, parseErr)
				msg.BootID = "boot"
				expected = append(expected, msg)
				lines = append(lines, scanner.Text())
			}
			require.NoError(t, scanner.Err())
			require.NotEmpty(t, expected)
			index := 0
			actual, readErr := readRecords(context.Background(), bootTime, "boot", func(buf []byte) (int, error) {
				if index == len(lines) {
					return 0, syscall.EAGAIN
				}
				n := copy(buf, lines[index])
				index++
				return n, nil
			})
			require.NoError(t, readErr)
			assert.Equal(t, expected, actual)
		})
	}
}

func Test_Parse(t *testing.T) {
	bootTime := time.Unix(0xb100, 0x5ea1).Round(time.Microsecond)

	msg, err := Parse(bootTime, "6,2565,102258085667,-;docker0: port 2(vethc1bb733) entered blocking state")
	require.NoError(t, err)

	assert.Equal(t, msg.Body, "docker0: port 2(vethc1bb733) entered blocking state")
	assert.Equal(t, msg.Priority, 6)
	assert.Equal(t, msg.Sequence, 2565)
	assert.Equal(t, msg.Timestamp, (bootTime.Add(102258085667 * time.Microsecond)))
}

func Test_parseLineComprehensive(t *testing.T) {
	bootTime := time.Unix(1000, 0).Round(time.Microsecond)

	tests := []struct {
		expected    *Message
		name        string
		input       string
		errorMsg    string
		expectError bool
	}{
		{
			name:  "valid message with standard format",
			input: "6,2565,102258085667,-;docker0: port 2(vethc1bb733) entered blocking state",
			expected: &Message{
				Priority:  6,
				Sequence:  2565,
				Timestamp: (bootTime.Add(102258085667 * time.Microsecond)),
				Body:      "docker0: port 2(vethc1bb733) entered blocking state",
			},
			expectError: false,
		},
		{
			name:  "valid message with high priority",
			input: "0,1234,5000000,-;Critical kernel message",
			expected: &Message{
				Priority:  0,
				Sequence:  1234,
				Timestamp: (bootTime.Add(5000000 * time.Microsecond)),
				Body:      "Critical kernel message",
			},
			expectError: false,
		},
		{
			name:  "valid message with low priority",
			input: "7,9999,10000,-;Debug kernel message",
			expected: &Message{
				Priority:  7,
				Sequence:  9999,
				Timestamp: (bootTime.Add(10000 * time.Microsecond)),
				Body:      "Debug kernel message",
			},
			expectError: false,
		},
		{
			name:  "valid message with large sequence number",
			input: "3,2147483647,50000,-;Message with max int32 sequence",
			expected: &Message{
				Priority:  3,
				Sequence:  2147483647,
				Timestamp: (bootTime.Add(50000 * time.Microsecond)),
				Body:      "Message with max int32 sequence",
			},
			expectError: false,
		},
		{
			name:  "valid message with zero timestamp",
			input: "4,100,0,-;Message at boot time",
			expected: &Message{
				Priority:  4,
				Sequence:  100,
				Timestamp: (bootTime),
				Body:      "Message at boot time",
			},
			expectError: false,
		},
		{
			name:  "valid message with semicolons in message part",
			input: "3,123,5000,-;Message with; semicolons; in it",
			expected: &Message{
				Priority:  3,
				Sequence:  123,
				Timestamp: (bootTime.Add(5000 * time.Microsecond)),
				Body:      "Message with; semicolons; in it",
			},
			expectError: false,
		},
		{
			name:  "valid message with extra metadata fields",
			input: "2,456,7890,extra,fields,-;Message with extra metadata",
			expected: &Message{
				Priority:  2,
				Sequence:  456,
				Timestamp: (bootTime.Add(7890 * time.Microsecond)),
				Body:      "Message with extra metadata",
			},
			expectError: false,
		},
		{
			name:        "error - missing semicolon",
			input:       "6,2565,102258085667",
			expectError: true,
			errorMsg:    "must contain a ';'",
		},
		{
			name:        "error - insufficient metadata parts",
			input:       "6,2565;message",
			expectError: true,
			errorMsg:    "must contain at least 3 ',' separated pieces at the start",
		},
		{
			name:        "error - invalid priority",
			input:       "invalid,2565,102258085667,-;message",
			expectError: true,
			errorMsg:    "could not parse",
		},
		{
			name:        "error - invalid sequence number",
			input:       "6,invalid,102258085667,-;message",
			expectError: true,
			errorMsg:    "could not parse",
		},
		{
			name:        "error - invalid timestamp",
			input:       "6,2565,invalid,-;message",
			expectError: true,
			errorMsg:    "could not parse",
		},
		{
			name:        "error - empty input",
			input:       "",
			expectError: true,
			errorMsg:    "must contain a ';'",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			msg, err := Parse(bootTime, tc.input)

			if tc.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.errorMsg)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.expected.Priority, msg.Priority)
				assert.Equal(t, tc.expected.Sequence, msg.Sequence)
				assert.Equal(t, tc.expected.Timestamp, msg.Timestamp)
				assert.Equal(t, tc.expected.Body, msg.Body)
			}
		})
	}
}

func Test_parseLineWithDifferentBootTimes(t *testing.T) {
	// Test that the timestamp calculation correctly uses the bootTime
	input := "3,123,5000000,-;Test message"

	bootTimes := []time.Time{
		time.Unix(0, 0),
		time.Unix(1000000000, 0),
		time.Unix(1700000000, 0),
		time.Now().Add(-24 * time.Hour),
	}

	for _, bootTime := range bootTimes {
		t.Run(bootTime.String(), func(t *testing.T) {
			msg, err := Parse(bootTime, input)
			require.NoError(t, err)

			// The message timestamp should be bootTime + 5 seconds
			expectedTime := (bootTime.Add(5000000 * time.Microsecond))
			assert.Equal(t, expectedTime, msg.Timestamp)
		})
	}
}

func Test_parseLineEdgeCases(t *testing.T) {
	bootTime := time.Unix(1000, 0)

	// Test with very large timestamp value
	t.Run("very large timestamp", func(t *testing.T) {
		// Large but safe timestamp value that won't overflow
		input := "1,100,9223372036854,-;Large timestamp message"
		msg, err := Parse(bootTime, input)
		require.NoError(t, err)

		// This should be bootTime + a large duration
		expectedTime := (bootTime.Add(9223372036854 * time.Microsecond))
		assert.Equal(t, expectedTime, msg.Timestamp)
	})

	// Test with negative priority (should still parse, even if unusual)
	t.Run("negative priority", func(t *testing.T) {
		input := "-1,100,5000,-;Negative priority message"
		msg, err := Parse(bootTime, input)
		require.NoError(t, err)
		assert.Equal(t, -1, msg.Priority)
	})

	// Test with negative sequence number (should still parse, even if unusual)
	t.Run("negative sequence", func(t *testing.T) {
		input := "1,-100,5000,-;Negative sequence message"
		msg, err := Parse(bootTime, input)
		require.NoError(t, err)
		assert.Equal(t, -100, msg.Sequence)
	})

	// Test with empty message
	t.Run("empty message", func(t *testing.T) {
		input := "1,100,5000,-;"
		msg, err := Parse(bootTime, input)
		require.NoError(t, err)
		assert.Equal(t, "", msg.Body)
	})
}
