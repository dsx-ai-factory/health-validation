//go:build ignore

// protolayout preserves compact layouts required by the repository's analyzer.
package main

import (
	"fmt"
	"go/format"
	"os"
	"strings"
)

func main() {
	if len(os.Args) == 3 && os.Args[1] == "--" {
		os.Args = append(os.Args[:1], os.Args[2])
	}
	if len(os.Args) != 2 {
		panic("usage: protolayout generated.go")
	}
	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		panic(err)
	}
	text := string(data)
	trailing := map[string][]string{
		"GpuInventoryEntry": {"SourceIndex"}, "NvLinkEndpointRef": {"LinkIndex"},
		"InfiniBandPortRef": {"Port"}, "KernelRecordRef": {"Sequence"},
		"ProcessRef": {"Pid", "StartTimeTicks"}, "BlockDeviceRef": {"Major", "Minor"},
		"MountRef": {"MountId"}, "FuseConnectionRef": {"ConnectionId"},
		"CollectionError": {"Category"}, "NoValue": {"Reason"},
	}
	for name, fields := range trailing {
		opener := "type " + name + " struct {\n"
		start := strings.Index(text, opener)
		if start < 0 {
			panic("missing message " + name)
		}
		bodyStart := start + len(opener)
		end := strings.Index(text[bodyStart:], "\n}")
		if end < 0 {
			panic("unterminated message " + name)
		}
		end += bodyStart
		lines := strings.Split(text[bodyStart:end], "\n")
		blocks := make(map[string]string)
		var keep []string
		var comments []string
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				comments = append(comments, line)
				continue
			}
			words := strings.Fields(trimmed)
			matched := false
			if len(words) > 0 {
				for _, field := range fields {
					if words[0] == field {
						blocks[field] = strings.Join(append(comments, line), "\n")
						matched = true
						break
					}
				}
			}
			if !matched {
				keep = append(keep, comments...)
				keep = append(keep, line)
			}
			comments = nil
		}
		keep = append(keep, comments...)
		for _, field := range fields {
			block, ok := blocks[field]
			if !ok {
				panic(fmt.Sprintf("missing %s.%s", name, field))
			}
			keep = append(keep, block)
		}
		text = text[:bodyStart] + strings.Join(keep, "\n") + text[end:]
	}
	formatted, err := format.Source([]byte(text))
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile(os.Args[1], formatted, 0o644); err != nil {
		panic(err)
	}
}
