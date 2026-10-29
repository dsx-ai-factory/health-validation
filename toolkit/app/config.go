// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
	"github.com/dsx-ai-factory/health-validation/toolkit"
)

// Config is the operator-supplied configuration for health-validation.
//
// For `run`, each entry under Checks maps a check ID to threshold overrides;
// unlisted checks remain enabled except rdma_topo, which requires an explicit
// entry (rdma_topo: {}). For `collect`, the listed check IDs select which sensors
// run; an empty list selects every default-enabled check. Threshold values are
// validated but not evaluated during collection.
// DisabledChecks lists check IDs to exclude. During `run`, they are reported
// as skipped with [health.CheckDisabled] and excluded from the node-level
// severity. CollectionTimeoutSeconds optionally bounds each sensor's
// collection (0 = use the built-in default). Metrics optionally pushes the
// verdict `run` produces to an OTLP/HTTP endpoint:
//
//	collection_timeout_seconds: 60
//	checks:
//	  gpu_count:
//	    expected_gpu_count: 4
//	  nvlink_active:
//	    max_inactive_nvlinks: 0
//	disabled_checks:
//	  - gpu_power_state
//	metrics:
//	  enabled: true
//	  endpoint: http://127.0.0.1:44318
type Config struct {
	Checks                   map[string]map[string]any `yaml:"checks"`
	DisabledChecks           []string                  `yaml:"disabled_checks"`
	Metrics                  MetricsConfig             `yaml:"metrics"`
	CollectionTimeoutSeconds int                       `yaml:"collection_timeout_seconds"`
}

// MetricsConfig configures the OTLP/HTTP metrics push performed after a run
// produces a verdict; see the README for the published metric set.
//
//	metrics:
//	  enabled: true
//	  endpoint: http://127.0.0.1:44318   # full URL, not the collector's host:port form
//	  timeout_seconds: 10
//	  headers:
//	    x-api-key: ...
//	  resource_attributes:
//	    cluster: dsx-east
//
// Publishing is off unless Enabled is set, and a failure to publish never
// changes the verdict or the exit code.
//
// Destination and credentials come only from this config: the OTLP exporter
// applies OTEL_EXPORTER_OTLP_* before the options it is given, so
// [otelmetrics.New] passes each of them explicitly to overwrite the
// environment. TLS trust material (OTEL_EXPORTER_OTLP_CERTIFICATE and the
// client-cert variables) is the deliberate exception — it is the only way to
// reach a collector behind a private CA, and this config has no field for it.
type MetricsConfig struct {
	Headers            map[string]string `yaml:"headers"`
	ResourceAttributes map[string]string `yaml:"resource_attributes"`
	Endpoint           string            `yaml:"endpoint"`
	TimeoutSeconds     int               `yaml:"timeout_seconds"`
	Enabled            bool              `yaml:"enabled"`
}

// DefaultMetricsTimeout bounds the OTLP export when the operator sets no
// timeout_seconds. Retries are disabled in the publisher, so this is the whole
// budget, not one attempt's.
const DefaultMetricsTimeout = 10 * time.Second

// Timeout returns the export timeout to apply; a non-positive TimeoutSeconds
// falls back to [DefaultMetricsTimeout].
func (mc *MetricsConfig) Timeout() time.Duration {
	if mc.TimeoutSeconds > 0 {
		return time.Duration(mc.TimeoutSeconds) * time.Second
	}
	return DefaultMetricsTimeout
}

// validate rejects a metrics block that would fail only at publish time, when
// the run has already completed and the error is too late to be actionable.
// A disabled block is not validated: an operator commenting out `enabled` should
// not have to also strip a half-written endpoint.
func (mc *MetricsConfig) validate(path string) error {
	if !mc.Enabled {
		return nil
	}
	// Store the trimmed value, not just validate it: url.Parse rejects
	// surrounding whitespace, so keeping the original would let a quoted
	// " http://host:4318 " pass here and fail at publish time.
	endpoint := strings.TrimSpace(mc.Endpoint)
	mc.Endpoint = endpoint
	if endpoint == "" {
		return fmt.Errorf("config %s: metrics.endpoint is required when metrics.enabled is true", path)
	}
	// A bare "host:port" — the form the collector's own receiver config uses,
	// so the likeliest mistake — fails url.Parse outright rather than parsing
	// to a bad scheme. Both land on the same message, which names the fix.
	u, err := url.Parse(endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf(
			"config %s: metrics.endpoint %q must be a full URL with an http or https scheme (e.g. http://%s)",
			path, endpoint, endpoint)
	}
	if u.Host == "" {
		return fmt.Errorf("config %s: metrics.endpoint %q has no host", path, endpoint)
	}
	if mc.TimeoutSeconds < 0 || mc.TimeoutSeconds > maxCollectionTimeoutSeconds {
		return fmt.Errorf("config %s: metrics.timeout_seconds must be between 0 and %d, got %d",
			path, maxCollectionTimeoutSeconds, mc.TimeoutSeconds)
	}
	return nil
}

// maxCollectionTimeoutSeconds is the largest collection_timeout_seconds
// that does not overflow a time.Duration (int64 nanoseconds) when scaled
// to seconds; larger values would wrap to a negative, already-expired
// timeout.
const maxCollectionTimeoutSeconds = int(math.MaxInt64 / int64(time.Second))

// LoadConfig reads and parses the YAML config at path.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	var cfg Config
	// KnownFields(true) rejects unrecognized keys so a typo like `check:`
	// (instead of `checks:`) is reported instead of silently yielding a
	// nil Checks map and an empty override set.
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	// Decode returns io.EOF for an empty document; treat that as a valid
	// "no overrides" config rather than an error (Unmarshal's old behavior).
	if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	if cfg.CollectionTimeoutSeconds < 0 || cfg.CollectionTimeoutSeconds > maxCollectionTimeoutSeconds {
		return nil, fmt.Errorf("config %s: collection_timeout_seconds must be between 0 and %d, got %d",
			path, maxCollectionTimeoutSeconds, cfg.CollectionTimeoutSeconds)
	}
	if err := cfg.Metrics.validate(path); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// CollectionTimeout returns the per-sensor collection timeout to apply.
// A non-positive CollectionTimeoutSeconds (the default when the operator
// did not set one) falls back to the toolkit default.
func (cfg *Config) CollectionTimeout() time.Duration {
	if cfg.CollectionTimeoutSeconds > 0 {
		return time.Duration(cfg.CollectionTimeoutSeconds) * time.Second
	}
	return toolkit.DefaultSensorTimeout
}

// Overrides validates the config against the catalog's check definitions
// and returns the corresponding [catalog.OverrideSet].
//
// It rejects:
//   - a check ID with no matching registered check,
//   - a parameter that is not a threshold on that check,
//   - a parameter whose threshold is Fixed (non-overridable), and
//   - a value that cannot be coerced to the threshold's declared type.
//
// Checks and parameters are validated in sorted order so the first error
// is deterministic.
func (cfg *Config) Overrides(cat *catalog.Catalog) (catalog.OverrideSet, error) {
	var overrides []catalog.Override

	for _, checkID := range sortedKeys(cfg.Checks) {
		cd, err := cat.Get(checkID)
		if err != nil {
			return catalog.OverrideSet{}, fmt.Errorf("config: unknown check %q", checkID)
		}

		params := cfg.Checks[checkID]
		for _, name := range sortedKeys(params) {
			spec, ok := cd.Thresholds[name]
			if !ok {
				return catalog.OverrideSet{}, fmt.Errorf(
					"config: check %q has no parameter %q", checkID, name)
			}
			if spec.Category == health.FixedThreshold {
				return catalog.OverrideSet{}, fmt.Errorf(
					"config: parameter %q on check %q is fixed and cannot be overridden",
					name, checkID)
			}

			raw := params[name]
			if spec.ValueType == health.ThresholdStringList {
				raw = trimListItems(raw)
			}
			value, err := spec.CoerceInput(raw)
			if err != nil {
				return catalog.OverrideSet{}, fmt.Errorf(
					"config: check %q parameter %q: %w", checkID, name, err)
			}

			overrides = append(overrides, catalog.Override{
				CheckID:       checkID,
				ThresholdName: name,
				Value:         value,
			})
		}
	}

	return catalog.NewOverrideSet(overrides...), nil
}

// trimListItems strips whitespace around the entries of a decoded string
// list. It stays here rather than in the catalog's coercion because only a
// serialized value can pick up whitespace it did not mean; an item left
// empty by trimming is still rejected during coercion.
func trimListItems(val any) any {
	items, ok := val.([]any)
	if !ok {
		return val
	}
	trimmed := make([]any, len(items))
	for i, item := range items {
		if s, ok := item.(string); ok {
			trimmed[i] = strings.TrimSpace(s)
			continue
		}
		trimmed[i] = item
	}
	return trimmed
}

// DisabledCheckIDs validates the config's disabled_checks against the
// catalog's check definitions and returns the trimmed IDs to disable, including
// rdma_topo unless explicitly listed in Checks. Explicit disables take precedence.
//
// It rejects a check ID with no matching registered check, and an empty
// entry. Validating here is what keeps a typo'd ID from reading as a
// working disable while the check silently keeps running.
func (cfg *Config) DisabledCheckIDs(cat *catalog.Catalog) ([]string, error) {
	var disabledChecks []string

	for _, checkID := range cfg.DisabledChecks {
		checkID = strings.TrimSpace(checkID)
		if checkID == "" {
			return nil, fmt.Errorf("config: disabled_checks contains empty entry")
		}
		_, err := cat.Get(checkID)
		if err != nil {
			return nil, fmt.Errorf("config: unknown check %q", checkID)
		}
		disabledChecks = append(disabledChecks, checkID)
	}
	if _, enabled := cfg.Checks["rdma_topo"]; !enabled {
		disabledChecks = append(disabledChecks, "rdma_topo")
	}
	return disabledChecks, nil
}

// sortedKeys returns the keys of m in sorted order for deterministic
// iteration.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
