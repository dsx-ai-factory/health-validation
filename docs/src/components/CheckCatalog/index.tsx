import React, { useState } from "react";
import { usePluginData } from "@docusaurus/useGlobalData";
import Chip from "../Chip";
import styles from "./styles.module.css";

interface ThresholdSpec {
  value: unknown;
  description: string;
  unit: string;
  category: string;
}

interface Rule {
  expression: string;
  message: string;
  severity: string;
}

interface CheckDefinition {
  id: string;
  name: string;
  description: string;
  category: string;
  condition_scope: string;
  provenance: string;
  maturity_level: string;
  execution_class: string;
  execution_contexts: string[];
  scheduling_cadence: string;
  architectures?: string[];
  required_signals: string[];
  thresholds: Record<string, ThresholdSpec>;
  rules: Rule[];
  dependencies?: string[];
  recommended_action?: {
    operational_state: string;
    diagnostic_action?: string;
  };
}

function CheckDetail({ check }: { check: CheckDefinition }) {
  return (
    <div className={styles.detail}>
      <p className={styles.description}>{check.description}</p>

      <div className={styles.metaRow}>
        <Chip label={check.provenance} />
        <Chip label={check.condition_scope} />
        <Chip label={check.execution_class} />
        <Chip label={check.scheduling_cadence} />
        <Chip label={check.maturity_level} variant="neutral" />
      </div>

      {check.architectures && check.architectures.length > 0 && (
        <div className={styles.section}>
          <h4>Architectures</h4>
          <div className={styles.chipRow}>
            {check.architectures.map((arch) => (
              <Chip key={arch} label={arch} variant="neutral" />
            ))}
          </div>
        </div>
      )}

      {check.dependencies && check.dependencies.length > 0 && (
        <div className={styles.section}>
          <h4>Depends On</h4>
          <p className={styles.description}>
            A verdict for a device this check could not read at all names these
            checks as the cause when they report CRITICAL.
          </p>
          <div className={styles.chipRow}>
            {check.dependencies.map((dep) => (
              <Chip key={dep} label={dep} variant="neutral" />
            ))}
          </div>
        </div>
      )}

      <div className={styles.section}>
        <h4>Execution Contexts</h4>
        <div className={styles.chipRow}>
          {check.execution_contexts.map((ctx) => (
            <Chip key={ctx} label={ctx} />
          ))}
        </div>
      </div>

      <div className={styles.section}>
        <h4>Required Signals</h4>
        <div className={styles.chipRow}>
          {check.required_signals.map((sig) => (
            <Chip key={sig} label={sig} variant="neutral" small />
          ))}
        </div>
      </div>

      {Object.keys(check.thresholds ?? {}).length > 0 && (
        <div className={styles.section}>
          <h4>Thresholds</h4>
          <div className={styles.tableWrap}>
            <table>
              <thead>
                <tr>
                  <th>Name</th>
                  <th>Value</th>
                  <th>Unit</th>
                  <th>Category</th>
                  <th>Description</th>
                </tr>
              </thead>
              <tbody>
                {Object.entries(check.thresholds).map(([name, spec]) => (
                  <tr key={name}>
                    <td>
                      <code>{name}</code>
                    </td>
                    <td>
                      <code>{String(spec.value)}</code>
                    </td>
                    <td>{spec.unit}</td>
                    <td>
                      <Chip label={spec.category} small />
                    </td>
                    <td>{spec.description}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {check.rules.length > 0 && (
        <div className={styles.section}>
          <h4>Rules</h4>
          <div className={styles.ruleList}>
            {check.rules.map((rule, i) => (
              <div key={i} className={styles.ruleCard}>
                <div className={styles.ruleHeader}>
                  <Chip label={rule.severity} />
                </div>
                <code className={styles.expression}>{rule.expression}</code>
                <p className={styles.ruleMessage}>{rule.message}</p>
              </div>
            ))}
          </div>
        </div>
      )}

      {check.recommended_action && (
        <div className={styles.section}>
          <h4>Recommended Action</h4>
          <div className={styles.chipRow}>
            <Chip label={check.recommended_action.operational_state} variant="amber" />
            {check.recommended_action.diagnostic_action && (
              <Chip label={check.recommended_action.diagnostic_action} variant="amber" />
            )}
          </div>
        </div>
      )}
    </div>
  );
}

export default function CheckCatalog({ category }: { category?: string }) {
  const { checks } = usePluginData("health-data") as {
    checks: CheckDefinition[];
  };
  const [expanded, setExpanded] = useState<Record<string, boolean>>({});

  const toggle = (id: string) =>
    setExpanded((prev) => ({ ...prev, [id]: !prev[id] }));

  const filtered = category
    ? checks.filter((c) => c.category === category)
    : checks;

  return (
    <div className={styles.catalog}>
      {filtered.map((check) => (
        <div key={check.id} className={styles.checkItem}>
          <button
            className={styles.checkHeader}
            onClick={() => toggle(check.id)}
            aria-expanded={!!expanded[check.id]}
          >
            <Chip label={check.category} />
            <span className={styles.checkName}>{check.name}</span>
            <span className={styles.checkId}>
              <code>{check.id}</code>
            </span>
            <span className={styles.chevron}>
              {expanded[check.id] ? "\u25B2" : "\u25BC"}
            </span>
          </button>
          {expanded[check.id] && <CheckDetail check={check} />}
        </div>
      ))}
      {filtered.length === 0 && (
        <p className={styles.empty}>No checks in this category yet.</p>
      )}
    </div>
  );
}
