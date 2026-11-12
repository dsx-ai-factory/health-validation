// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { validateReviewedDecisions } from './intake-plan.mjs';

export function checkRegistry(text, evaluatedAt = new Date().toISOString()) {
  const invalid = code => ({ valid: false, record_count: 0, errors: [{ code }] });
  // GitHub Actions limits each secret to 48 KB, including JSON whitespace.
  if (Buffer.byteLength(text, 'utf8') > 48 * 1024) return invalid('registry_too_large');
  let records;
  try { records = JSON.parse(text); } catch { return invalid('invalid_json'); }
  const errors = validateReviewedDecisions(records, evaluatedAt);
  return { valid: errors.length === 0, record_count: Array.isArray(records) ? records.length : 0, errors };
}

function cli() {
  if (process.argv.length !== 3) throw new Error('use check-registry.mjs PRIVATE_JSON_FILE');
  let text;
  try { text = readFileSync(process.argv[2], 'utf8'); } catch { throw new Error('registry file unavailable'); }
  const result = checkRegistry(text);
  if (!result.valid) {
    for (const error of result.errors) process.stderr.write(`Registry invalid: ${error.code}${error.record ? ` at record ${error.record}` : ''}.\n`);
    process.exitCode = 1;
    return;
  }
  process.stdout.write(`Registry format valid: ${result.record_count} records. Evidence still requires review before upload.\n`);
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try { cli(); } catch { process.stderr.write('Registry check failed. Use check-registry.mjs PRIVATE_JSON_FILE with a readable file.\n'); process.exitCode = 1; }
}
