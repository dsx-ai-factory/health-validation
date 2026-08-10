package toolkit

import (
	"fmt"
	"strconv"
	"strings"
)

const maxInt64 = int64(1<<63 - 1)

func parseSysfsListCount(data []byte) (int64, error) {
	value := strings.TrimSpace(string(data))
	if value == "" {
		return 0, fmt.Errorf("list is empty")
	}

	var count int64
	previousEnd := int64(-1)
	for index, entry := range strings.Split(value, ",") {
		if entry == "" {
			return 0, fmt.Errorf("entry %d is empty", index)
		}
		if strings.TrimSpace(entry) != entry {
			return 0, fmt.Errorf("entry %d %q contains whitespace", index, entry)
		}

		bounds := strings.Split(entry, "-")
		if len(bounds) > 2 {
			return 0, fmt.Errorf("entry %d %q is not an index or range", index, entry)
		}

		start, err := parseSysfsIndex(bounds[0])
		if err != nil {
			return 0, fmt.Errorf("entry %d %q: %w", index, entry, err)
		}
		end := start
		if len(bounds) == 2 {
			end, err = parseSysfsIndex(bounds[1])
			if err != nil {
				return 0, fmt.Errorf("entry %d %q: %w", index, entry, err)
			}
			if end < start {
				return 0, fmt.Errorf("entry %d %q has a descending range", index, entry)
			}
		}

		if start <= previousEnd {
			return 0, fmt.Errorf("entry %d %q overlaps or precedes an earlier entry", index, entry)
		}
		span := end - start
		if span == maxInt64 {
			return 0, fmt.Errorf("entry %d %q count overflows int64", index, entry)
		}
		entryCount := span + 1
		if count > maxInt64-entryCount {
			return 0, fmt.Errorf("list count overflows int64")
		}
		count += entryCount
		previousEnd = end
	}

	return count, nil
}

func parseSysfsIndex(value string) (int64, error) {
	if value == "" {
		return 0, fmt.Errorf("index is empty")
	}
	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return 0, fmt.Errorf("index %q is not a decimal integer", value)
		}
	}

	index, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse index %q: %w", value, err)
	}
	return index, nil
}
