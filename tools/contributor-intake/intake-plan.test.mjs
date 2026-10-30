// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

import test from 'node:test';
import assert from 'node:assert/strict';
import { planIntake } from './intake-plan.mjs';

const NOW = '2026-10-02T05:00:00Z';
function item(overrides = {}) {
  return {
    repository: { id: 1295779810, full_name: 'dsx-ai-factory/health-validation' },
    node_id: 'I_TEST_1', number: 23, kind: 'issue', state: 'open', draft: false,
    created_at: '2026-09-24T12:00:00Z',
    user: { id: 1001, login: 'outside-user', type: 'User' },
    labels: ['bug', 'affiliation/external'], assignees: ['maintainer'],
    title: '<!here> <@U_BAD> <https://evil.invalid|click>',
    url: 'https://evil.invalid/23', author_association: 'COLLABORATOR',
    commit_email: 'forged@nvidia.com', ...overrides,
  };
}
function input(overrides = {}) {
  return {
    evaluated_at: NOW, item: item(), reviewed_decisions: [], ...overrides,
  };
}
function review(overrides = {}) {
  return {
    author_id: 1001, login: 'outside-user', affiliation: 'external',
    reviewed_by: 'reviewer', evidence_ref: 'https://github.com/example/evidence',
    reviewed_at: '2026-10-01T12:00:00Z', expires_at: '2026-10-10T12:00:00Z',
    revision: 'review-1', ...overrides,
  };
}

test('missing reviews and forged NVIDIA email stay unverified and visible', () => {
  const result = planIntake(input());
  assert.equal(result.affiliation, 'unverified');
  assert.deepEqual(result.label_changes, {
    remove: ['affiliation/external'], add: ['affiliation/unverified'],
  });
  assert.deepEqual(result.diagnostics, []);
  assert.deepEqual(Object.keys(result).sort(), ['affiliation', 'diagnostics', 'input_error', 'label_changes']);
});

test('current numeric-ID NVIDIA review establishes affiliation', () => {
  const result = planIntake(input({ reviewed_decisions: [review({ affiliation: 'nvidia' })] }));
  assert.equal(result.affiliation, 'nvidia');
});

const membership = (overrides = {}) => ({ author_id: 1001, organization: 'dsx-ai-factory',
  organization_id: 311157577, ...overrides });

for (const [organization, organization_id] of [['dsx-ai-factory', 311157577], ['NVIDIA', 1728152]]) {
  test(`authenticated membership in ${organization} independently establishes NVIDIA affiliation`, () => {
    const result = planIntake(input({ membership: membership({ organization, organization_id }) }));
    assert.equal(result.affiliation, 'nvidia');
    assert.deepEqual(result.label_changes, { remove: ['affiliation/external'], add: ['affiliation/nvidia'] });
  });
}

test('membership conflicts with a current external exception instead of silently replacing it', () => {
  const result = planIntake(input({ membership: membership(), reviewed_decisions: [review()] }));
  assert.equal(result.affiliation, 'unverified');
  assert.ok(result.diagnostics.includes('affiliation_conflict'));
});

test('matching NVIDIA exception and expired external evidence do not block membership', () => {
  for (const reviewed_decisions of [[review({ affiliation: 'nvidia' })], [review({ expires_at: NOW })]]) {
    assert.equal(planIntake(input({ membership: membership(), reviewed_decisions })).affiliation, 'nvidia');
  }
});

test('malformed or conflicting relevant exceptions stay unresolved despite membership', () => {
  for (const reviewed_decisions of [null, [review({ expires_at: null })], [review(), review({ revision: 'other' })]]) {
    assert.equal(planIntake(input({ membership: membership(), reviewed_decisions })).affiliation, 'unverified');
  }
});

for (const value of [true, {}, membership({ author_id: 999 }), membership({ organization_id: 1 }),
  membership({ organization: 'other' }), membership({ organization: 'NVIDIA' })]) {
  test(`invalid or mismatched membership evidence stays unresolved: ${JSON.stringify(value)}`, () => {
    assert.equal(planIntake(input({ membership: value })).affiliation, 'unverified');
  });
}

test('contributor-provided membership metadata is ignored without an authenticated lookup', () => {
  assert.equal(planIntake(input({ item: item({ membership: membership(), organization_membership: true }) })).affiliation, 'unverified');
});

test('labels do not lock derived affiliation and unrelated metadata survives', () => {
  const data = input({ item: item({ labels: ['affiliation/nvidia', 'enhancement', 'affiliation/bot'] }) });
  const before = structuredClone(data);
  const result = planIntake(data);
  assert.equal(result.affiliation, 'unverified');
  assert.deepEqual(result.label_changes.remove, ['affiliation/nvidia', 'affiliation/bot']);
  assert.deepEqual(result.label_changes.add, ['affiliation/unverified']);
  assert.deepEqual(data, before);
});

test('current external review establishes affiliation', () => {
  const result = planIntake(input({ reviewed_decisions: [review()] }));
  assert.equal(result.affiliation, 'external');
});

test('expired review cannot establish external status', () => {
  assert.equal(planIntake(input({ reviewed_decisions: [review({ expires_at: NOW })] })).affiliation, 'unverified');
});

test('multiple current revisions force unresolved review', () => {
  const data = input({ reviewed_decisions: [review(), review({ affiliation: 'nvidia', revision: 'review-2' })] });
  const result = planIntake(data);
  assert.equal(result.affiliation, 'unverified');
  assert.ok(result.diagnostics.includes('review_conflict'));
});

for (const [name, changes] of [
  ['missing expiry', { expires_at: undefined }],
  ['missing reviewer', { reviewed_by: '' }],
  ['missing evidence reference', { evidence_ref: '' }],
  ['missing revision', { revision: '' }],
  ['string author ID', { author_id: '1001' }],
  ['fractional author ID', { author_id: 1001.5 }],
  ['expiry before review', { expires_at: '2026-09-30T12:00:00Z' }],
  ['invalid affiliation', { affiliation: 'internal' }],
  ['future review', { reviewed_at: '2026-10-03T05:00:00Z' }],
  ['invalid date', { reviewed_at: '2026-02-31T05:00:00Z' }],
]) {
  test(`relevant malformed review (${name}) cannot establish affiliation`, () => {
    const data = input({ reviewed_decisions: [review(changes)] });
    assert.equal(planIntake(data).affiliation, 'unverified');
  });
}

test('reused login with mismatched stable author ID forces review', () => {
  const data = input({ reviewed_decisions: [review({ author_id: 88 })] });
  const result = planIntake(data);
  assert.equal(result.affiliation, 'unverified');
  assert.ok(result.diagnostics.includes('review_identity_mismatch'));
});

test('matching stable ID survives a valid login rename', () => {
  const result = planIntake(input({ reviewed_decisions: [review({ login: 'previous-login' })] }));
  assert.equal(result.affiliation, 'external');
  assert.ok(result.diagnostics.includes('review_login_changed'));
});

test('unrelated malformed review does not contaminate another reviewed author', () => {
  const data = input({ reviewed_decisions: [review({ affiliation: 'nvidia' }),
    review({ author_id: 99, login: 'another-user', expires_at: null })] });
  assert.equal(planIntake(data).affiliation, 'nvidia');
});

for (const [name, user] of [
  ['missing', undefined], ['null', null],
  ['ghost', { id: 2003, login: 'ghost', type: 'User' }],
  ['invalid login', { id: 2003, login: '<!here>', type: 'User' }],
  ['unknown type', { id: 2003, login: 'outside-user', type: 'Organization' }],
]) {
  test(`${name} author remains unverified`, () => {
    const result = planIntake(input({ item: item({ user }) }));
    assert.equal(result.affiliation, 'unverified');
  });
}

test('contribution Bot account type decides affiliation; labeling sender does not', () => {
  const bot = planIntake(input({ item: item({ user: { id: 100, login: 'dependabot[bot]', type: 'Bot' } }) }));
  assert.equal(bot.affiliation, 'bot');
  const human = planIntake(input({ item: item({ sender: { type: 'Bot' } }) }));
  assert.equal(human.affiliation, 'unverified');
});

test('draft and closed PRs retain state without mutating creation time', () => {
  const data = input({ item: item({ kind: 'pr', state: 'closed', draft: true }) });
  const result = planIntake(data);
  assert.equal(result.affiliation, 'unverified');
  assert.equal(data.item.created_at, '2026-09-24T12:00:00Z');
  assert.equal(data.item.state, 'closed');
  assert.equal(data.item.draft, true);
});

test('reviewed affiliation preserves an existing priority label', () => {
  const marked = planIntake(input({ item: item({ labels: ['bug', 'affiliation/unverified', 'priority/P1'] }), reviewed_decisions: [review()] }));
  assert.equal(marked.affiliation, 'external');
  assert.deepEqual(marked.diagnostics, []);
  assert.equal(marked.label_changes.remove.includes('priority/P1'), false);
  assert.equal(marked.label_changes.add.includes('priority/P1'), false);
});

test('invalid evaluation timestamp fails visibly', () => {
  assert.throws(() => planIntake(input({ evaluated_at: '2026-02-31T05:00:00Z' })));
});

test('review and evaluation timestamps accept microsecond precision', () => {
  const data = input({ evaluated_at: '2026-10-02T05:01:46.197773+00:00',
    reviewed_decisions: [review({ affiliation: 'nvidia', reviewed_at: '2026-10-01T08:41:24.750618+00:00' })] });
  assert.equal(planIntake(data).affiliation, 'nvidia');
});

for (const nodeId of ['MDU6SXNzdWUxMjM=', 'MDExOlB1bGxSZXF1ZXN0MQ==']) {
  test(`opaque legacy GitHub node ID ${nodeId} remains usable`, () => {
    const result = planIntake(input({ item: item({ node_id: nodeId }) }));
    assert.deepEqual(result.input_error, []);
  });
}

for (const user of [{ type: 'Bot' }, { type: 'Bot', id: 0, login: 'unknown' }]) {
  test(`known Bot account type with incomplete identity ${JSON.stringify(user)} retains bot affiliation`, () => {
    const result = planIntake(input({ item: item({ user }) }));
    assert.equal(result.affiliation, 'bot');
  });
}

test('fractional User ID remains unverified', () => {
  const result = planIntake(input({ item: item({ user: { id: 1.5, login: 'outside-user', type: 'User' } }) }));
  assert.equal(result.affiliation, 'unverified');
});

for (const [name, patch] of [
  ['missing node ID', { node_id: undefined }],
  ['unsupported kind', { kind: 'merge_request' }],
  ['invalid creation date', { created_at: '2026-02-31T05:00:00Z' }],
  ['missing labels', { labels: undefined }],
  ['invalid lifecycle', { state: 'merged' }],
  ['nonboolean PR draft', { kind: 'pr', draft: 'false' }],
  ['invalid number', { number: '58><!here>' }],
  ['wrong repository', { repository: { id: 1295779810, full_name: 'other/repo' } }],
]) {
  test(`${name} is a visible input error without proposed writes`, () => {
    const result = planIntake(input({ item: item(patch) }));
    assert.ok(result.input_error.length > 0);
    assert.deepEqual(result.label_changes, { remove: [], add: [] });
  });
}

test('malformed reviewed collection preserves unknown intake', () => {
  const result = planIntake(input({ reviewed_decisions: {} }));
  assert.equal(result.affiliation, 'unverified');
  assert.ok(result.diagnostics.includes('reviewed_decisions_malformed'));
});

test('a sparse label array remains an invalid input without proposed writes', () => {
  const result = planIntake(input({ item: item({ labels: Array(1) }) }));
  assert.ok(result.input_error.includes('invalid_labels'));
  assert.deepEqual(result.label_changes, { remove: [], add: [] });
});

for (const metadata of [
  { author_association: 'MEMBER' }, { company: 'NVIDIA' },
  { commit_email: 'forged@nvidia.com' }, { codeowners: ['outside-user'] },
  { team_membership: 'maintainer', organization_membership: true },
]) {
  test('item access and profile fields do not substitute for authenticated affiliation evidence: ' + JSON.stringify(metadata), () => {
    const result = planIntake(input({ item: item({ ...metadata, user: { ...item().user, ...metadata } }) }));
    assert.equal(result.affiliation, 'unverified');
    assert.deepEqual(result.label_changes.add, ['affiliation/unverified']);
  });
}
