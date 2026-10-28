// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package gpuinventory

import (
	"errors"
	"fmt"
	"strings"

	"github.com/dsx-ai-factory/health-validation/collect/observation"
)

// ArchitectureUnknown is reported when at least one GPU model reads but none
// maps to a recognized architecture. It is absent from every check's
// Architectures list, so architecture-scoped checks skip rather than run
// against another architecture's thresholds. Discovery failures (no GPUs, or
// no readable model) are errors, not this value.
const ArchitectureUnknown = "unknown"

// Architecture reports the catalog architecture of the first GPU in snapshot
// whose model maps to one. An enumeration failure, an empty inventory, or an
// inventory where no model could be read is an error: callers treat it as a
// node fault rather than evaluate against the wrong thresholds.
func Architecture(snapshot *observation.GPUInventorySnapshot) (string, error) {
	if failure := snapshot.GetCollectionError(); failure != nil {
		return "", fmt.Errorf("GPU architecture discovery: %s", describe(failure))
	}
	entries := snapshot.GetInventory().GetEntries()
	if len(entries) == 0 {
		return "", errors.New("GPU architecture discovery: no GPUs enumerated")
	}
	modelRead := false
	var firstFailure string
	for _, entry := range entries {
		if entry.GetGpu() == nil || entry.GetGpu().Model == nil {
			if firstFailure == "" {
				failure := entry.GetFieldErrors()["model"]
				if failure == nil {
					failure = entry.GetCollectionError()
				}
				firstFailure = fmt.Sprintf("GPU %d: %s", entry.GetSourceIndex(), describe(failure))
			}
			continue
		}
		modelRead = true
		if arch := ArchitectureFromName(entry.GetGpu().GetModel()); arch != "" {
			return arch, nil
		}
	}
	if !modelRead {
		return "", fmt.Errorf("GPU architecture discovery: no GPU model could be read from %d device(s): %s", len(entries), firstFailure)
	}
	return ArchitectureUnknown, nil
}

func describe(failure *observation.CollectionError) string {
	if failure == nil {
		return "model was not reported"
	}
	msg := fmt.Sprintf("%s failed with %s", failure.GetOperation(), failure.GetCategory())
	if failure.GetSourceError() != "" {
		msg += fmt.Sprintf(" (%s)", failure.GetSourceError())
	}
	if failure.GetDetail() != "" {
		msg += ": " + failure.GetDetail()
	}
	return msg
}

// ArchitectureFromName maps an NVML product name (e.g. "NVIDIA H100 80GB
// HBM3", "NVIDIA GB300") to the catalog architecture identifier the check
// definitions are scoped by, or "" for an unrecognized name. More specific
// tokens are matched first so "NVIDIA GB300" does not fall through to a
// broader family match.
func ArchitectureFromName(name string) string {
	switch {
	case strings.Contains(name, "GB300"):
		return "GB300"
	case strings.Contains(name, "GB200"):
		return "GB200"
	case strings.Contains(name, "B200"):
		return "B200"
	case strings.Contains(name, "H200"):
		return "H200"
	case strings.Contains(name, "H100"):
		return "H100"
	case strings.Contains(name, "A100"):
		return "A100"
	default:
		return ""
	}
}
