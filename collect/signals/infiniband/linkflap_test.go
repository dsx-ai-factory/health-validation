// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package infiniband

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseLinkTransition(t *testing.T) {
	tests := []struct {
		message string
		reason  string
		want    LinkTransition
		match   bool
	}{
		{message: "mlx5_core 0000:5a:00.0 enp90s0np0: Link down", match: true, want: LinkTransition{Interface: "enp90s0np0", Device: "0000:5a:00.0", Down: true}},
		{message: "mlx5_core 0000:5a:00.0 enp90s0np0: Link up", match: true, want: LinkTransition{Interface: "enp90s0np0", Device: "0000:5a:00.0"}},
		{message: "MLX5_CORE 0000:5a:00.0 ib0: LINK UP", match: true, want: LinkTransition{Interface: "ib0", Device: "0000:5a:00.0"}},
		// netdev_reg_state's suffix is not part of the name.
		{message: "mlx5_core 0000:5a:00.0 enp90s0np0 (unregistering): Link down", match: true, want: LinkTransition{Interface: "enp90s0np0", Device: "0000:5a:00.0", Down: true}},
		// The device names the link when the driver logs no netdev.
		{message: "mlx5_core 0000:5a:00.0: Link down", match: true, want: LinkTransition{Interface: "0000:5a:00.0", Device: "0000:5a:00.0", Down: true}},
		{message: "mlx5_core.sf mlx5_core.sf.2 enp90s0f0s2: Link up", match: true, want: LinkTransition{Interface: "enp90s0f0s2", Device: "mlx5_core.sf.2"}},
		{message: "mlx5_core 0000:5a:00.0 a b: Link down", match: true, reason: ReasonMissingInterface},
		{message: "mlx5_core 0000:5a:00.0 en/p90: Link down", match: true, reason: ReasonInvalidInterface},
		{message: "mlx5_core 0000:5a:00.0 enp90s0np0enp90s0: Link down", match: true, reason: ReasonInvalidInterface},
		{message: "mlx5_core 0000:5a:00.0 en\x01p: Link down", match: true, reason: ReasonInvalidInterface},
		{message: "mlx5_core " + strings.Repeat("0", maxDeviceName+1) + ": Link down", match: true, reason: ReasonInvalidInterface},
		{message: "mlx5_core 0000:5a:00.0 enp90s0np0 Link down"},
		{message: "mlx5_core: Link up"},
		{message: "mlx5_core 0000:5a:00.0 enp90s0np0: Link upgrade complete"},
		{message: "mlx5_ib: mlx5_0 port 1 link up"},
		{message: "e1000e 0000:00:1f.6 eno1: NIC Link is Up 1000 Mbps"},
		// Another subsystem's line that mentions the driver is not one of its
		// transitions.
		{message: "python3[4242]: segfault mlx5_core 0000:5a:00.0 eth0: Link down"},
		{message: "IPv6: ADDRCONF(NETDEV_CHANGE): mlx5_core x: Link down"},
	}
	for _, test := range tests {
		t.Run(test.message, func(t *testing.T) {
			require.Equal(t, test.match, MatchLinkTransition(test.message))
			if !test.match {
				return
			}
			got, err := ParseLinkTransition(test.message)
			if test.reason == "" {
				require.NoError(t, err)
				require.Equal(t, test.want, got)
				return
			}
			var parseErr *LinkTransitionParseError
			require.True(t, errors.As(err, &parseErr), "err=%v", err)
			require.Equal(t, test.reason, parseErr.Reason)
			require.NotContains(t, parseErr.Error(), "enp90")
		})
	}
}

// TestLinkFlapSignalsDisjointFromPorts keeps the journal history signals
// apart from the sysfs port signals, so both sensors can feed one run.
func TestLinkFlapSignalsDisjointFromPorts(t *testing.T) {
	ports := All().IDs()
	for _, id := range LinkFlapSignals().IDs() {
		require.False(t, slices.Contains(ports, id), id)
	}
	require.Equal(t, LinkFlapScanSince, LinkFlapSignals()[0])
	require.Equal(t, LinkTransitionSignals(), LinkFlapSignals()[1:])
}
