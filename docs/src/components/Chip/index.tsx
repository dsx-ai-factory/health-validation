/*
 * SPDX-FileCopyrightText: Copyright (c) 2025-2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
 * SPDX-License-Identifier: Apache-2.0
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

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
  gpu: styles.green,
  nvlink: styles.purple,
  pcie: styles.blue,
  infiniband: styles.orange,
  storage: styles.teal,
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
