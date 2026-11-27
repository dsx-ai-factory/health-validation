// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package output makes text from health-validation safe to hand to a model: it strips
// control and format characters, caps sizes and says when it cut, fences untrusted text
// with a random marker, and replaces tenant-controlled text with facts the server builds.
package output

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Caps on what goes back to the caller.
const (
	MaxMessageBytes  = 300
	MaxSourceBytes   = 64
	MaxValueBytes    = 128
	MaxEntities      = 16
	MaxPIDs          = 16
	MaxStuckCalls    = 8
	MaxErrorBytes    = 300
	truncationSuffix = "…"
)

// Clean removes control characters (C0, C1, DEL), format characters (bidi overrides,
// zero-width) and line or paragraph separators, so text cannot drive a terminal or
// reorder what a reader sees. Newlines become spaces. It then cuts the text to limit
// bytes on a rune boundary and reports whether it cut.
func Clean(text string, limit int) (string, bool) {
	text = strings.ToValidUTF8(text, "")

	var out strings.Builder

	out.Grow(min(len(text), limit+len(truncationSuffix)))

	for _, r := range text {
		switch {
		case r == '\n', r == '\t':
			out.WriteRune(' ')
		case unicode.Is(unicode.Cc, r), unicode.Is(unicode.Cf, r),
			unicode.Is(unicode.Zl, r), unicode.Is(unicode.Zp, r),
			unicode.Is(unicode.Co, r), r == utf8.RuneError:
			continue
		default:
			out.WriteRune(r)
		}
	}

	return Cut(out.String(), limit)
}

// Cut keeps at most limit bytes of text, on a rune boundary, and reports whether it cut.
func Cut(text string, limit int) (string, bool) {
	if len(text) <= limit {
		return text, false
	}

	end := limit
	for end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}

	return text[:end] + truncationSuffix, true
}

// Fence wraps untrusted text in a marker chosen at random for one tool call, so the
// text cannot close the fence itself.
type Fence struct {
	marker string
}

// NewFence picks a new random marker.
func NewFence() (*Fence, error) {
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		return nil, fmt.Errorf("reading random bytes: %w", err)
	}

	return &Fence{marker: "untrusted-hv-text-" + hex.EncodeToString(random)}, nil
}

// Marker is the fence's name, given to the caller so it knows where data starts and ends.
func (f *Fence) Marker() string { return f.marker }

// Wrap cleans and caps text, then fences it. It reports whether the text was cut.
func (f *Fence) Wrap(text string, limit int) (string, bool) {
	cleaned, cut := Clean(text, limit)
	// The marker is random, so this only matters if the text guessed it.
	cleaned = strings.ReplaceAll(cleaned, f.marker, "[marker removed]")

	return "<" + f.marker + ">" + cleaned + "</" + f.marker + ">", cut
}

var (
	gpuUUID    = regexp.MustCompile(`^GPU-[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	pciAddress = regexp.MustCompile(`^[0-9a-fA-F]{4,8}:[0-9a-fA-F]{2}:[0-9a-fA-F]{2}(?:\.[0-7])?$`)
	// RDMA device names as the kernel drivers give them, then the port number.
	ibPort   = regexp.MustCompile(`^(?:mlx|ib|bnxt_re|rxe)[A-Za-z0-9_]{0,15}:[0-9]{1,3}$`)
	gpuIndex = regexp.MustCompile(`^[0-9]{1,3}$`)
	// hv writes one process per line, "pid=N" then the quoted command line, and
	// strconv.Quote escapes newlines inside it, so only a line start is trusted.
	pidField = regexp.MustCompile(`(?m)^pid=([0-9]{1,10})(?:\s|$)`)
)

// Entities keeps the entity names that look like a GPU UUID, a PCI address, an
// InfiniBand port or a GPU index, up to MaxEntities, and counts the rest. Anything else
// could be text a tenant wrote, so it is never returned.
func Entities(names []string) (kept []string, dropped int, cut bool) {
	for _, name := range names {
		if !gpuUUID.MatchString(name) && !pciAddress.MatchString(name) &&
			!ibPort.MatchString(name) && !gpuIndex.MatchString(name) {
			dropped++
			continue
		}

		if slices.Contains(kept, name) {
			continue
		}

		if len(kept) == MaxEntities {
			cut = true
			continue
		}

		kept = append(kept, name)
	}

	return kept, dropped, cut
}

// PIDs pulls the integer process IDs out of compute_process_cmdlines text, whose lines
// start with pid=N. The command lines themselves are the tenant's and are dropped.
func PIDs(text string) []int64 {
	var pids []int64

	for _, match := range pidField.FindAllStringSubmatch(text, -1) {
		pid, err := strconv.ParseInt(match[1], 10, 64)
		if err != nil || slices.Contains(pids, pid) {
			continue
		}

		if len(pids) == MaxPIDs {
			break
		}

		pids = append(pids, pid)
	}

	return pids
}

// ComputeAppsSentence is what the server says for a gpu_compute_apps finding, instead of
// the check's message, which carries the tenant's command lines.
func ComputeAppsSentence(count int64, pids []int64) string {
	text := fmt.Sprintf("%d compute process(es) still hold this GPU.", count)
	if count < 0 {
		text = "Compute processes still hold this GPU; the count could not be read."
	}

	if len(pids) == 0 {
		return text + " Command lines are withheld because the tenant controls them."
	}

	parts := make([]string, 0, len(pids))
	for _, pid := range pids {
		parts = append(parts, strconv.FormatInt(pid, 10))
	}

	return text + " PIDs: " + strings.Join(parts, ", ") +
		". Command lines are withheld because the tenant controls them."
}

// ComputeAppsUnreadSentence is what the server says for a gpu_compute_apps check that
// could not collect: nothing was observed, so it must not sound like live processes.
func ComputeAppsUnreadSentence() string {
	return "The compute process list could not be read, so no process was confirmed on this GPU. " +
		"Collect again before acting on processes."
}

// XidUnreadSentence is what the server says for gpu_xid_* checks that could not read
// the journal. others is how many more such checks this sentence stands for.
func XidUnreadSentence(checkID string, others int) string {
	subject := fmt.Sprintf("Check %s", checkID)
	if others > 0 {
		subject = fmt.Sprintf("Check %s and %d other gpu_xid_* checks", checkID, others)
	}

	return subject + " could not read the journal; no Xid was confirmed. Collect again before treating " +
		"this as an Xid fault."
}

// XidSentence is what the server says for a gpu_xid_* finding, instead of text that can
// hold a raw kernel log line and a process name the tenant chose.
func XidSentence(checkID, catalogMessage string) string {
	text := fmt.Sprintf("Check %s fired.", checkID)
	if catalogMessage != "" {
		text += " Catalog guidance: " + catalogMessage
	}

	return text + " Journal text and process names are withheld because a tenant can set them."
}
