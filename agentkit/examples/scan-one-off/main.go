// scan-one-off runs one collection cycle over the built-in sensors, prints
// the observation batch, and exits. It is the one-off counterpart of
// server-simple-collect: no server, no config file.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	dcgmsensor "github.com/dsx-ai-factory/health-validation/collect/sensors/dcgm"
	ibsensor "github.com/dsx-ai-factory/health-validation/collect/sensors/infiniband"
	kmsgsensor "github.com/dsx-ai-factory/health-validation/collect/sensors/kmsg"
	nvmlsensor "github.com/dsx-ai-factory/health-validation/collect/sensors/nvml"
	dcgmsource "github.com/dsx-ai-factory/health-validation/collect/source/dcgm"
	nvmlsource "github.com/dsx-ai-factory/health-validation/collect/source/nvml"
)

func main() {
	// Deferred cleanup runs only when run returns; log.Fatal alone would
	// call os.Exit and skip the defers.
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	// NVML source client: one lifecycle for the process; hosts without NVML
	// get the no-op instance and the sensor reports per-signal errors.
	nvmlInst, err := nvmlsource.New()
	if err != nil {
		return fmt.Errorf("init NVML: %w", err)
	}
	defer func() { _ = nvmlInst.Shutdown() }()

	// DCGM initializes lazily on the first collection. Creating the sensor now
	// registers every field before DCGM creates its metric watch.
	dcgmSource := dcgmsource.NewStandaloneSource(dcgmsource.StandaloneOptions{})
	defer func() { _ = dcgmSource.Close() }()
	dcgmSensor, err := dcgmsensor.New(dcgmSource)
	if err != nil {
		return fmt.Errorf("init DCGM sensor: %w", err)
	}

	kmsgSensor, err := kmsgsensor.New(nil)
	if err != nil {
		return fmt.Errorf("init kmsg sensor: %w", err)
	}

	currentCycle, err := collect.NewCycle(uuid.NewString(), time.Now().UTC())
	if err != nil {
		return fmt.Errorf("create collection cycle: %w", err)
	}
	batch := &observation.ObservationBatch{CollectionId: currentCycle.CollectionID()}
	for _, sensor := range []collect.Sensor{nvmlsensor.New(nvmlInst), dcgmSensor, ibsensor.New(nil), kmsgSensor} {
		measured, measureErr := sensor.Measure(context.Background(), collect.WithCycle(currentCycle))
		if measureErr != nil {
			return fmt.Errorf("measure sensor %q: %w", sensor.ID(), measureErr)
		}
		batch.Observations = append(batch.Observations, measured.GetObservations()...)
	}

	// The batch is the full result. Collection errors are observations, not
	// verdicts, so they print like values and do not change the exit code.
	if validationErr := observation.ValidateBatch(batch); validationErr != nil {
		return fmt.Errorf("validate observation batch: %w", validationErr)
	}
	out, err := protojson.MarshalOptions{Multiline: true, Indent: "  "}.Marshal(batch)
	if err != nil {
		return fmt.Errorf("marshal batch: %w", err)
	}
	fmt.Println(string(out))
	return nil
}
