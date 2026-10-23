// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"maps"
	"syscall"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/require"

	dcgmsensor "github.com/dsx-ai-factory/health-validation/collect/sensors/dcgm"
	dcgmsource "github.com/dsx-ai-factory/health-validation/collect/source/dcgm"
)

func TestParseConfig(t *testing.T) {
	defaults := config{
		architecture:        "H100",
		dcgmAddress:         "localhost",
		dcgmGroupNamePrefix: "health-validation-evaluate",
		dcgmMode:            dcgmModeStandalone,
		timeout:             60 * time.Second,
	}
	auto := defaults
	auto.dcgmMode = dcgmModeAuto
	embedded := defaults
	embedded.dcgmMode = dcgmModeEmbedded
	socket := defaults
	socket.dcgmMode = dcgmModeStandalone
	socket.dcgmAddress = "/run/nvidia-dcgm/socket"
	socket.dcgmSocket = true
	tcp := defaults
	tcp.dcgmMode = dcgmModeStandalone
	tcp.dcgmAddress = "10.0.0.1"
	configured := defaults
	configured.dcgmMode = dcgmModeStandalone
	configured.dcgmAddress = "10.0.0.1"

	tests := []struct {
		name       string
		wantOutput string
		args       []string
		want       config
		wantErr    bool
		wantHelp   bool
	}{
		{name: "defaults", want: defaults},
		{name: "auto", args: []string{"--dcgm-mode=auto"}, want: auto},
		{name: "embedded", args: []string{"--dcgm-mode=embedded"}, want: embedded},
		{name: "normalized mode", args: []string{"--dcgm-mode= Embedded "}, want: embedded},
		{name: "unknown mode", args: []string{"--dcgm-mode=hostengine"}, wantErr: true, wantOutput: `unknown mode "hostengine"`},
		{name: "embedded with address", args: []string{"--dcgm-mode=embedded", "--dcgm-address=10.0.0.1"}, wantErr: true, wantOutput: "apply only to standalone mode"},
		{name: "embedded with socket", args: []string{"--dcgm-mode=embedded", "--dcgm-socket"}, wantErr: true, wantOutput: "apply only to standalone mode"},
		{name: "normalized embedded with socket", args: []string{"--dcgm-mode=Embedded", "--dcgm-socket"}, wantErr: true, wantOutput: "apply only to standalone mode"},
		{name: "explicit standalone with address", args: []string{"--dcgm-mode=STANDALONE", "--dcgm-address=10.0.0.1"}, want: tcp},
		{name: "address", args: []string{"--dcgm-address=10.0.0.1"}, want: configured},
		{name: "explicit auto with address", args: []string{"--dcgm-mode=auto", "--dcgm-address=10.0.0.1"}, want: configured},
		{name: "standalone socket", args: []string{"--dcgm-address=/run/nvidia-dcgm/socket", "--dcgm-socket"}, want: socket},
		{name: "embedded with socket off", args: []string{"--dcgm-mode=embedded", "--dcgm-socket=false"}, want: embedded},
		{name: "socket path as argument", args: []string{"--dcgm-socket", "/run/nvidia-dcgm/socket"}, wantErr: true, wantOutput: `unexpected argument "/run/nvidia-dcgm/socket"`},
		{name: "flags after an argument", args: []string{"--dcgm-mode=embedded", "x", "--dcgm-address=10.0.0.1"}, wantErr: true, wantOutput: `unexpected argument "x"`},
		{name: "help", args: []string{"-h"}, wantErr: true, wantHelp: true, wantOutput: "Usage of scan-one-off-evaluate"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			got, err := parseConfig(test.args, &output)
			require.Equal(t, test.wantErr, err != nil, "error = %v", err)
			require.Equal(t, test.wantHelp, errors.Is(err, flag.ErrHelp))
			if test.wantErr {
				require.Contains(t, output.String(), test.wantOutput)
			} else {
				require.Empty(t, output.String())
			}
			if !test.wantErr {
				got.nodeID = ""
				require.Equal(t, test.want, got)
			}
		})
	}
}

// TestWiring covers the setup run does before its first DCGM read in each
// mode: the DCGM sensor validates the watched signals without starting DCGM.
func TestWiring(t *testing.T) {
	for _, mode := range []string{dcgmModeAuto, dcgmModeStandalone, dcgmModeEmbedded} {
		source, err := newDCGMSource(config{dcgmMode: mode}, hostProc(nil))
		require.NoError(t, err, mode)
		_, err = dcgmsensor.New(source, watchedSignals()...)
		require.NoError(t, err, mode)
		require.NoError(t, source.Close(), mode)
	}
	_, err := newValidationCatalog("H100")
	require.NoError(t, err)
}

func TestNewDCGMSourceSelectsConstructor(t *testing.T) {
	var called []string
	t.Cleanup(func() {
		newStandaloneSource = dcgmsource.NewStandaloneSource
		newEmbeddedSource = dcgmsource.NewEmbeddedSource
		newAutoSource = dcgmsource.NewAutoSource
	})
	newStandaloneSource = func(options dcgmsource.StandaloneOptions) *dcgmsource.Source {
		called = append(called, fmt.Sprintf("standalone %s socket=%t %s", options.Address, options.IsSocket, options.GroupNamePrefix))
		return dcgmsource.NewStandaloneSource(options)
	}
	newEmbeddedSource = func(options dcgmsource.EmbeddedOptions) *dcgmsource.Source {
		called = append(called, "embedded "+options.GroupNamePrefix)
		return dcgmsource.NewEmbeddedSource(options)
	}
	newAutoSource = func(options dcgmsource.AutoOptions) *dcgmsource.Source {
		called = append(called, fmt.Sprintf("auto %s hook=%t", options.GroupNamePrefix, options.BeforeEmbedded != nil))
		return dcgmsource.NewAutoSource(options)
	}

	for mode, want := range map[string]string{
		dcgmModeAuto:       "auto hv hook=true",
		dcgmModeEmbedded:   "embedded hv",
		dcgmModeStandalone: "standalone /run/x socket=true hv",
	} {
		called = nil
		_, err := newDCGMSource(config{dcgmMode: mode, dcgmAddress: "/run/x", dcgmSocket: true, dcgmGroupNamePrefix: "hv"}, hostProc(nil))
		require.NoError(t, err, mode)
		require.Equal(t, []string{want}, called, mode)
	}
}

func TestNewDCGMSourceChecksForAnotherEngine(t *testing.T) {
	t.Cleanup(func() {
		newEmbeddedSource = dcgmsource.NewEmbeddedSource
		newAutoSource = dcgmsource.NewAutoSource
	})
	exporter := hostProc(fstest.MapFS{"proc/13/comm": {Data: []byte("dcgm-exporter\n")}})

	newEmbeddedSource = func(dcgmsource.EmbeddedOptions) *dcgmsource.Source {
		t.Fatal("embedded source built while dcgm-exporter is running")
		return nil
	}
	_, err := newDCGMSource(config{dcgmMode: dcgmModeEmbedded}, exporter)
	require.ErrorContains(t, err, "refuse embedded DCGM: dcgm-exporter is running (pid 13)")

	var beforeEmbedded func(context.Context) error
	newAutoSource = func(options dcgmsource.AutoOptions) *dcgmsource.Source {
		beforeEmbedded = options.BeforeEmbedded
		return dcgmsource.NewAutoSource(options)
	}
	_, err = newDCGMSource(config{dcgmMode: dcgmModeAuto}, exporter)
	require.NoError(t, err)
	require.ErrorContains(t, beforeEmbedded(context.Background()), "dcgm-exporter is running (pid 13)")
}

type readErrorFS struct {
	err error
	fstest.MapFS
	path string
}

func (fsys readErrorFS) ReadFile(name string) ([]byte, error) {
	if name == fsys.path {
		return nil, fsys.err
	}
	return fsys.MapFS.ReadFile(name)
}

// hostProc returns a host-namespace /proc with PID 1 visible plus extra entries.
func hostProc(extra fstest.MapFS) fstest.MapFS {
	fsys := fstest.MapFS{
		"proc/self/ns/pid": {Mode: fs.ModeSymlink, Data: []byte(hostPIDNamespace)},
		"proc/1/comm":      {Data: []byte("systemd\n")},
	}
	maps.Copy(fsys, extra)
	return fsys
}

func TestRefuseEmbedded(t *testing.T) {
	tests := []struct {
		fsys    fs.FS
		name    string
		wantErr string
	}{
		{name: "host view, no other engine", fsys: hostProc(fstest.MapFS{"proc/7/comm": {Data: []byte("sshd\n")}})},
		{name: "nv-hostengine", fsys: hostProc(fstest.MapFS{"proc/12/comm": {Data: []byte("nv-hostengine\n")}}), wantErr: "nv-hostengine is running (pid 12); pass --dcgm-address with an address that reaches it"},
		{name: "dcgm-exporter", fsys: hostProc(fstest.MapFS{"proc/13/comm": {Data: []byte("dcgm-exporter\n")}}), wantErr: "dcgm-exporter is running (pid 13) and may run its own DCGM engine"},
		{name: "dcgmi", fsys: hostProc(fstest.MapFS{"proc/14/comm": {Data: []byte("dcgmi\n")}}), wantErr: "dcgmi is running (pid 14) and may run its own DCGM engine"},
		{name: "container PID namespace", fsys: fstest.MapFS{
			"proc/self/ns/pid": {Mode: fs.ModeSymlink, Data: []byte("pid:[4026532526]")},
			"proc/1/comm":      {Data: []byte("sh\n")},
		}, wantErr: "outside the host PID namespace"},
		{name: "namespace unreadable", fsys: fstest.MapFS{"proc/1/comm": {Data: []byte("systemd\n")}}, wantErr: "read the PID namespace"},
		{name: "hidepid hides PID 1", fsys: fstest.MapFS{
			"proc/self/ns/pid": {Mode: fs.ModeSymlink, Data: []byte(hostPIDNamespace)},
			"proc/9/comm":      {Data: []byte("sshd\n")},
		}, wantErr: "cannot see every process"},
		{name: "hidepid denies PID 1", fsys: readErrorFS{err: fs.ErrPermission, path: "proc/1/comm", MapFS: hostProc(nil)}, wantErr: "cannot see every process"},
		{name: "process exited during scan", fsys: hostProc(fstest.MapFS{"proc/9": {Mode: fs.ModeDir}})},
		{name: "process gone during read", fsys: readErrorFS{err: syscall.ESRCH, path: "proc/9/comm", MapFS: hostProc(fstest.MapFS{"proc/9/comm": {Data: []byte("x\n")}})}},
		{name: "malformed comm", fsys: hostProc(fstest.MapFS{"proc/9/comm": {Data: []byte("\xffab\n")}})},
		{name: "comm not readable", fsys: readErrorFS{err: fs.ErrPermission, path: "proc/9/comm", MapFS: hostProc(fstest.MapFS{"proc/9/comm": {Data: []byte("x\n")}})}, wantErr: "read process 9"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := refuseEmbedded(test.fsys)
			if test.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, test.wantErr)
		})
	}
}
