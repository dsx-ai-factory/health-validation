// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package rdmatopo reads ACS and IOMMU diagnostics from rdma_topo check.
package rdmatopo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"
	"unicode"
)

// Finding is an explicit configuration failure reported by the tool.
type Finding struct {
	Diagnostic string   `json:"diagnostic"`
	Actual     string   `json:"actual,omitempty"`
	Expected   string   `json:"expected,omitempty"`
	BDFs       []string `json:"bdfs"`
}

// Evidence identifies the invocation that produced a result.
type Evidence struct {
	Host        string   `json:"host"`
	Executable  string   `json:"executable"`
	ToolVersion string   `json:"tool_version"`
	Mode        string   `json:"mode"`
	Invocation  []string `json:"invocation"`
	ExitCode    int      `json:"exit_code"`
}

// Result retains confirmed findings even when inspection fails.
type Result struct {
	Err                error
	ACSFailures        []Finding
	IOMMUGroupFailures []Finding
	Evidence           Evidence
}

// Runner executes one check. Nonnegative exit codes describe a finished
// process; errors describe failures to start or complete the invocation.
type Runner func(context.Context, io.Writer, io.Writer) (int, error)

type Reader struct {
	run Runner
}

// New uses rdma_topo on PATH when run is nil.
func New(run Runner) *Reader { return &Reader{run: run} }

// Read publishes independently owned partial results as diagnostics arrive.
// The callback must not block; it lets a collector retain findings on timeout.
func (r *Reader) Read(ctx context.Context, publish func(Result)) Result {
	host, _ := os.Hostname()
	evidence := Evidence{
		Host: clean(host), Executable: "rdma_topo", ToolVersion: "unknown",
		Invocation: []string{"rdma_topo", "check"}, Mode: "auto", ExitCode: -1,
	}
	p := &parser{ctx: ctx, result: Result{Evidence: evidence}, onResult: publish}
	p.publish()
	run := r.run
	if run == nil {
		path, err := exec.LookPath("rdma_topo")
		if err != nil {
			p.addError(fmt.Errorf("find rdma_topo: %w", err))
			return p.snapshot()
		}
		p.result.Evidence.Executable = clean(path)
		p.publish()
		run = func(ctx context.Context, stdout, stderr io.Writer) (int, error) {
			cmd := exec.CommandContext(ctx, path, "check")
			cmd.Stdout, cmd.Stderr = stdout, stderr
			// Python must flush findings before a later read can hang.
			cmd.Env = append(os.Environ(), "PYTHONUNBUFFERED=1")
			cmd.WaitDelay = 100 * time.Millisecond
			err := cmd.Run()
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) && exitErr.ExitCode() >= 0 {
				return exitErr.ExitCode(), nil
			}
			if err != nil {
				return -1, err
			}
			return 0, nil
		}
	}
	stderr := &prefixWriter{}
	exitCode, runErr := run(ctx, p, stderr)
	p.result.Evidence.ExitCode = exitCode
	p.finish(exitCode, errors.Join(runErr, ctx.Err()), stderr.String())
	return p.snapshot()
}

type prefixWriter struct {
	data []byte
}

func (w *prefixWriter) Write(p []byte) (int, error) {
	n := len(p)
	if left := 4096 - len(w.data); left > 0 {
		w.data = append(w.data, p[:min(left, n)]...)
	}
	return n, nil
}

func (w *prefixWriter) String() string { return strings.TrimSpace(clean(string(w.data))) }

func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\t' && r != '\n' {
			return '\uFFFD'
		}
		return r
	}, strings.ToValidUTF8(s, "\uFFFD"))
}

func cloneFindings(in []Finding) []Finding {
	out := slices.Clone(in)
	for i := range out {
		out[i].BDFs = slices.Clone(out[i].BDFs)
	}
	return out
}
