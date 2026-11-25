// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

import test from 'node:test';
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { mkdtempSync, writeFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { checkRegistry } from './check-registry.mjs';

const NOW = '2026-10-26T12:00:00Z';
const review = (patch = {}) => ({ author_id: 1001, login: 'example-user', affiliation: 'external',
  reviewed_by: 'synthetic-reviewer', evidence_ref: 'synthetic-private-evidence', revision: 'test-1',
  reviewed_at: '2026-10-25T12:00:00Z', expires_at: '2026-10-27T12:00:00Z', ...patch });
const check = records => checkRegistry(JSON.stringify(records), NOW);

test('current human records validate without exposing their contents or changing them', () => {
  const records = [review(), review({ author_id: 1002, login: 'second-user', affiliation: 'nvidia' })];
  const before = structuredClone(records);
  assert.deepEqual(check(records), { valid: true, record_count: 2, errors: [] });
  assert.deepEqual(records, before);
  assert.equal(JSON.stringify(check(records)).includes('synthetic-private-evidence'), false);
});

test('empty registry is valid and explicitly reports no records', () => {
  assert.deepEqual(check([]), { valid: true, record_count: 0, errors: [] });
});

for (const value of [null, {}, { records: [review()], approval_status: 'pending' }, 'private-content']) {
  test(`reject non-array registry ${JSON.stringify(value).slice(0, 20)}`, () => {
    assert.equal(check(value).valid, false);
  });
}

test('invalid JSON never exposes parser input', () => {
  const result = checkRegistry('{synthetic-private-evidence', NOW);
  assert.deepEqual(result, { valid: false, record_count: 0, errors: [{ code: 'invalid_json' }] });
});

for (const [name, record] of [
  ['null record', null], ['primitive record', 'private-content'],
  ['string ID', review({ author_id: '1001' })], ['missing ID', review({ author_id: undefined })],
  ['missing login', review({ login: '' })], ['bot affiliation', review({ affiliation: 'bot' })],
  ['missing reviewer', review({ reviewed_by: '' })], ['missing evidence', review({ evidence_ref: '' })],
  ['missing revision', review({ revision: '' })], ['missing expiry', review({ expires_at: undefined })],
  ['expired', review({ expires_at: NOW })], ['future review', review({ reviewed_at: '2026-10-27T11:00:00Z' })],
  ['invalid calendar day', review({ reviewed_at: '2026-02-30T12:00:00Z' })],
]) {
  test(`reject ${name} even when it is not relevant to a current opening`, () => {
    const result = check([review({ author_id: 1002, login: 'second-user' }), record]);
    assert.equal(result.valid, false);
    assert.equal(result.errors[0].record, 2);
  });
}

for (const [name, other] of [
  ['duplicate account', review()],
  ['same ID with renamed login', review({ login: 'renamed-user' })],
  ['reused login with another ID', review({ author_id: 1002 })],
  ['case-insensitive reused login', review({ author_id: 1002, login: 'Example-User' })],
  ['conflicting affiliation', review({ affiliation: 'nvidia' })],
]) {
  test(`reject ${name}`, () => {
    assert.deepEqual(check([review(), other]).errors, [{ record: 2, code: 'review_conflict' }]);
  });
}

test('secret size is checked in UTF-8 bytes, including whitespace', () => {
  assert.equal(checkRegistry('[]' + ' '.repeat(48 * 1024 - 2), NOW).valid, true);
  assert.deepEqual(checkRegistry('[]' + ' '.repeat(48 * 1024 - 1), NOW).errors, [{ code: 'registry_too_large' }]);
  const unicode = JSON.stringify([review({ evidence_ref: '🧪'.repeat(13000) })]);
  assert.ok(unicode.length < 48 * 1024);
  assert.equal(checkRegistry(unicode, NOW).errors[0].code, 'registry_too_large');
});

test('CLI validates a private file without credentials and keeps private values out of output', () => {
  const dir = mkdtempSync(join(tmpdir(), 'intake-registry-'));
  const cli = fileURLToPath(new URL('./check-registry.mjs', import.meta.url));
  const runtime = Object.fromEntries(Object.entries(process.env).filter(([key]) => key.startsWith('JS_BINARY__')));
  const run = args => spawnSync(process.execPath, [cli, ...args], { encoding: 'utf8', env: runtime });
  try {
    const path = join(dir, 'private-records.json');
    writeFileSync(path, '[]', { mode: 0o600 });
    const empty = run([path]);
    assert.equal(empty.status, 0);
    assert.match(empty.stdout, /0 records/);
    const now = Date.now();
    writeFileSync(path, JSON.stringify([review({ reviewed_at: new Date(now - 60000).toISOString(),
      expires_at: new Date(now + 60000).toISOString() })]));
    const valid = run([path]);
    assert.equal(valid.status, 0);
    assert.match(valid.stdout, /1 records/);
    writeFileSync(path, JSON.stringify({ approval_status: 'pending', private: 'synthetic-private-evidence' }));
    const invalid = run([path]);
    assert.equal(invalid.status, 1);
    assert.match(invalid.stderr, /registry_not_array/);
    for (const result of [valid, invalid, run([]), run([join(dir, 'missing-private-file.json')])]) {
      assert.equal(`${result.stdout}${result.stderr}`.includes('synthetic-private-evidence'), false);
      assert.equal(`${result.stdout}${result.stderr}`.includes(dir), false);
    }
    assert.equal(run([]).status, 1);
    assert.equal(run([path, 'extra']).status, 1);
    assert.equal(run([join(dir, 'missing-private-file.json')]).status, 1);
  } finally { rmSync(dir, { recursive: true, force: true }); }
});
