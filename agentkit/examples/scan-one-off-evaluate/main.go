// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// scan-one-off-evaluate demonstrates the complete in-process path from
// shared collection through Health Validation catalog evaluation.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/dsx-ai-factory/health-validation/catalog"
	cataloggpu "github.com/dsx-ai-factory/health-validation/catalog/gpu"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	catalognvlink "github.com/dsx-ai-factory/health-validation/catalog/nvlink"
	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	dcgmsensor "github.com/dsx-ai-factory/health-validation/collect/sensors/dcgm"
	dcgmsource "github.com/dsx-ai-factory/health-validation/collect/source/dcgm"
	"github.com/dsx-ai-factory/health-validation/collect/source/process"
	"github.com/dsx-ai-factory/health-validation/toolkit/observations"
)

type config struct {
	architecture        string
	dcgmAddress         string
	dcgmGroupNamePrefix string
	dcgmMode            string
	nodeID              string
	timeout             time.Duration
	dcgmSocket          bool
}

type output struct {
	CollectionID string               `json:"collection_id"`
	Measurements []health.Measurement `json:"measurements"`
	Verdict      health.NodeVerdict   `json:"verdict"`
}

const (
	dcgmModeAuto       = "auto"
	dcgmModeStandalone = "standalone"
	dcgmModeEmbedded   = "embedded"
)

func main() {
	cfg, err := parseConfig(os.Args[1:], os.Stderr)
	if errors.Is(err, flag.ErrHelp) {
		os.Exit(0)
	}
	if err != nil {
		os.Exit(2)
	}
	if err := run(cfg); err != nil {
		log.Fatal(err)
	}
}

func parseConfig(args []string, output io.Writer) (config, error) {
	hostname, hostnameErr := os.Hostname()
	if hostnameErr != nil {
		hostname = ""
	}

	var cfg config
	flags := flag.NewFlagSet("scan-one-off-evaluate", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.StringVar(&cfg.architecture, "architecture", "H100", "GPU architecture used to resolve Health Validation checks and thresholds")
	flags.StringVar(&cfg.dcgmAddress, "dcgm-address", "localhost", "DCGM hostengine address or Unix socket path; setting it selects standalone mode")
	flags.StringVar(&cfg.dcgmGroupNamePrefix, "dcgm-group-name-prefix", "health-validation-evaluate", "prefix used to identify this process in DCGM group listings")
	flags.StringVar(&cfg.dcgmMode, "dcgm-mode", dcgmModeStandalone, "DCGM mode: standalone connects to a running hostengine; auto uses a hostengine on localhost and starts DCGM inside this process only when none answers and no other DCGM engine is visible; embedded starts DCGM inside this process unless another DCGM engine is visible")
	flags.BoolVar(&cfg.dcgmSocket, "dcgm-socket", false, "treat dcgm-address as a Unix socket path; setting it selects standalone mode")
	flags.StringVar(&cfg.nodeID, "node-id", hostname, "node identifier written to the verdict")
	flags.DurationVar(&cfg.timeout, "source-timeout", collect.DefaultCollectionTimeout, "maximum time for one source operation")
	if err := flags.Parse(args); err != nil {
		return config{}, err
	}

	var err error
	endpointSet := false
	flags.Visit(func(set *flag.Flag) {
		endpointSet = endpointSet || set.Name == "dcgm-address" || (set.Name == "dcgm-socket" && cfg.dcgmSocket)
	})
	cfg.dcgmMode = strings.ToLower(strings.TrimSpace(cfg.dcgmMode))
	switch cfg.dcgmMode {
	case dcgmModeAuto:
		// A configured hostengine is used without any embedded fallback.
		if endpointSet {
			cfg.dcgmMode = dcgmModeStandalone
		}
	case dcgmModeStandalone:
	case dcgmModeEmbedded:
		if endpointSet {
			err = errors.New("--dcgm-address and --dcgm-socket apply only to standalone mode")
		}
	default:
		err = fmt.Errorf("--dcgm-mode: unknown mode %q (want auto, standalone or embedded)", cfg.dcgmMode)
	}
	// Parsing stops at the first non-flag, so every flag after it was ignored.
	if flags.NArg() != 0 {
		err = fmt.Errorf("unexpected argument %q", flags.Arg(0))
	}
	if err != nil {
		fmt.Fprintln(output, err)
		flags.Usage()
		return config{}, err
	}
	return cfg, nil
}

// hostPIDNamespace is the fixed inode of the initial PID namespace.
const hostPIDNamespace = "pid:[4026531836]"

// refuseEmbedded returns an error when starting embedded DCGM could put a
// second engine on the node: this process cannot see every host process, or a
// hostengine, dcgm-exporter (which embeds DCGM by default), or dcgmi (whose diag
// can embed DCGM) is running. An embedded engine in any other process stays
// invisible.
func refuseEmbedded(fsys fs.FS) error {
	namespace, err := fs.ReadLink(fsys, "proc/self/ns/pid")
	if err != nil {
		return fmt.Errorf("read the PID namespace: %w", err)
	}
	if namespace != hostPIDNamespace {
		return fmt.Errorf("running outside the host PID namespace (%s), so another DCGM engine would be invisible; pass --dcgm-address", namespace)
	}
	reader := process.New(fsys)
	if _, err = reader.Comm(1); err != nil {
		return fmt.Errorf("cannot see every process (PID 1: %w), so another DCGM engine could be hidden; run as root or pass --dcgm-address", err)
	}
	pids, err := reader.PIDs()
	if err != nil {
		return fmt.Errorf("list processes: %w", err)
	}
	for _, pid := range pids {
		comm, commErr := reader.Comm(pid)
		// ENOENT or ESRCH: the process exited after PIDs listed it. ErrMalformed:
		// a non-UTF-8 comm cannot match the names below.
		if errors.Is(commErr, fs.ErrNotExist) || errors.Is(commErr, syscall.ESRCH) || errors.Is(commErr, process.ErrMalformed) {
			continue
		}
		if commErr != nil {
			return fmt.Errorf("read process %d: %w", pid, commErr)
		}
		switch comm {
		case "nv-hostengine":
			return fmt.Errorf("nv-hostengine is running (pid %d); pass --dcgm-address with an address that reaches it", pid)
		case "dcgm-exporter", "dcgmi":
			return fmt.Errorf("%s is running (pid %d) and may run its own DCGM engine", comm, pid)
		}
	}
	return nil
}

// Tests replace these to check which constructor each mode uses.
var (
	newStandaloneSource = dcgmsource.NewStandaloneSource
	newEmbeddedSource   = dcgmsource.NewEmbeddedSource
	newAutoSource       = dcgmsource.NewAutoSource
)

func newDCGMSource(cfg config, fsys fs.FS) (*dcgmsource.Source, error) {
	switch cfg.dcgmMode {
	case dcgmModeAuto:
		return newAutoSource(dcgmsource.AutoOptions{
			BeforeEmbedded:  func(context.Context) error { return refuseEmbedded(fsys) },
			GroupNamePrefix: cfg.dcgmGroupNamePrefix,
		}), nil
	case dcgmModeEmbedded:
		if err := refuseEmbedded(fsys); err != nil {
			return nil, fmt.Errorf("refuse embedded DCGM: %w", err)
		}
		return newEmbeddedSource(dcgmsource.EmbeddedOptions{GroupNamePrefix: cfg.dcgmGroupNamePrefix}), nil
	}
	return newStandaloneSource(dcgmsource.StandaloneOptions{
		Address:         cfg.dcgmAddress,
		IsSocket:        cfg.dcgmSocket,
		GroupNamePrefix: cfg.dcgmGroupNamePrefix,
	}), nil
}

func run(cfg config) error {
	dcgmSource, err := newDCGMSource(cfg, os.DirFS("/"))
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := dcgmSource.Close(); closeErr != nil {
			log.Printf("close DCGM source: %v", closeErr)
		}
	}()

	sensor, err := dcgmsensor.New(dcgmSource, watchedSignals()...)
	if err != nil {
		return fmt.Errorf("initialize DCGM sensor: %w", err)
	}
	currentCycle, err := collect.NewCycleWithTimeout(uuid.NewString(), time.Now().UTC(), cfg.timeout)
	if err != nil {
		return fmt.Errorf("create collection cycle: %w", err)
	}
	batch, err := sensor.Measure(context.Background(), collect.WithCycle(currentCycle))
	if err != nil {
		return fmt.Errorf("collect observations: %w", err)
	}
	if validationErr := observation.ValidateBatch(batch); validationErr != nil {
		return fmt.Errorf("validate observation batch: %w", validationErr)
	}

	measurements := observations.ToMeasurements(batch.GetObservations())
	validationCatalog, err := newValidationCatalog(cfg.architecture)
	if err != nil {
		return err
	}
	verdict, err := validationCatalog.EvaluateAll(measurements...)
	if err != nil {
		return fmt.Errorf("evaluate measurements: %w", err)
	}
	verdict.NodeID = cfg.nodeID

	encoded, err := json.MarshalIndent(output{
		CollectionID: batch.GetCollectionId(),
		Measurements: measurements,
		Verdict:      verdict,
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal evaluation output: %w", err)
	}
	fmt.Println(string(encoded))
	return nil
}

// watchedSignals selects only the signals the registered checks require,
// which also fixes the DCGM field watch before its first lazy native read.
func watchedSignals() []string {
	return slices.Concat(
		observations.MappedSignalsFor(health.GPU),
		observations.MappedSignalsFor(health.NVLink),
	)
}

func newValidationCatalog(architecture string) (*catalog.Catalog, error) {
	validationCatalog := catalog.NewEmpty(
		catalog.WithArchitecture(architecture),
		catalog.WithExecutionContext(health.OperatorInfra),
	)
	for _, check := range []health.CheckDefinition{
		cataloggpu.GPUAveragePowerDraw(),
		cataloggpu.GPUPowerLimit(),
		cataloggpu.GPUPowerState(),
		cataloggpu.GPUSensorThrottle(),
		catalognvlink.NVLinkFabric(),
		catalognvlink.NVLinkFabricHealth(),
	} {
		if err := validationCatalog.Register(check); err != nil {
			return nil, fmt.Errorf("register check %q: %w", check.ID, err)
		}
	}
	return validationCatalog, nil
}
