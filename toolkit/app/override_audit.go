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

// overrideRecord is one configured override and its check's provenance.
type overrideRecord struct {
	catalog.ConfiguredOverride
	provenance health.Provenance
}

// auditOverrides records every override configured on cat in the journal,
// opening it only when there is something to record.
func (a *App) auditOverrides(cat *catalog.Catalog) error {
	records, err := overrideRecords(cat)
	if err != nil || len(records) == 0 {
		return err
	}
	journal, err := a.openJournal()
	if err != nil {
		return err
	}
	defer journal.Close()
	return writeOverrideAudit(journal, records)
}

// overrideRecords pairs each override configured on cat with its check's
// provenance.
func overrideRecords(cat *catalog.Catalog) ([]overrideRecord, error) {
	configured, err := cat.ConfiguredOverrides()
	if err != nil {
		return nil, err
	}
	records := make([]overrideRecord, 0, len(configured))
	for _, o := range configured {
		cd, err := cat.Get(o.CheckID)
		if err != nil {
			return nil, err
		}
		records = append(records, overrideRecord{ConfiguredOverride: o, provenance: cd.Provenance})
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
		original, err := json.Marshal(r.OriginalValue)
		if err != nil {
			return fmt.Errorf("encode audit entry for %s.%s: %w", r.CheckID, r.ThresholdName, err)
		}
		value, err := json.Marshal(r.Value)
		if err != nil {
			return fmt.Errorf("encode audit entry for %s.%s: %w", r.CheckID, r.ThresholdName, err)
		}
		var entry []byte
		for _, f := range [][2]string{
			{"MESSAGE_ID", overrideAuditMessageID},
			{"MESSAGE", fmt.Sprintf("threshold override configured: %s.%s = %s (catalog default %s)",
				r.CheckID, r.ThresholdName, value, original)},
			{"PRIORITY", "5"},
			{"SYSLOG_IDENTIFIER", "health-validation"},
			{"CHECK_ID", r.CheckID},
			{"THRESHOLD_NAME", r.ThresholdName},
			{"ORIGINAL_VALUE", string(original)},
			{"OVERRIDE_VALUE", string(value)},
			{"PROVENANCE", r.provenance.String()},
		} {
			// A newline would end the field early and corrupt the entry.
			if strings.ContainsRune(f[1], '\n') {
				return fmt.Errorf("audit entry for %q: %s contains a newline", r.CheckID, f[0])
			}
			entry = append(entry, f[0]+"="+f[1]+"\n"...)
		}
		if _, err = w.Write(entry); err != nil {
			return fmt.Errorf("write audit entry for %s.%s: %w", r.CheckID, r.ThresholdName, err)
		}
	}
	return nil
}
