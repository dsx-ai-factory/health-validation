// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

import React from "react";
import styles from "./styles.module.css";

const VARIANT_CLASSES: Record<string, string> = {
  // Severity
  PASS: styles.green,
  INFO: styles.blue,
  WARNING: styles.amber,
  CRITICAL: styles.red,
  UNKNOWN: styles.gray,
  // Category
  cpu: styles.orange,
  gpu: styles.green,
  nvlink: styles.purple,
  pcie: styles.blue,
  infiniband: styles.orange,
  storage: styles.teal,
  node: styles.purple,
  xid: styles.red,
  // Threshold category
  DEFAULT: styles.blue,
  FIXED: styles.red,
  ADJUSTABLE: styles.amber,
  // Execution class
  PASSIVE: styles.teal,
  ACTIVE: styles.orange,
  // Provenance
  nvidia: styles.green,
  community: styles.blue,
  private: styles.purple,
  // Scheduling cadence
  PROLOG: styles.blue,
  CONTINUOUS: styles.teal,
  TENANT_TURNOVER: styles.amber,
  MAINTENANCE: styles.purple,
  // Execution context
  OPERATOR_INFRA: styles.blue,
  TENANT_GUEST: styles.teal,
  OUT_OF_BAND: styles.purple,
  // Generic
  neutral: styles.gray,
};

interface ChipProps {
  label: string;
  variant?: string;
  small?: boolean;
}

export default function Chip({ label, variant, small }: ChipProps) {
  const colorClass = VARIANT_CLASSES[variant ?? label] ?? styles.gray;
  return (
    <span
      className={`${styles.chip} ${colorClass} ${small ? styles.small : ""}`}
    >
      {label}
    </span>
  );
}
