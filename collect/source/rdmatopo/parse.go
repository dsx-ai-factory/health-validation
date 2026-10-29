// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package rdmatopo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

const (
	maxOutput = 1 << 20
	maxLine   = 16 << 10
	bdf       = `[0-9a-fA-F]{4}:[0-9a-fA-F]{2}:[01][0-9a-fA-F]\.[0-7]`
	device    = `[a-zA-Z0-9_]+ ` + bdf
	group     = `(?:[0-9]+|None)`
)

var (
	ErrMalformed   = errors.New("unrecognized or incomplete rdma_topo output")
	ErrUnsupported = errors.New("rdma_topo cannot assess this topology")
	ErrUnreadable  = errors.New("rdma_topo could not read PCI information")
	bdfPattern     = regexp.MustCompile(bdf)
	acsPattern     = regexp.MustCompile(`^ACS for (` + device + `) has (correct|incorrect) values ([01]{7,16}) (=|!=) ([01x]{7}),?(?: \((0x[0-9a-f]{1,4}) != (0x[0-9a-f]{1,4})\))?$`)
	dmaPattern     = regexp.MustCompile(`^Kernel iommu_group for DMA ` + bdf + ` and GPU ` + bdf + ` are (?:both ([0-9]+)|not equal (` + group + `) != (` + group + `))$`)
	pairPattern    = regexp.MustCompile(`^Kernel iommu_group for ` + device + ` and ` + device + ` are (?:(equal|not equal) (` + group + `) (==|!=) (` + group + `)|not set)$`)
	missingPattern = regexp.MustCompile(`^Kernel iommu_group is missing for ` + device + `$`)
	numaMissing    = regexp.MustCompile(`^Kernel iommu_group missing for devices on NUMA node [0-9]+: ` + bdf + `(?:, ` + bdf + `)*$`)
	numaShared     = regexp.MustCompile(`^Multiple devices share the same kernel iommu_group for NUMA node [0-9]+: (\[[0-9]+(?:, [0-9]+)*\])$`)
	numaOK         = regexp.MustCompile(`^All kernel iommu_groups for NUMA node [0-9]+ are unique$`)
	atsPattern     = regexp.MustCompile(`^ATS capability for ` + device + ` is (?:not )?available$`)
	unreadableACS  = regexp.MustCompile(`^Could not read ACS control register for ` + device + `$`)
)

type parser struct {
	ctx        context.Context
	onResult   func(Result)
	topology   string
	errors     []error
	pending    []byte
	result     Result
	pendingATS int
	total      int
	hasACS     bool
	hasIOMMU   bool
	hasFailure bool
	limited    bool
}

func (p *parser) Write(data []byte) (int, error) {
	n := len(data)
	if p.ctx.Err() != nil || p.limited {
		return n, nil
	}
	if len(data) > maxOutput-p.total {
		data = data[:maxOutput-p.total]
		p.limited = true
		p.addError(fmt.Errorf("%w: stdout exceeds %d bytes", ErrMalformed, maxOutput))
	}
	p.total += len(data)
	for len(data) > 0 {
		end := bytes.IndexByte(data, '\n')
		if end < 0 {
			p.pending = append(p.pending, data...)
			break
		}
		p.pending = append(p.pending, data[:end]...)
		if len(p.pending) > maxLine {
			p.addError(fmt.Errorf("%w: line exceeds %d bytes", ErrMalformed, maxLine))
		} else {
			p.line(string(p.pending))
		}
		p.pending = p.pending[:0]
		data = data[end+1:]
	}
	if len(p.pending) > maxLine {
		p.pending = nil
		p.limited = true
		p.addError(fmt.Errorf("%w: line exceeds %d bytes", ErrMalformed, maxLine))
	}
	return n, nil
}

func (p *parser) line(line string) {
	if p.ctx.Err() != nil {
		return
	}
	line = strings.TrimSuffix(line, "\r")
	status, msg, ok := strings.Cut(line, "\t")
	if !ok || (status != "OK" && status != "FAIL") || clean(line) != line {
		p.addError(diagnosticError(line))
		return
	}
	failed := status == "FAIL"
	if failed {
		p.hasFailure = true
	}
	switch msg {
	case "All ConnectX DMA functions have correct PCI topology", "All NIC/GPU complexes have correct PCI topology", "All NUMA-based complexes have correct PCI topology":
		if failed || p.topology != "" || p.hasACS || p.hasIOMMU {
			p.addError(fmt.Errorf("%w: invalid topology header", ErrMalformed))
		}
		p.topology = msg
		return
	}
	if p.topology == "" {
		p.addError(fmt.Errorf("%w: missing topology header", ErrMalformed))
	}
	finding := Finding{BDFs: bdfPattern.FindAllString(msg, -1), Diagnostic: line}
	for i := range finding.BDFs {
		finding.BDFs[i] = strings.ToLower(finding.BDFs[i])
	}
	if m := acsPattern.FindStringSubmatch(msg); m != nil {
		p.hasACS = true
		if p.hasIOMMU || p.pendingATS > 0 {
			p.addError(fmt.Errorf("%w: ACS diagnostic after complex assessment started", ErrMalformed))
		}
		if failed != (m[2] == "incorrect") || failed != (m[4] == "!=") || failed != (m[6] != "") {
			p.addError(diagnosticError(line))
			return
		}
		if failed {
			actual, _ := strconv.ParseUint(m[3], 2, 16)
			hexActual, _ := strconv.ParseUint(m[6][2:], 16, 16)
			hexExpected, _ := strconv.ParseUint(m[7][2:], 16, 16)
			if actual != hexActual || actual == hexExpected {
				p.addError(diagnosticError(line))
				return
			}
			finding.Actual, finding.Expected = m[3], m[5]
			p.result.ACSFailures = append(p.result.ACSFailures, finding)
			p.publish()
		}
		return
	}
	if unreadableACS.MatchString(msg) && failed {
		p.addError(fmt.Errorf("%w: %s", ErrUnreadable, msg))
		return
	}
	if atsPattern.MatchString(msg) {
		p.pendingATS++
		if strings.Contains(p.topology, "DMA functions") {
			p.addError(diagnosticError(line))
		}
		if failed {
			// ATS is outside the two requested finding categories.
			p.addError(fmt.Errorf("rdma_topo reported an ATS failure: %s", msg))
		}
		return
	}
	matched := false
	kind := ""
	switch {
	case dmaPattern.MatchString(msg):
		kind = "DMA functions"
		m := dmaPattern.FindStringSubmatch(msg)
		if failed == (m[1] != "") || (failed && m[2] == m[3] && m[2] != "None") {
			break
		}
		matched = true
		if failed {
			finding.Actual, finding.Expected = m[2]+" != "+m[3], "same non-missing IOMMU group"
		}
	case pairPattern.MatchString(msg):
		kind = "NIC/GPU"
		m := pairPattern.FindStringSubmatch(msg)
		if m[1] == "" && failed {
			break
		}
		if (m[1] == "equal" && m[3] != "==") || (m[1] == "not equal" && m[3] != "!=") {
			break
		}
		if m[1] != "" && ((m[1] == "equal") != (m[2] == m[4])) {
			break
		}
		matched = true
		if failed {
			finding.Actual = m[2] + " " + m[3] + " " + m[4]
			finding.Expected = "same IOMMU group"
			if m[1] == "equal" {
				finding.Expected = "different IOMMU groups"
			}
		}
	case missingPattern.MatchString(msg), numaMissing.MatchString(msg):
		kind = "NIC/GPU"
		if numaMissing.MatchString(msg) {
			kind = "NUMA-based"
		}
		matched = failed
		finding.Actual, finding.Expected = "missing", "assigned IOMMU group"
	case numaShared.MatchString(msg):
		kind = "NUMA-based"
		matched = failed
		finding.Actual, finding.Expected = numaShared.FindStringSubmatch(msg)[1], "unique IOMMU groups"
	case numaOK.MatchString(msg):
		kind = "NUMA-based"
		matched = !failed
	}
	if !matched {
		p.addError(diagnosticError(line))
		return
	}
	if !strings.Contains(p.topology, kind) {
		p.addError(fmt.Errorf("%w: diagnostic does not match topology", ErrMalformed))
	}
	if (kind == "NIC/GPU" && p.pendingATS != 1) || (kind == "NUMA-based" && p.pendingATS == 0) {
		p.addError(fmt.Errorf("%w: missing or unexpected ATS assessment before IOMMU assessment", ErrMalformed))
	}
	p.pendingATS = 0
	p.hasIOMMU = true
	if failed {
		p.result.IOMMUGroupFailures = append(p.result.IOMMUGroupFailures, finding)
		p.publish()
	}
}

func (p *parser) addError(err error) {
	if len(p.errors) < 16 {
		p.errors = append(p.errors, err)
		p.publish()
	}
}

func diagnosticError(line string) error {
	line = clean(line)
	if len(line) > 1024 {
		line = strings.ToValidUTF8(line[:1024], "") + "…"
	}
	kind := ErrMalformed
	switch {
	case strings.Contains(line, "No supported topology"), strings.Contains(line, "Unexpected topology structure"), strings.Contains(line, "Could not auto-detect virtualization"):
		kind = ErrUnsupported
	case strings.Contains(line, "Are you root?"), strings.Contains(line, "are you root?"), strings.Contains(line, "Permission denied"), strings.Contains(line, "Cannot read sysfs"), strings.Contains(line, "Could not read ACS"):
		kind = ErrUnreadable
		if strings.Contains(line, "root?") || strings.Contains(line, "Permission denied") {
			kind = fs.ErrPermission
		}
	}
	return fmt.Errorf("%w: %s", kind, line)
}

func (p *parser) finish(exitCode int, runErr error, stderr string) {
	if runErr != nil {
		p.addError(runErr)
	}
	if stderr != "" {
		p.addError(diagnosticError(stderr))
	}
	if runErr == nil {
		if len(p.pending) > 0 {
			p.addError(fmt.Errorf("%w: unterminated final line", ErrMalformed))
		}
		// NUMA topologies with generic root ports may have no ACS checks.
		if p.topology == "" || (!p.hasACS && p.topology != "All NUMA-based complexes have correct PCI topology") || !p.hasIOMMU {
			p.addError(fmt.Errorf("%w: topology, ACS, or IOMMU assessment missing", ErrMalformed))
		}
		if (exitCode != 0 && exitCode != 100) || (exitCode == 100 && !p.hasFailure) || (exitCode == 0 && p.hasFailure) {
			p.addError(fmt.Errorf("rdma_topo exit status %d is inconsistent with its diagnostics", exitCode))
		}
		if p.pendingATS > 0 {
			p.addError(fmt.Errorf("%w: IOMMU assessment missing after ATS assessment", ErrMalformed))
		}
	}
}

func (p *parser) snapshot() Result {
	r := p.result
	r.Err = errors.Join(p.errors...)
	r.ACSFailures = cloneFindings(r.ACSFailures)
	r.IOMMUGroupFailures = cloneFindings(r.IOMMUGroupFailures)
	r.Evidence.Invocation = slices.Clone(r.Evidence.Invocation)
	return r
}

func (p *parser) publish() {
	if p.onResult != nil {
		p.onResult(p.snapshot())
	}
}
