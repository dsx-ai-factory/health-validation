// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package rdmatopo reads ACS and IOMMU diagnostics from rdma_topo check.
package rdmatopo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"slices"
	"strconv"
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
// A custom runner must also report incomplete PCI configuration access.
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
	var configFiles fs.FS
	if run == nil {
		path, err := exec.LookPath("rdma_topo")
		if err != nil {
			p.addError(fmt.Errorf("find rdma_topo: %w", err))
			return p.snapshot()
		}
		p.result.Evidence.Executable = clean(path)
		p.publish()
		if err := checkExecCapabilities(); err != nil {
			p.addError(err)
		}
		// Use the same device directory and environment as the child process.
		env := append(os.Environ(), "PYTHONUNBUFFERED=1")
		deviceDir := "/sys/bus/pci/devices"
		for _, entry := range env {
			if value, ok := strings.CutPrefix(entry, "RDMA_TOPO_DEVDIR="); ok {
				deviceDir = value
			}
		}
		configFiles = os.DirFS(deviceDir)
		run = func(ctx context.Context, stdout, stderr io.Writer) (int, error) {
			cmd := exec.CommandContext(ctx, path, "check")
			cmd.Stdout, cmd.Stderr = stdout, stderr
			// Python must flush findings before a later read can hang.
			cmd.Env = env
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
	if configFiles != nil && runErr == nil && ctx.Err() == nil {
		if err := checkPCIConfig(ctx, configFiles); err != nil {
			p.addError(err)
		}
	}
	p.finish(exitCode, errors.Join(runErr, ctx.Err()), stderr.String())
	return p.snapshot()
}

// Parent reads cannot establish child access if exec drops CAP_SYS_ADMIN.
func checkCapabilityInheritance(status string, rootPrivileges bool) error {
	caps := make(map[string]uint64, 4)
	for _, line := range strings.Split(status, "\n") {
		name, value, _ := strings.Cut(line, ":")
		if name != "CapEff" && name != "CapAmb" && name != "CapBnd" && name != "CapInh" {
			continue
		}
		bits, err := strconv.ParseUint(strings.TrimSpace(value), 16, 64)
		if err != nil {
			return fmt.Errorf("%w: parse %s: %w", ErrUnreadable, name, err)
		}
		caps[name] = bits
	}
	if _, ok := caps["CapEff"]; !ok {
		return fmt.Errorf("%w: process capabilities missing CapEff", ErrUnreadable)
	}
	inherited := caps["CapAmb"]
	if rootPrivileges {
		inherited |= caps["CapBnd"] | caps["CapInh"]
	}
	const capSysAdmin = 1 << 21
	if caps["CapEff"]&capSysAdmin != 0 && inherited&capSysAdmin == 0 {
		return fmt.Errorf("%w: CAP_SYS_ADMIN would not survive rdma_topo execution", ErrUnreadable)
	}
	return nil
}

// Linux limits unprivileged config reads to 64 bytes (128 for CardBus).
// Probe past both limits; kernel quirks can legitimately shorten larger reads.
func checkPCIConfig(ctx context.Context, files fs.FS) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	devices, err := fs.ReadDir(files, ".")
	if err != nil {
		return fmt.Errorf("%w: list PCI devices: %w", ErrUnreadable, err)
	}
	if len(devices) == 0 {
		return fmt.Errorf("%w: no PCI devices found", ErrUnreadable)
	}
	for _, device := range devices {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := path.Join(device.Name(), "config")
		if err := readPCIConfig(files, name); err != nil {
			return fmt.Errorf("%w: %s: %w", ErrUnreadable, name, err)
		}
	}
	return nil
}

func readPCIConfig(files fs.FS, name string) error {
	file, err := files.Open(name)
	if err != nil {
		return err
	}
	defer file.Close()
	const extendedHeaderEnd = 0x104
	config, err := io.ReadAll(io.LimitReader(file, extendedHeaderEnd))
	if err != nil {
		return err
	}
	if len(config) < 129 {
		return fmt.Errorf("read %d of 129 configuration bytes: %w", len(config), io.EOF)
	}
	if len(config) == extendedHeaderEnd || config[0x06]&0x10 == 0 {
		return nil
	}
	// Only PCIe devices require access to the extended capability header.
	pointer := 0x34
	if config[0x0e]&0x7f == 2 {
		pointer = 0x14 // CardBus capability list.
	}
	var visited [256]bool
	for offset := int(config[pointer] &^ 3); offset != 0; offset = int(config[offset+1] &^ 3) {
		if offset < 0x40 || offset+2 > len(config) || visited[offset] {
			return fmt.Errorf("invalid or incomplete PCI capability list at %#x", offset)
		}
		visited[offset] = true
		if config[offset] == 0x10 { // PCI Express capability.
			return fmt.Errorf("read %d of %d PCIe configuration bytes: %w", len(config), extendedHeaderEnd, io.EOF)
		}
	}
	return nil
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
