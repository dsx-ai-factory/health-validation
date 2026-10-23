//go:build linux && cgo

// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dcgm

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	godcgm "github.com/NVIDIA/go-dcgm/pkg/dcgm"
	"github.com/dsx-ai-factory/health-validation/collect/observation"
)

func TestNativeInitSelectsMode(t *testing.T) {
	tests := []struct {
		name     string
		wantArgs []string
		options  nativeOptions
		wantMode sessionMode
	}{
		{
			name:     "standalone tcp",
			wantArgs: []string{"10.0.0.1:5555", "0"},
			options:  nativeOptions{address: "10.0.0.1:5555", mode: standaloneMode},
			wantMode: standaloneMode,
		},
		{
			name:     "standalone socket",
			wantArgs: []string{"/run/nvidia-dcgm/socket", "1"},
			options:  nativeOptions{address: "/run/nvidia-dcgm/socket", isSocket: true, mode: standaloneMode},
			wantMode: standaloneMode,
		},
		{
			name:     "embedded",
			options:  nativeOptions{mode: embeddedMode},
			wantMode: embeddedMode,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var calls, cleanups int
			var gotMode sessionMode
			var gotArgs []string
			library := &nativeLibrary{
				options: test.options,
				dcgmInit: func(mode sessionMode, args ...string) (func(), error) {
					calls++
					gotMode = mode
					gotArgs = args
					return func() { cleanups++ }, nil
				},
			}

			cleanup, err := library.Init()
			if err != nil {
				t.Fatalf("Init() error = %v", err)
			}
			if calls != 1 || gotMode != test.wantMode || !slices.Equal(gotArgs, test.wantArgs) {
				t.Fatalf("dcgmInit calls = %d, mode = %v, args = %q; want 1, %v, %q",
					calls, gotMode, gotArgs, test.wantMode, test.wantArgs)
			}
			cleanup()
			if cleanups != 1 {
				t.Fatalf("cleanup calls = %d, want 1", cleanups)
			}
		})
	}
}

func TestNativeInitWrapsInitError(t *testing.T) {
	library := &nativeLibrary{
		options: nativeOptions{mode: embeddedMode},
		dcgmInit: func(sessionMode, ...string) (func(), error) {
			return nil, errors.New("libdcgm.so.4 not found")
		},
	}

	cleanup, err := library.Init()
	if cleanup != nil {
		t.Fatal("Init() returned a cleanup function on failure")
	}
	var failure *dcgmError
	if !errors.As(err, &failure) {
		t.Fatalf("Init() error = %T %v, want *dcgmError", err, err)
	}
	if failure.category != observation.CollectionErrorCategoryOther || failure.detail != "libdcgm.so.4 not found" {
		t.Fatalf("Init() error = %v (%v), want OTHER libdcgm.so.4 not found", failure.detail, failure.category)
	}
}

// recordGoDCGMInit returns a fake with init's signature, which lets a test hold
// go-dcgm's unexported mode type without naming it.
func recordGoDCGMInit[M any](_ func(M, ...string) (func(), error), gotMode *any, gotArgs *[]string) func(M, ...string) (func(), error) {
	return func(mode M, args ...string) (func(), error) {
		*gotMode, *gotArgs = mode, args
		return func() {}, nil
	}
}

func TestInitDCGMPassesGoDCGMMode(t *testing.T) {
	original := goDCGMInit
	t.Cleanup(func() { goDCGMInit = original })
	var gotMode any
	var gotArgs []string
	goDCGMInit = recordGoDCGMInit(goDCGMInit, &gotMode, &gotArgs)

	for _, test := range []struct {
		wantMode any
		args     []string
		mode     sessionMode
	}{
		{wantMode: godcgm.Embedded, mode: embeddedMode},
		{wantMode: godcgm.Standalone, args: []string{"10.0.0.1:5555", "0"}, mode: standaloneMode},
	} {
		if _, err := initDCGM(test.mode, test.args...); err != nil {
			t.Fatalf("initDCGM(%v) error = %v", test.mode, err)
		}
		if gotMode != test.wantMode || !slices.Equal(gotArgs, test.args) {
			t.Fatalf("initDCGM(%v) called go-dcgm Init(%v, %q), want Init(%v, %q)", test.mode, gotMode, gotArgs, test.wantMode, test.args)
		}
	}
}

func TestInitDCGMRejectsUnknownMode(t *testing.T) {
	for _, mode := range []sessionMode{0, autoMode + 1} {
		cleanup, err := initDCGM(mode)
		if cleanup != nil {
			t.Fatalf("initDCGM(%d) returned a cleanup function", mode)
		}
		want := fmt.Sprintf("unknown DCGM session mode %d", mode)
		if err == nil || err.Error() != want {
			t.Fatalf("initDCGM(%d) error = %v, want %q", mode, err, want)
		}
	}
}

func TestNewStandaloneSourceNativeOptions(t *testing.T) {
	tests := []struct {
		name string
		in   StandaloneOptions
		want nativeOptions
	}{
		{
			name: "defaults",
			want: nativeOptions{address: "localhost", mode: standaloneMode},
		},
		{
			name: "explicit",
			in:   StandaloneOptions{Address: "/run/x", GroupNamePrefix: "hv", IsSocket: true},
			want: nativeOptions{address: "/run/x", groupNamePrefix: "hv", isSocket: true, mode: standaloneMode},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := NewStandaloneSource(test.in)
			if source.mode != standaloneMode {
				t.Fatalf("source mode = %v, want %v", source.mode, standaloneMode)
			}
			library, ok := source.native.(*nativeLibrary)
			if !ok {
				t.Fatalf("native = %T, want *nativeLibrary", source.native)
			}
			if library.options != test.want {
				t.Fatalf("native options = %+v, want %+v", library.options, test.want)
			}
			if library.dcgmInit == nil {
				t.Fatal("native dcgmInit is nil")
			}
		})
	}
}

func TestNewEmbeddedSourceNativeOptions(t *testing.T) {
	library, ok := NewEmbeddedSource(EmbeddedOptions{GroupNamePrefix: "hv"}).native.(*nativeLibrary)
	if !ok {
		t.Fatal("embedded source native is not *nativeLibrary")
	}
	if want := (nativeOptions{groupNamePrefix: "hv", mode: embeddedMode}); library.options != want {
		t.Fatalf("native options = %+v, want %+v", library.options, want)
	}
	if library.dcgmInit == nil {
		t.Fatal("native dcgmInit is nil")
	}
}

func TestNewAutoSourceNativeOptions(t *testing.T) {
	source := NewAutoSource(AutoOptions{GroupNamePrefix: "hv"})
	standalone, standaloneOK := source.native.(*nativeLibrary)
	embedded, embeddedOK := source.fallback.(*nativeLibrary)
	if !standaloneOK || !embeddedOK {
		t.Fatalf("natives = %T, %T; want *nativeLibrary", source.native, source.fallback)
	}
	if want := (nativeOptions{address: "localhost", groupNamePrefix: "hv", mode: standaloneMode}); standalone.options != want {
		t.Fatalf("standalone options = %+v, want %+v", standalone.options, want)
	}
	if want := (nativeOptions{groupNamePrefix: "hv", mode: embeddedMode}); embedded.options != want {
		t.Fatalf("embedded options = %+v, want %+v", embedded.options, want)
	}
}

func TestAutoSourceFallsBackOnGoDCGMConnectError(t *testing.T) {
	var modes []sessionMode
	dcgmInit := func(mode sessionMode, _ ...string) (func(), error) {
		modes = append(modes, mode)
		if mode == standaloneMode {
			return nil, errors.New("error connecting to nv-hostengine: Host engine connection invalid/disconnected")
		}
		return func() {}, nil
	}
	var processMode sessionMode
	source := newSourceInMode(&nativeLibrary{dcgmInit: dcgmInit, options: nativeOptions{address: "localhost", mode: standaloneMode}},
		&sync.Mutex{}, &processMode, autoMode)
	source.fallback = &nativeLibrary{dcgmInit: dcgmInit, options: nativeOptions{mode: embeddedMode}}
	source.beforeEmbedded = func(context.Context) error { return nil }

	if err := source.ensureInitialized(context.Background()); err != nil {
		t.Fatalf("ensureInitialized() error = %v", err)
	}
	if !slices.Equal(modes, []sessionMode{standaloneMode, embeddedMode}) || processMode != embeddedMode {
		t.Fatalf("Init modes = %v, process mode = %v; want [standalone embedded], embedded", modes, processMode)
	}
}

func TestAutoSourceReportsGoDCGMEmbeddedFailure(t *testing.T) {
	dcgmInit := func(mode sessionMode, _ ...string) (func(), error) {
		if mode == standaloneMode {
			return nil, errors.New("error connecting to nv-hostengine: Host engine connection invalid/disconnected")
		}
		return nil, errors.New("error starting nv-hostengine: DCGM initialization error")
	}
	var processMode sessionMode
	source := newSourceInMode(&nativeLibrary{dcgmInit: dcgmInit, options: nativeOptions{address: "localhost", mode: standaloneMode}},
		&sync.Mutex{}, &processMode, autoMode)
	source.fallback = &nativeLibrary{dcgmInit: dcgmInit, options: nativeOptions{mode: embeddedMode}}
	source.beforeEmbedded = func(context.Context) error { return nil }

	failure := collectionError(source.ensureInitialized(context.Background()))
	if failure.GetCategory() != observation.CollectionErrorCategoryUnavailable || failure.GetSourceError() != "DCGM_ST_INIT_ERROR" ||
		!strings.Contains(failure.GetDetail(), "embedded DCGM failed: error starting nv-hostengine: DCGM initialization error") {
		t.Fatalf("failure = %v, want UNAVAILABLE DCGM_ST_INIT_ERROR with the embedded error", &failure)
	}
}
