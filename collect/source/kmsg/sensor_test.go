// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package kmsg_test

import (
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	kmsgsensor "github.com/dsx-ai-factory/health-validation/collect/sensors/kmsg"
	kmsgsignals "github.com/dsx-ai-factory/health-validation/collect/signals/kmsg"
	kmsgsource "github.com/dsx-ai-factory/health-validation/collect/source/kmsg"
)

func TestSharedStreamSensorOverrun(t *testing.T) {
	stream, send := kmsgsource.NewStreamForTest(t)
	var sensors []collect.Sensor
	for range 2 {
		sensor, err := kmsgsensor.New(stream.Read, map[string]string{"all": `(?s).*`})
		require.NoError(t, err)
		sensors = append(sensors, sensor)
	}
	measure := func(code string, sequences ...int64) {
		t.Helper()
		for _, sensor := range sensors {
			cycle, err := collect.NewCycle("stream", time.Now())
			require.NoError(t, err)
			batch, err := sensor.Measure(t.Context(), collect.WithCycle(cycle), collect.WithSignalID(kmsgsignals.Message.ID()))
			require.NoError(t, err)
			require.NoError(t, observation.ValidateBatch(batch))
			require.Len(t, batch.Observations, 1+len(sequences))
			failure := batch.Observations[0].GetCollectionError()
			require.NotNil(t, failure)
			assert.Equal(t, code, failure.SourceError)
			assert.Equal(t, observation.CollectionErrorCategorySourceError, failure.Category)
			for i, seq := range sequences {
				reading := batch.Observations[i+1]
				assert.Equal(t, seq, reading.GetTarget().GetKernelRecord().GetSequence())
				assert.NotNil(t, reading.GetValue())
			}
		}
	}

	send("6,1,100,-;before", nil)
	send("", syscall.EPIPE)
	measure("EPIPE", 1)
	send("6,3,300,-;after", nil)
	measure("EPIPE", 1, 3)
	// A terminal failure after loss must not retain the recoverable code.
	send("", syscall.EIO)
	measure("", 1, 3)
}
