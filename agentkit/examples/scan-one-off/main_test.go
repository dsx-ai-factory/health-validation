// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"io/fs"
	"log"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dsx-ai-factory/health-validation/collect"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
	ibsensor "github.com/dsx-ai-factory/health-validation/collect/sensors/infiniband"
	kmsgsensor "github.com/dsx-ai-factory/health-validation/collect/sensors/kmsg"
	ibsignals "github.com/dsx-ai-factory/health-validation/collect/signals/infiniband"
	kmsgxid "github.com/dsx-ai-factory/health-validation/collect/signals/kmsg/xid"
	ibsource "github.com/dsx-ai-factory/health-validation/collect/source/infiniband"
	kmsgsource "github.com/dsx-ai-factory/health-validation/collect/source/kmsg"
)

func TestLogEvents(t *testing.T) {
	for _, tc := range []struct {
		readErr                            error
		name, body, state, linkLayer, want string
	}{
		{
			name:      "Xid matched by two regexes and a down port",
			body:      "NVRM: Xid (PCI:0018:01:00): 149, NETIR_LINK_EVT Fatal XC0 i0 Link 08 (0x004505c6 0x00000000)",
			state:     "1: DOWN",
			linkLayer: "InfiniBand",
			want:      "event=xid record=boot/42 xid=149\nevent=infiniband_port_down port=mlx5_0/1\n",
		},
		{name: "Ethernet down is not an IB event", state: "1: DOWN", linkLayer: "Ethernet"},
		{name: "unknown layer is not an IB event", state: "1: DOWN", linkLayer: "Unknown"},
		{name: "no events", linkLayer: "InfiniBand", body: "normal boot", state: "4: ACTIVE"},
		{name: "collection errors are not events", linkLayer: "InfiniBand", state: "malformed", readErr: fs.ErrPermission},
		{name: "unparseable Xid is not an event", linkLayer: "InfiniBand", body: "NVRM: Xid (PCI:0000:01:00): 99999999999999999999999999, overflow", state: "4: ACTIVE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now()
			cycle, err := collect.NewCycle("example", now)
			require.NoError(t, err)
			kernel, err := kmsgsensor.New(func(context.Context) ([]kmsgsource.Message, error) {
				return []kmsgsource.Message{{BootID: "boot", Timestamp: now, Sequence: 42, Body: tc.body}}, tc.readErr
			}, kmsgxid.DefaultPatterns())
			require.NoError(t, err)
			batch, err := kernel.Measure(context.Background(), collect.WithCycle(cycle))
			require.NoError(t, err)
			ib := ibsensor.New(ibsource.New(fstest.MapFS{"mlx5_0/ports/1/link_layer": {Data: []byte(tc.linkLayer)}, "mlx5_0/ports/1/state": {Data: []byte(tc.state)}}))
			ports, err := ib.Measure(context.Background(), collect.WithCycle(cycle), collect.WithSignalID(ibsignals.State.ID()))
			require.NoError(t, err)
			batch.Observations = append(batch.Observations, ports.Observations...)
			require.NoError(t, observation.ValidateBatch(batch))
			var output bytes.Buffer
			logEvents(log.New(&output, "", 0), batch)
			require.Equal(t, tc.want, output.String())
		})
	}
}
