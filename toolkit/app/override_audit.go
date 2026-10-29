// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
)

// overrideAuditMessageID tags every audit entry. Operators query by it, so it
// must never change.
const overrideAuditMessageID = "e5a478e586994730854b18982d557053"

// overrideRecord is one operator threshold override and the catalog value it
// replaced.
type overrideRecord struct {
	originalValue any
	value         any
	checkID       string
	thresholdName string
	provenance    health.Provenance
}

// auditOverrides records every override cfg applies to cat in the journal,
// opening it only when there is something to record.
func (a *App) auditOverrides(cfg *Config, cat *catalog.Catalog, arch string) error {
	overrides, err := cfg.overrideList(cat)
	if err != nil || len(overrides) == 0 {
		return err
	}
	records, err := overrideRecords(cat, overrides, arch)
	if err != nil {
		return err
	}
	journal, err := a.openJournal()
	if err != nil {
		return err
	}
	defer journal.Close()
	return writeOverrideAudit(journal, records)
}

// overrideRecords pairs each override with the catalog value for arch.
func overrideRecords(cat *catalog.Catalog, overrides []catalog.Override, arch string) ([]overrideRecord, error) {
	records := make([]overrideRecord, 0, len(overrides))
	for _, o := range overrides {
		cd, err := cat.Get(o.CheckID)
		if err != nil {
			return nil, err
		}
		// The base-value rule of catalog/internal/evaluation's resolveThresholds;
		// TestAuditOriginalValueMatchesVerdict keeps the two in step.
		spec := cd.Thresholds[o.ThresholdName]
		original := spec.Value
		if v, ok := spec.PerArchitecture[arch]; ok {
			original = v
		}
		records = append(records, overrideRecord{
			originalValue: original,
			value:         o.Value,
			checkID:       o.CheckID,
			thresholdName: o.ThresholdName,
			provenance:    cd.Provenance,
		})
	}
	return records, nil
}

// dialJournal connects to journald; each Write sends one entry. The deadline
// makes a stalled journald fail the run instead of hanging it.
func dialJournal() (io.WriteCloser, error) {
	conn, err := net.Dial("unixgram", "/run/systemd/journal/socket")
	if err != nil {
		return nil, err
	}
	if err = conn.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

// writeOverrideAudit writes one journal entry per record to w and stops at the
// first failure.
func writeOverrideAudit(w io.Writer, records []overrideRecord) error {
	for _, r := range records {
		original, err := json.Marshal(r.originalValue)
		if err != nil {
			return fmt.Errorf("encode audit entry for %s.%s: %w", r.checkID, r.thresholdName, err)
		}
		value, err := json.Marshal(r.value)
		if err != nil {
			return fmt.Errorf("encode audit entry for %s.%s: %w", r.checkID, r.thresholdName, err)
		}
		var entry []byte
		for _, f := range [][2]string{
			{"MESSAGE_ID", overrideAuditMessageID},
			{"MESSAGE", fmt.Sprintf("threshold override active: %s.%s = %s (catalog default %s)",
				r.checkID, r.thresholdName, value, original)},
			{"PRIORITY", "5"},
			{"CHECK_ID", r.checkID},
			{"THRESHOLD_NAME", r.thresholdName},
			{"ORIGINAL_VALUE", string(original)},
			{"OVERRIDE_VALUE", string(value)},
			{"PROVENANCE", r.provenance.String()},
		} {
			// A newline would end the field early and corrupt the entry.
			if strings.ContainsRune(f[1], '\n') {
				return fmt.Errorf("audit entry for %q: %s contains a newline", r.checkID, f[0])
			}
			entry = append(entry, f[0]+"="+f[1]+"\n"...)
		}
		if _, err = w.Write(entry); err != nil {
			return fmt.Errorf("write audit entry for %s.%s: %w", r.checkID, r.thresholdName, err)
		}
	}
	return nil
}
