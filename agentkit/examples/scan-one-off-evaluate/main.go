// scan-one-off-evaluate demonstrates the complete in-process path from
// shared collection through Health Validation catalog evaluation.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/google/uuid"

	"github.com/dsx-ai-factory/health-validation/catalog"
	cataloggpu "github.com/dsx-ai-factory/health-validation/catalog/gpu"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	catalognvlink "github.com/dsx-ai-factory/health-validation/catalog/nvlink"
	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	dcgmsensor "github.com/dsx-ai-factory/health-validation/collect/sensors/dcgm"
	gpusignals "github.com/dsx-ai-factory/health-validation/collect/signals/gpu"
	dcgmsource "github.com/dsx-ai-factory/health-validation/collect/source/dcgm"
)

var requiredGPUSignals = []string{
	gpusignals.PowerDraw.ID(),
	gpusignals.PowerLimitRequested.ID(),
	gpusignals.PowerLimitEnforced.ID(),
	gpusignals.PerformanceState.ID(),
	gpusignals.HardwareSlowdownActive.ID(),
	gpusignals.HardwareThermalSlowdownActive.ID(),
	gpusignals.HardwarePowerBrakeSlowdownActive.ID(),
}

var requiredNVLinkSignals = []string{
	gpusignals.NVLinkFabricBandwidthDegraded.ID(),
	gpusignals.NVLinkFabricRouteRecoveryActive.ID(),
	gpusignals.NVLinkFabricRouteUnhealthy.ID(),
	gpusignals.NVLinkFabricAccessTimeoutRecovery.ID(),
	gpusignals.FabricManagerStatus.ID(),
}

type config struct {
	architecture        string
	dcgmAddress         string
	dcgmGroupNamePrefix string
	nodeID              string
	timeout             time.Duration
	dcgmSocket          bool
}

type output struct {
	CollectionID string               `json:"collection_id"`
	Measurements []health.Measurement `json:"measurements"`
	Verdict      health.NodeVerdict   `json:"verdict"`
}

func main() {
	if err := run(parseConfig()); err != nil {
		log.Fatal(err)
	}
}

func parseConfig() config {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = ""
	}

	var cfg config
	flag.StringVar(&cfg.architecture, "architecture", "H100", "GPU architecture used to resolve Health Validation checks and thresholds")
	flag.StringVar(&cfg.dcgmAddress, "dcgm-address", "localhost", "DCGM hostengine address or Unix socket path")
	flag.StringVar(&cfg.dcgmGroupNamePrefix, "dcgm-group-name-prefix", "health-validation-evaluate", "prefix used to identify this process in DCGM group listings")
	flag.BoolVar(&cfg.dcgmSocket, "dcgm-socket", false, "treat dcgm-address as a Unix socket path")
	flag.StringVar(&cfg.nodeID, "node-id", hostname, "node identifier written to the verdict")
	flag.DurationVar(&cfg.timeout, "source-timeout", 15*time.Second, "maximum time for one source operation")
	flag.Parse()
	return cfg
}

func run(cfg config) error {
	dcgmSource := dcgmsource.NewStandaloneSource(dcgmsource.StandaloneOptions{
		Address:         cfg.dcgmAddress,
		IsSocket:        cfg.dcgmSocket,
		GroupNamePrefix: cfg.dcgmGroupNamePrefix,
	})
	defer func() {
		if err := dcgmSource.Close(); err != nil {
			log.Printf("close DCGM source: %v", err)
		}
	}()

	// Selecting only the signals required by the registered checks also fixes
	// the DCGM field watch before its first lazy native read.
	signalIDs := append(append([]string{}, requiredGPUSignals...), requiredNVLinkSignals...)
	sensor, err := dcgmsensor.New(dcgmSource, signalIDs...)
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

	measurements := buildMeasurements(batch.GetObservations())
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
