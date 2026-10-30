// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

import test from 'node:test';
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { mkdtempSync, writeFileSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { openingFromActions, classifyFromActions, createGitHubClient } from './actions-intake.mjs';

const REPO = { id: 1295779810, full_name: 'dsx-ai-factory/health-validation' };
const NODE_RUNTIME_KEYS = ['JS_BINARY__NODE_BINARY', 'JS_BINARY__NODE_PATCHES', 'JS_BINARY__NODE_WRAPPER',
  'JS_BINARY__PATCH_NODE_FS', 'JS_BINARY__FS_PATCH_ROOTS'];
const nodeRuntimeEnvironment = () => Object.fromEntries(NODE_RUNTIME_KEYS
  .filter(key => typeof process.env[key] === 'string').map(key => [key, process.env[key]]));

const NOW = '2026-10-02T20:00:00Z';
const raw = (patch = {}) => ({ node_id: 'PR_example', number: 17, state: 'open', draft: true,
  created_at: NOW, user: { id: 1001, login: 'outside-user', type: 'User' },
  labels: [{ name: 'bug' }], assignees: [{ login: 'maintainer', id: 10 }], base: { repo: REPO }, ...patch });
const event = (patch = {}) => ({ action: 'opened', repository: REPO, pull_request: raw(), ...patch });
const review = (patch = {}) => ({ author_id: 1001, login: 'outside-user', affiliation: 'external',
  reviewed_by: 'synthetic-reviewer', evidence_ref: 'synthetic-private-evidence', revision: 'test-1',
  reviewed_at: '2026-10-02T19:00:00Z', expires_at: '2026-10-03T19:00:00Z', ...patch });
function api(current = raw()) {
  const writes = [];
  return { writes, readItem: async () => structuredClone(current),
    addLabels: async (number, labels) => { writes.push({ op: 'add', number, labels }); },
    removeLabel: async (number, label) => { writes.push({ op: 'remove', number, label }); } };
}
const args = (patch = {}) => ({ eventName: 'pull_request_target', payload: event(),
  evaluatedAt: NOW, reviewedDecisions: [], ...patch });

const membership = (org = 'dsx-ai-factory', overrides = {}) => ({ state: 'active', user: raw().user,
  organization: { login: org, id: org === 'NVIDIA' ? 1728152 : 311157577 }, ...overrides });
const membershipTokens = { 'dsx-ai-factory': 'synthetic-dsx-token', NVIDIA: 'synthetic-nvidia-token' };

test('membership lookup uses separate read credentials and stable account IDs', async () => {
  const calls = [];
  const github = createGitHubClient({ token: 'synthetic-write-token', membershipTokens,
    fetchFn: async (url, options) => {
      calls.push({ url, authorization: options.headers.Authorization, method: options.method });
      return { ok: true, status: 200, json: async () => membership() };
    } });
  assert.deepEqual(await github.findNvidiaMembership(raw().user), {
    membership: { author_id: 1001, organization: 'dsx-ai-factory', organization_id: 311157577 }, unavailable: false,
  });
  assert.deepEqual(calls, [{ url: 'https://api.github.com/orgs/dsx-ai-factory/memberships/outside-user',
    authorization: 'Bearer synthetic-dsx-token', method: 'GET' }]);
});

test('NVIDIA-only membership is recognized after DSX does not return a match', async () => {
  const calls = [];
  const github = createGitHubClient({ token: 'synthetic-write-token', membershipTokens,
    fetchFn: async (url, options) => {
      calls.push({ url, authorization: options.headers.Authorization });
      return url.includes('/dsx-ai-factory/') ? { ok: false, status: 404 }
        : { ok: true, status: 200, json: async () => membership('NVIDIA') };
    } });
  assert.equal((await github.findNvidiaMembership(raw().user)).membership.organization, 'NVIDIA');
  assert.equal(calls.length, 2);
  assert.equal(calls[1].authorization, 'Bearer synthetic-nvidia-token');
});

for (const state of ['pending', 'missing']) {
  test(`${state} membership does not establish NVIDIA or external status`, async () => {
    const github = createGitHubClient({ token: 'synthetic-write-token', membershipTokens,
      fetchFn: async url => state === 'missing' ? { ok: false, status: 404 }
        : { ok: true, status: 200, json: async () => membership(url.includes('/NVIDIA/') ? 'NVIDIA' : 'dsx-ai-factory', { state }) } });
    assert.deepEqual(await github.findNvidiaMembership(raw().user), { membership: null, unavailable: false });
  });
}

for (const changes of [{ user: { ...raw().user, id: 999 } }, { organization: { login: 'dsx-ai-factory', id: 1 } },
  { organization: { login: 'other', id: 311157577 } }, { state: 'invalid' }, { user: null }]) {
  test(`malformed or mismatched GitHub membership cannot transfer affiliation: ${JSON.stringify(changes)}`, async () => {
    const github = createGitHubClient({ token: 'synthetic-write-token', membershipTokens,
      fetchFn: async () => ({ ok: true, status: 200, json: async () => membership('dsx-ai-factory', changes) }) });
    assert.deepEqual(await github.findNvidiaMembership(raw().user), { membership: null, unavailable: true });
  });
}

test('missing membership credentials do not use the repository token for organization reads', async () => {
  const github = createGitHubClient({ token: 'synthetic-write-token', fetchFn: () => assert.fail('unexpected request') });
  assert.deepEqual(await github.findNvidiaMembership(raw().user), { membership: null, unavailable: true });
});

test('invalid author identities do not become arbitrary organization request paths', async () => {
  const github = createGitHubClient({ token: 'synthetic-write-token', membershipTokens,
    fetchFn: () => assert.fail('unexpected request') });
  for (const user of [null, { id: 1, login: '../other', type: 'User' }, { id: '1', login: 'user', type: 'User' },
    { id: 1, login: 'dependabot[bot]', type: 'Bot' }]) {
    assert.equal((await github.findNvidiaMembership(user)).membership, null);
  }
});

test('membership errors are bounded and cannot create external affiliation', async () => {
  let calls = 0;
  const github = createGitHubClient({ token: 'synthetic-write-token', membershipTokens, sleepFn: async () => {},
    fetchFn: async () => { calls++; return { ok: false, status: 503, json: () => { throw new Error('PRIVATE_BODY'); } }; } });
  assert.deepEqual(await github.findNvidiaMembership(raw().user), { membership: null, unavailable: true });
  assert.equal(calls, 6);
});

test('an unavailable first organization does not hide a valid second organization match', async () => {
  const github = createGitHubClient({ token: 'synthetic-write-token', membershipTokens,
    fetchFn: async url => url.includes('/dsx-ai-factory/') ? { ok: false, status: 403 }
      : { ok: true, status: 200, json: async () => membership('NVIDIA') } });
  assert.equal((await github.findNvidiaMembership(raw().user)).membership.organization, 'NVIDIA');
});

test('membership rate-limit waits above ten seconds do not delay the second organization', async () => {
  const github = createGitHubClient({ token: 'synthetic-write-token', membershipTokens,
    sleepFn: () => assert.fail('unexpected long wait'), fetchFn: async url => url.includes('/dsx-ai-factory/')
      ? { ok: false, status: 429, headers: new Headers({ 'retry-after': '11' }) }
      : { ok: true, status: 200, json: async () => membership('NVIDIA') } });
  assert.equal((await github.findNvidiaMembership(raw().user)).membership.organization, 'NVIDIA');
});

test('bounded transport failures in one organization do not prevent the second lookup', async () => {
  const waits = [];
  let calls = 0;
  const github = createGitHubClient({ token: 'synthetic-write-token', membershipTokens,
    sleepFn: async ms => { waits.push(ms); }, fetchFn: async url => {
      calls++;
      if (url.includes('/dsx-ai-factory/')) throw new Error('PRIVATE_TIMEOUT_DETAIL');
      return { ok: true, status: 200, json: async () => membership('NVIDIA') };
    } });
  const result = await github.findNvidiaMembership(raw().user);
  assert.equal(result.membership.organization, 'NVIDIA');
  assert.equal(calls, 4);
  assert.deepEqual(waits, [1000, 2000]);
  assert.equal(JSON.stringify(result).includes('PRIVATE_TIMEOUT_DETAIL'), false);
});

test('a missing DSX token still permits a separately authorized NVIDIA lookup', async () => {
  const calls = [];
  const github = createGitHubClient({ token: 'synthetic-write-token', membershipTokens: { NVIDIA: membershipTokens.NVIDIA },
    fetchFn: async url => { calls.push(url); return { ok: true, status: 200, json: async () => membership('NVIDIA') }; } });
  assert.equal((await github.findNvidiaMembership(raw().user)).membership.organization, 'NVIDIA');
  assert.deepEqual(calls, ['https://api.github.com/orgs/NVIDIA/memberships/outside-user']);
});

test('classification uses authenticated current-author membership and preserves unrelated metadata', async () => {
  const current = raw({ labels: [{ name: 'bug' }, { name: 'affiliation/unverified' }],
    user: { id: 1002, login: 'renamed-user', type: 'User' } });
  const github = api(current);
  github.findNvidiaMembership = async user => {
    assert.deepEqual(user, current.user);
    return { membership: { author_id: 1002, organization: 'NVIDIA', organization_id: 1728152 }, unavailable: false };
  };
  const result = await classifyFromActions(args(), github);
  assert.equal(result.decision.affiliation, 'nvidia');
  assert.deepEqual(github.writes, [
    { op: 'add', number: 17, labels: ['affiliation/nvidia'] },
    { op: 'remove', number: 17, label: 'affiliation/unverified' },
  ]);
});

test('unavailable lookup still labels unverified or uses an explicit reviewed exception', async () => {
  for (const reviewedDecisions of [[], [review()]]) {
    const github = api();
    github.findNvidiaMembership = async () => { throw new Error('PRIVATE_MEMBERSHIP_FAILURE'); };
    const result = await classifyFromActions(args({ reviewedDecisions }), github);
    assert.equal(result.decision.affiliation, reviewedDecisions.length ? 'external' : 'unverified');
    assert.ok(result.decision.diagnostics.includes('membership_unavailable'));
    assert.equal(JSON.stringify(result).includes('PRIVATE_MEMBERSHIP_FAILURE'), false);
  }
});

test('Bots skip membership APIs and keep their dedicated label', async () => {
  const github = api(raw({ user: { id: 1, login: 'dependabot[bot]', type: 'Bot' } }));
  github.findNvidiaMembership = async () => assert.fail('unexpected bot membership lookup');
  assert.equal((await classifyFromActions(args(), github)).decision.affiliation, 'bot');
});

test('opening validation supports issues and draft PRs in the fixed repository', () => {
  const item = openingFromActions('pull_request_target', event());
  assert.equal(item.draft, true);
  assert.equal(item.user.id, 1001);
  assert.equal(item.kind, 'pr');
  assert.deepEqual(item.repository, REPO);
  assert.equal(openingFromActions('issues', event({ issue: raw({ node_id: 'I_example' }) })).kind, 'issue');
  assert.throws(() => openingFromActions('issues', event({ repository: { ...REPO, id: 1 }, issue: raw() })), /scope/);
  assert.throws(() => openingFromActions('pull_request_target', event({ repository: { ...REPO, full_name: 'other/repo' } })), /scope/);
});

test('comments and subsequent PR events do not read or write GitHub', async () => {
  const github = api();
  github.readItem = async () => { throw new Error('unexpected GitHub read'); };
  for (const patch of [{ eventName: 'issue_comment' }, { payload: event({ action: 'synchronize' }) }]) {
    assert.deepEqual(await classifyFromActions(args(patch), github), { status: 'ignored' });
  }
  assert.deepEqual(github.writes, []);
});

test('classification returns only a decision and writes affiliation without other metadata', async () => {
  const github = api();
  const result = await classifyFromActions(args(), github);
  assert.equal(result.status, 'classified');
  assert.equal(result.decision.affiliation, 'unverified');
  assert.deepEqual(Object.keys(result).sort(), ['decision', 'status']);
  assert.deepEqual(github.writes, [{ op: 'add', number: 17, labels: ['affiliation/unverified'] }]);
});

test('fresh reviewed metadata corrects affiliation while preserving unrelated labels and assignees', async () => {
  const current = raw({ labels: [{ name: 'affiliation/unverified' }, { name: 'bug' }, { name: 'priority/P1' }] });
  const before = structuredClone(current);
  const github = api(current);
  const result = await classifyFromActions(args({ reviewedDecisions: [review()] }), github);
  assert.equal(result.decision.affiliation, 'external');
  assert.deepEqual(github.writes, [
    { op: 'add', number: 17, labels: ['affiliation/external'] },
    { op: 'remove', number: 17, label: 'affiliation/unverified' },
  ]);
  assert.deepEqual(current, before);
  assert.equal(JSON.stringify(result).includes('synthetic-private-evidence'), false);
  assert.equal(JSON.stringify(result).includes('synthetic-reviewer'), false);
});

test('repeat classification preserves priority without label writes', async () => {
  const github = api(raw({ labels: [{ name: 'affiliation/unverified' }, { name: 'priority/P1' }] }));
  assert.equal((await classifyFromActions(args(), github)).status, 'classified');
  assert.deepEqual(github.writes, []);
});

test('fresh author account type and numeric evidence determine every affiliation', async () => {
  for (const [current, reviews, affiliation] of [
    [raw(), [review({ affiliation: 'nvidia' })], 'nvidia'],
    [raw(), [review()], 'external'],
    [raw({ user: { id: 9001, login: 'dependabot[bot]', type: 'Bot' } }), [review()], 'bot'],
    [raw({ user: { id: 1002, login: 'outside-user', type: 'User' } }), [review()], 'unverified'],
  ]) {
    const github = api(current);
    const result = await classifyFromActions(args({ reviewedDecisions: reviews }), github);
    assert.equal(result.status, 'classified');
    assert.equal(result.decision.affiliation, affiliation);
    assert.deepEqual(github.writes, [{ op: 'add', number: 17, labels: [`affiliation/${affiliation}`] }]);
  }
});

for (const [name, patch] of [
  ['node ID mismatch', { node_id: 'OTHER' }], ['number mismatch', { number: 18 }],
  ['wrong repository ID', { base: { repo: { ...REPO, id: 1 } } }],
  ['wrong repository name', { base: { repo: { ...REPO, full_name: 'other/repo' } } }],
  ['missing labels', { labels: null }], ['invalid label', { labels: [{ name: null }] }],
  ['invalid creation date', { created_at: '2026-02-31T12:00:00Z' }],
  ['invalid draft', { draft: 'false' }],
]) {
  test(`invalid authoritative metadata (${name}) prevents label writes`, async () => {
    const github = api(raw(patch));
    await assert.rejects(classifyFromActions(args(), github), /identity|scope|metadata/);
    assert.deepEqual(github.writes, []);
  });
}

test('a failed affiliation write is surfaced', async () => {
  const github = api(); github.addLabels = async () => { throw new Error('synthetic write failure'); };
  await assert.rejects(classifyFromActions(args(), github), /write failure/);
});

test('GitHub adapter fixes scope, uses JSON bodies and preserves unrelated labels through additive requests', async () => {
  const calls = [];
  const github = createGitHubClient({ token: 'synthetic-token', fetchFn: async (url, options) => {
    calls.push({ url, method: options.method ?? 'GET', body: options.body });
    return { ok: true, status: 200, json: async () => options.method === 'POST'
      ? JSON.parse(options.body).labels.map(name => ({ name }))
      : options.method === 'DELETE' ? [{ name: 'bug' }, { name: 'affiliation/unverified' }] : raw() };
  } });
  await github.readItem('pr', 17);
  await github.addLabels(17, ['affiliation/unverified']);
  await github.removeLabel(17, 'affiliation/nvidia');
  assert.deepEqual(calls, [
    { url: 'https://api.github.com/repos/dsx-ai-factory/health-validation/pulls/17', method: 'GET', body: undefined },
    { url: 'https://api.github.com/repos/dsx-ai-factory/health-validation/issues/17/labels', method: 'POST', body: '{"labels":["affiliation/unverified"]}' },
    { url: 'https://api.github.com/repos/dsx-ai-factory/health-validation/issues/17/labels/affiliation%2Fnvidia', method: 'DELETE', body: undefined },
  ]);
  await assert.rejects(github.readItem('pr', '17/../../secrets'));
  await assert.rejects(github.removeLabel(17, 'bug'));
  for (const label of ['bug', 'priority/P1', 'affiliation/unknown']) {
    await assert.rejects(github.addLabels(17, [label]), /scope/);
    await assert.rejects(github.removeLabel(17, label), /scope/);
  }
  assert.equal(calls.length, 3);
});

test('failed GitHub responses are reported without response-body or credential leakage', async () => {
  const github = createGitHubClient({ token: 'synthetic-token', fetchFn: async () => ({
    ok: false, status: 403, json: async () => ({ message: 'PRIVATE_RESPONSE' }),
  }) });
  await assert.rejects(github.readItem('pr', 17), error => /403/.test(error.message) && !/PRIVATE|token/.test(error.message));
});

for (const [name, response] of [
  ['missing requested label', [{ name: 'bug' }]], ['empty label array', []],
  ['malformed response', { message: 'PRIVATE_RESPONSE' }], ['malformed label', [{ name: null }]],
]) {
  test(`successful HTTP status with ${name} cannot verify an affiliation write`, async () => {
    const github = createGitHubClient({ token: 'synthetic-token', fetchFn: async () => ({
      ok: true, status: 200, json: async () => response,
    }) });
    await assert.rejects(github.addLabels(17, ['affiliation/unverified']), error =>
      /unverified|reconcile/i.test(error.message) && !error.message.includes('PRIVATE_RESPONSE'));
  });
}

for (const [name, response] of [
  ['retained affiliation', [{ name: 'affiliation/nvidia' }]],
  ['malformed response', { message: 'PRIVATE_REMOVAL_RESPONSE' }],
  ['null response', null], ['malformed label', [{ name: null }]],
]) {
  test(`label removal rejects HTTP-success with ${name} after the affiliation write`, async () => {
    const calls = [];
    const github = createGitHubClient({ token: 'synthetic-token', fetchFn: async (url, options) => {
      calls.push({ method: options.method, body: options.body });
      return { ok: true, status: 200, json: async () => {
        if (options.method === 'GET') return raw({ labels: [{ name: 'affiliation/nvidia' }] });
        if (options.method === 'DELETE') return response;
        return JSON.parse(options.body).labels.map(name => ({ name }));
      } };
    } });
    await assert.rejects(classifyFromActions(args(), github), error =>
      /unverified|reconcile/i.test(error.message) && !error.message.includes('PRIVATE_REMOVAL_RESPONSE'));
    assert.deepEqual(calls.map(call => call.method), ['GET', 'POST', 'DELETE']);
  });
}

test('unreadable label removal response stops after the affiliation write without leaking response data', async () => {
  const calls = [];
  const github = createGitHubClient({ token: 'synthetic-token', fetchFn: async (url, options) => {
    calls.push(options.method);
    return { ok: true, status: 200, json: async () => {
      if (options.method === 'GET') return raw({ labels: [{ name: 'affiliation/nvidia' }] });
      if (options.method === 'DELETE') throw new Error('PRIVATE_REMOVAL_BODY');
      return JSON.parse(options.body).labels.map(name => ({ name }));
    } };
  } });
  await assert.rejects(classifyFromActions(args(), github), error =>
    /invalid|reconcile/i.test(error.message) && !error.message.includes('PRIVATE_REMOVAL_BODY'));
  assert.deepEqual(calls, ['GET', 'POST', 'DELETE']);
});

test('verified remaining labels complete affiliation correction after affiliation removal', async () => {
  const calls = [];
  const github = createGitHubClient({ token: 'synthetic-token', fetchFn: async (url, options) => {
    calls.push(options.method);
    return { ok: true, status: 200, json: async () => {
      if (options.method === 'GET') return raw({ labels: [{ name: 'bug' }, { name: 'affiliation/nvidia' }] });
      if (options.method === 'DELETE') return [{ name: 'bug' }, { name: 'affiliation/unverified' }];
      return JSON.parse(options.body).labels.map(name => ({ name }));
    } };
  } });
  const result = await classifyFromActions(args(), github);
  assert.equal(result.status, 'classified');
  assert.deepEqual(calls, ['GET', 'POST', 'DELETE']);
});

test('only DELETE 404 means an affiliation label is already absent', async () => {
  let jsonReads = 0;
  const github = createGitHubClient({ token: 'synthetic-token', fetchFn: async () => ({
    ok: false, status: 404, json: async () => { jsonReads++; throw new Error('PRIVATE_404_BODY'); },
  }) });
  await github.removeLabel(17, 'affiliation/nvidia');
  await assert.rejects(github.readItem('pr', 17), /404/);
  await assert.rejects(github.addLabels(17, ['affiliation/unverified']), /404/);
  assert.equal(jsonReads, 0);
});

test('a sparse requested label array is refused before a GitHub request', async () => {
  let calls = 0;
  const github = createGitHubClient({ token: 'synthetic-token', fetchFn: async () => {
    calls++;
    return { ok: true, status: 200, json: async () => [{ name: 'affiliation/unverified' }] };
  } });
  await assert.rejects(github.addLabels(17, Array(1)), /scope|invalid/i);
  assert.equal(calls, 0);
});

test('a sparse acknowledged label array cannot prove an affiliation was removed', async () => {
  const github = createGitHubClient({ token: 'synthetic-token', fetchFn: async () => ({
    ok: true, status: 200, json: async () => Array(1),
  }) });
  await assert.rejects(github.removeLabel(17, 'affiliation/nvidia'), /unverified|reconcile/i);
});

function runIntakeCli({ registry = '[]', current = raw(), mode = 'intake',
    githubToken = 'synthetic-github-token', apply = 'true', eventName = 'pull_request_target',
    payload = event(), memberships = {}, membershipCredentials = false } = {}) {
  const directory = mkdtempSync(join(tmpdir(), 'intake-actions-cli-'));
  try {
    const eventPath = join(directory, 'event.json');
    const outputPath = join(directory, 'output');
    const callsPath = join(directory, 'calls');
    writeFileSync(eventPath, JSON.stringify(payload));
    writeFileSync(outputPath, ''); writeFileSync(callsPath, '');
    const preload = 'data:text/javascript,' + encodeURIComponent(String.raw`
      import {appendFileSync} from 'node:fs';
      globalThis.fetch = async (url, options) => {
        appendFileSync(process.env.TEST_API_CALLS, JSON.stringify({url, method: options.method,
          payload: options.body ? JSON.parse(options.body) : null}) + '\n');
        const org = /^https:\/\/api\.github\.com\/orgs\/(dsx-ai-factory|NVIDIA)\/memberships\/[A-Za-z0-9-]+$/.exec(url)?.[1];
        if (org) {
          const response = JSON.parse(process.env.TEST_MEMBERSHIPS)[org] ?? {status: 404};
          return {ok: response.status === 200, status: response.status, json: async () => response.body};
        }
        if (!url.startsWith('https://api.github.com/repos/dsx-ai-factory/health-validation/')) throw new Error('unexpected fetch URL');
        return {ok: true, status: 200, json: async () => options.method === 'POST'
          ? JSON.parse(options.body).labels.map(name => ({name})) : options.method === 'DELETE'
          ? [] : JSON.parse(process.env.TEST_CURRENT_ITEM)};
      };
    `);
    const env = { ...nodeRuntimeEnvironment(), HEALTH_VALIDATION_APPLY_LABELS: apply, GITHUB_TOKEN: githubToken,
      GITHUB_EVENT_NAME: eventName, GITHUB_EVENT_PATH: eventPath, GITHUB_OUTPUT: outputPath,
      TEST_API_CALLS: callsPath, TEST_CURRENT_ITEM: JSON.stringify(current), TEST_MEMBERSHIPS: JSON.stringify(memberships) };
    if (membershipCredentials) Object.assign(env, { HEALTH_VALIDATION_DSX_MEMBERSHIP_TOKEN: 'synthetic-dsx-token',
      HEALTH_VALIDATION_NVIDIA_MEMBERSHIP_TOKEN: 'synthetic-nvidia-token' });
    if (registry !== null) env.HEALTH_VALIDATION_REVIEWED_DECISIONS_JSON = registry;
    const script = fileURLToPath(new URL('./actions-intake.mjs', import.meta.url));
    const run = spawnSync(process.execPath, ['--import', preload, script, mode], {
      env, encoding: 'utf8', timeout: 5000,
    });
    return { ...run, output: readFileSync(outputPath, 'utf8'),
      calls: readFileSync(callsPath, 'utf8').trim().split('\n').filter(Boolean).map(line => JSON.parse(line)) };
  } finally { rmSync(directory, { recursive: true, force: true }); }
}

for (const org of ['dsx-ai-factory', 'NVIDIA']) {
  test(`CLI applies ${org} membership without requiring exception records`, () => {
    const run = runIntakeCli({ registry: null, membershipCredentials: true,
      memberships: { [org]: { status: 200, body: membership(org) } } });
    assert.equal(run.status, 0, run.stderr);
    assert.deepEqual(run.calls.at(-1).payload.labels, ['affiliation/nvidia']);
    assert.equal(run.stderr, '');
    for (const privateValue of ['outside-user', 'synthetic-dsx-token', 'synthetic-nvidia-token']) {
      assert.equal((run.stdout + run.stderr + run.output).includes(privateValue), false);
    }
  });
}

test('CLI keeps membership and an explicit external exception conflict visible', () => {
  const currentReview = review({ reviewed_at: new Date(Date.now() - 60000).toISOString(),
    expires_at: new Date(Date.now() + 3600000).toISOString() });
  const run = runIntakeCli({ registry: JSON.stringify([currentReview]), membershipCredentials: true,
    memberships: { 'dsx-ai-factory': { status: 200, body: membership() } } });
  assert.equal(run.status, 0, run.stderr);
  assert.deepEqual(run.calls.at(-1).payload.labels, ['affiliation/unverified']);
});

test('CLI does not ignore malformed exceptions when membership is positive', () => {
  const run = runIntakeCli({ registry: 'PRIVATE_INVALID_REGISTRY', membershipCredentials: true,
    memberships: { NVIDIA: { status: 200, body: membership('NVIDIA') } } });
  assert.equal(run.status, 0, run.stderr);
  assert.deepEqual(run.calls.at(-1).payload.labels, ['affiliation/unverified']);
  assert.match(run.stderr, /evidence/i);
  assert.equal((run.stdout + run.stderr).includes('PRIVATE_INVALID_REGISTRY'), false);
});

test('CLI token/read failures still create an unverified intake label', () => {
  const run = runIntakeCli({ membershipCredentials: true,
    memberships: { 'dsx-ai-factory': { status: 403 }, NVIDIA: { status: 429 } } });
  assert.equal(run.status, 0, run.stderr);
  assert.deepEqual(run.calls.map(call => call.method), ['GET', 'GET', 'GET', 'POST']);
  assert.deepEqual(run.calls.at(-1).payload.labels, ['affiliation/unverified']);
});

test('classification CLI only reads the item and adds its affiliation label', () => {
  const run = runIntakeCli();
  assert.equal(run.status, 0, run.stderr);
  assert.deepEqual(run.calls.map(call => ({ url: call.url, method: call.method })), [
    { url: 'https://api.github.com/repos/dsx-ai-factory/health-validation/pulls/17', method: 'GET' },
    { url: 'https://api.github.com/repos/dsx-ai-factory/health-validation/issues/17/labels', method: 'POST' },
  ]);
  assert.deepEqual(run.calls[1].payload.labels, ['affiliation/unverified']);
  assert.equal(run.output, '');
  assert.match(run.stdout, /classified/);
});

test('classification CLI preserves priority when affiliation is already correct', () => {
  const run = runIntakeCli({ current: raw({ labels: [{ name: 'affiliation/unverified' }, { name: 'priority/P1' }] }) });
  assert.equal(run.status, 0, run.stderr);
  assert.equal(run.calls.length, 1);
  assert.match(run.stdout, /classified/i);
  assert.equal(run.output, '');
});

for (const apply of ['', 'false', 'TRUE', '1', ' true ']) {
  test(`CLI requires exact explicit label application: ${apply}`, () => {
    const run = runIntakeCli({ apply });
    assert.equal(run.status, 1);
    assert.deepEqual(run.calls, []);
    assert.equal(run.output, '');
    assert.equal(run.stdout, '');
  });
}

test('missing GitHub credential fails before network access', () => {
  const run = runIntakeCli({ githubToken: '' });
  assert.equal(run.status, 1);
  assert.deepEqual(run.calls, []);
  assert.equal(run.output, '');
});

for (const [name, registry] of [
  ['missing', null], ['invalid JSON', 'PRIVATE_REGISTRY_CONTENT'],
  ['wrong collection', '{"PRIVATE_REGISTRY_FIELD":true}'],
  ['malformed relevant record', JSON.stringify([review({ expires_at: null })])],
]) {
  test(`${name} registry warns generically and classifies unknown authors`, () => {
    const run = runIntakeCli({ registry });
    assert.equal(run.status, 0, run.stderr);
    assert.match(run.stderr, /evidence/i);
    assert.deepEqual(run.calls.map(call => call.method), ['GET', 'POST']);
    assert.deepEqual(run.calls.at(-1).payload.labels, ['affiliation/unverified']);
    assert.equal(run.output, '');
    for (const value of ['PRIVATE_REGISTRY', 'synthetic-private-evidence', 'synthetic-reviewer']) {
      assert.equal((run.stdout + run.stderr + run.output).includes(value), false);
    }
  });
}

test('reviewed classification emits no author, private evidence or credentials in Actions output', () => {
  const currentReview = review({ affiliation: 'nvidia',
    reviewed_at: new Date(Date.now() - 60000).toISOString(), expires_at: new Date(Date.now() + 3600000).toISOString() });
  const run = runIntakeCli({ registry: JSON.stringify([currentReview]) });
  assert.equal(run.status, 0, run.stderr);
  assert.deepEqual(run.calls.at(-1).payload.labels, ['affiliation/nvidia']);
  assert.equal(run.output, '');
  for (const value of ['outside-user', 'synthetic-github-token', 'synthetic-private-evidence', 'synthetic-reviewer']) {
    assert.equal((run.stdout + run.stderr).includes(value), false);
  }
});

test('unsupported CLI mode is refused without API access', () => {
  const run = runIntakeCli({ mode: 'unsupported' });
  assert.equal(run.status, 1);
  assert.deepEqual(run.calls, []);
  assert.equal(run.output, '');
});

test('manual classification uses current evidence for an issue or draft PR', async () => {
  for (const kind of ['issue', 'pr']) {
    const github = api(raw({ labels: [{ name: 'bug' }, { name: 'affiliation/unverified' }] }));
    const result = await classifyFromActions(args({ eventName: 'workflow_dispatch',
      payload: { repository: REPO, inputs: { kind, number: '17' } },
      reviewedDecisions: [review()] }), github);
    assert.equal(result.status, 'classified');
    assert.equal(result.decision.affiliation, 'external');
    assert.deepEqual(github.writes, [
      { op: 'add', number: 17, labels: ['affiliation/external'] },
      { op: 'remove', number: 17, label: 'affiliation/unverified' },
    ]);
  }
});

for (const inputs of [
  {}, { kind: 'comment', number: '17' }, { kind: 'pr', number: 17 },
  ...['0', '-1', '1.5', '17x', '17/../../secrets', '9007199254740992']
    .map(number => ({ kind: 'issue', number })),
]) {
  test(`invalid manual target ${JSON.stringify(inputs)} fails before API access`, async () => {
    const github = api();
    github.readItem = async () => { throw new Error('unexpected API access'); };
    await assert.rejects(classifyFromActions(args({ eventName: 'workflow_dispatch',
      payload: { repository: REPO, inputs } }), github), /manual target/);
    assert.deepEqual(github.writes, []);
  });
}

test('manual classification rejects a different repository', async () => {
  const github = api();
  github.readItem = async () => { throw new Error('unexpected API access'); };
  await assert.rejects(classifyFromActions(args({ eventName: 'workflow_dispatch',
    payload: { repository: { ...REPO, id: 1 }, inputs: { kind: 'pr', number: '17' } } }), github), /scope/);
  assert.deepEqual(github.writes, []);
});

test('a PR cannot be classified through the manual issue route', async () => {
  const github = api(raw({ pull_request: { url: 'https://api.github.com/example' } }));
  await assert.rejects(classifyFromActions(args({ eventName: 'workflow_dispatch',
    payload: { repository: REPO, inputs: { kind: 'issue', number: '17' } } }), github), /kind/);
  assert.deepEqual(github.writes, []);
});

test('manual CLI classification fetches authoritative issue metadata', () => {
  const run = runIntakeCli({ eventName: 'workflow_dispatch',
    payload: { repository: REPO, inputs: { kind: 'issue', number: '17' } } });
  assert.equal(run.status, 0, run.stderr);
  assert.equal(run.calls[0]?.url, 'https://api.github.com/repos/dsx-ai-factory/health-validation/issues/17');
  assert.deepEqual(run.calls.at(-1)?.payload.labels, ['affiliation/unverified']);
});

test('a transient removal failure converges to one affiliation without replacing other labels', async () => {
  const labels = new Set(['bug', 'affiliation/unverified']);
  const waits = [];
  let removalAttempts = 0;
  const github = createGitHubClient({ token: 'synthetic-token', sleepFn: async ms => { waits.push(ms); },
    fetchFn: async (url, options) => {
      if (options.method === 'GET') return { ok: true, json: async () => raw({ labels: [...labels].map(name => ({ name })) }) };
      if (options.method === 'POST') JSON.parse(options.body).labels.forEach(name => labels.add(name));
      if (options.method === 'DELETE') {
        if (++removalAttempts === 1) return { ok: false, status: 503 };
        labels.delete('affiliation/unverified');
      }
      return { ok: true, json: async () => [...labels].map(name => ({ name })) };
    } });
  const result = await classifyFromActions(args({ reviewedDecisions: [review()] }), github);
  assert.equal(result.status, 'classified');
  assert.deepEqual([...labels], ['bug', 'affiliation/external']);
  assert.deepEqual(waits, [1000]);
});

test('a temporary network failure is retried without exposing its message', async () => {
  let attempts = 0;
  const waits = [];
  const github = createGitHubClient({ token: 'synthetic-token', sleepFn: async ms => { waits.push(ms); },
    fetchFn: async () => {
      if (++attempts === 1) throw new Error('PRIVATE_NETWORK_DETAIL');
      return { ok: true, json: async () => raw() };
    } });
  assert.equal((await github.readItem('pr', 17)).number, 17);
  assert.equal(attempts, 2);
  assert.deepEqual(waits, [1000]);
});

for (const status of [429, 403]) {
  test(`HTTP ${status} honors the server retry delay`, async () => {
    const waits = [];
    let attempts = 0;
    const github = createGitHubClient({ token: 'synthetic-token', sleepFn: async ms => { waits.push(ms); },
      fetchFn: async () => ++attempts === 1
        ? { ok: false, status, headers: new Headers({ 'retry-after': '2' }) }
        : { ok: true, json: async () => raw() } });
    assert.equal((await github.readItem('issue', 17)).number, 17);
    assert.deepEqual(waits, [2000]);
  });
}

test('transient retries stop after three attempts and keep error bodies private', async () => {
  let attempts = 0;
  const waits = [];
  const github = createGitHubClient({ token: 'synthetic-token', sleepFn: async ms => { waits.push(ms); },
    fetchFn: async () => { attempts++; return { ok: false, status: 502,
      json: async () => { throw new Error('PRIVATE_RESPONSE_BODY'); } }; } });
  await assert.rejects(github.readItem('pr', 17), error =>
    /502/.test(error.message) && !error.message.includes('PRIVATE_RESPONSE_BODY'));
  assert.equal(attempts, 3);
  assert.deepEqual(waits, [1000, 2000]);
});

test('a long server delay fails without retrying too early', async () => {
  let attempts = 0;
  const waits = [];
  const github = createGitHubClient({ token: 'synthetic-token', sleepFn: async ms => { waits.push(ms); },
    fetchFn: async () => { attempts++; return { ok: false, status: 429, headers: new Headers({ 'retry-after': '120' }) }; } });
  await assert.rejects(github.readItem('pr', 17), /429/);
  assert.equal(attempts, 1);
  assert.deepEqual(waits, []);
});
