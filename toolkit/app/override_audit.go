package app

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/dsx-ai-factory/health-validation/catalog"
	"github.com/dsx-ai-factory/health-validation/catalog/health"
)

// overrideAuditMessageID is the journald MESSAGE_ID of every override audit
// entry, so `journalctl MESSAGE_ID=...` selects exactly these entries. Operator
// queries depend on it; never change it.
const overrideAuditMessageID = "e5a478e586994730854b18982d557053"

// auditWriteTimeout bounds the audit writes so a stalled journald fails the
// run instead of hanging it.
const auditWriteTimeout = 5 * time.Second

// overrideRecord is one operator threshold override and the catalog default
// it replaced.
type overrideRecord struct {
	originalValue any
	value         any
	checkID       string
	thresholdName string
	provenance    health.Provenance
}

// overrideRecords pairs each override with its catalog default for arch,
// resolved the same way evaluation resolves it.
func overrideRecords(cat *catalog.Catalog, overrides []catalog.Override, arch string) ([]overrideRecord, error) {
	records := make([]overrideRecord, 0, len(overrides))
	for _, o := range overrides {
		cd, err := cat.Get(o.CheckID)
		if err != nil {
			return nil, err
		}
		defaults, err := health.ResolveThresholds(cd.ID, cd.Thresholds, nil, arch)
		if err != nil {
			return nil, err
		}
		records = append(records, overrideRecord{
			originalValue: defaults[o.ThresholdName].OriginalValue,
			value:         o.Value,
			checkID:       o.CheckID,
			thresholdName: o.ThresholdName,
			provenance:    cd.Provenance,
		})
	}
	return records, nil
}

// writeOverrideAudit writes one journal entry per record to w, stamped with
// the verdict the overrides fed, and stops at the first failure.
func writeOverrideAudit(w io.Writer, records []overrideRecord, v health.NodeVerdict, nodeID, prog string) error {
	if d, ok := w.(interface{ SetWriteDeadline(time.Time) error }); ok {
		if err := d.SetWriteDeadline(time.Now().Add(auditWriteTimeout)); err != nil {
			return err
		}
	}
	timestamp := v.Timestamp.UTC().Format(time.RFC3339Nano)
	for _, r := range records {
		original, err := json.Marshal(r.originalValue)
		if err != nil {
			return fmt.Errorf("encode audit entry for %s.%s: %w", r.checkID, r.thresholdName, err)
		}
		value, err := json.Marshal(r.value)
		if err != nil {
			return fmt.Errorf("encode audit entry for %s.%s: %w", r.checkID, r.thresholdName, err)
		}
		entry := journalDatagram([]journalField{
			{"MESSAGE_ID", overrideAuditMessageID},
			{"MESSAGE", fmt.Sprintf("threshold override active: %s.%s = %s (catalog default %s)",
				r.checkID, r.thresholdName, value, original)},
			{"PRIORITY", "5"},
			{"SYSLOG_IDENTIFIER", prog},
			{"CHECK_ID", r.checkID},
			{"THRESHOLD_NAME", r.thresholdName},
			{"ORIGINAL_VALUE", string(original)},
			{"OVERRIDE_VALUE", string(value)},
			{"PROVENANCE", r.provenance.String()},
			{"TIMESTAMP", timestamp},
			{"TRACE_ID", v.TraceID},
			{"NODE_ID", nodeID},
			{"CATALOG_VERSION", v.CatalogVersion},
		})
		if _, err := w.Write(entry); err != nil {
			return fmt.Errorf("write audit entry for %s.%s: %w", r.checkID, r.thresholdName, err)
		}
	}
	return nil
}
