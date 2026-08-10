package toolkit

import "testing"

func TestParseSysfsListCount(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  int64
	}{
		{name: "single index", input: "0", want: 1},
		{name: "contiguous range", input: "0-143", want: 144},
		{name: "sparse ranges and indices", input: "0-3,8-11,14,17", want: 10},
		{name: "newline terminated", input: "0-3\n", want: 4},
		{name: "maximum index", input: "9223372036854775807", want: 1},
		{name: "range ending at maximum index", input: "9223372036854775806-9223372036854775807", want: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseSysfsListCount([]byte(tt.input))
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("count = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestParseSysfsListCountRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "empty", input: ""},
		{name: "whitespace", input: "\n"},
		{name: "empty first entry", input: ",0"},
		{name: "empty middle entry", input: "0,,2"},
		{name: "empty final entry", input: "0,"},
		{name: "entry whitespace", input: "0, 2"},
		{name: "negative index", input: "-1"},
		{name: "signed index", input: "+1"},
		{name: "nonnumeric index", input: "one"},
		{name: "incomplete range", input: "0-"},
		{name: "multiple separators", input: "0-1-2"},
		{name: "descending range", input: "2-1"},
		{name: "duplicate index", input: "0,0"},
		{name: "overlapping range", input: "0-2,2-3"},
		{name: "out of order", input: "2,0"},
		{name: "index overflow", input: "9223372036854775808"},
		{name: "range count overflow", input: "0-9223372036854775807"},
		{name: "accumulated count overflow", input: "0-9223372036854775806,9223372036854775807"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseSysfsListCount([]byte(tt.input))
			if err == nil {
				t.Fatalf("count = %d, want error", got)
			}
			if got != 0 {
				t.Errorf("count = %d on error, want 0", got)
			}
		})
	}
}
