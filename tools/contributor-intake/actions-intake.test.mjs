// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

import test from 'node:test';
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { mkdtempSync, writeFileSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { openingFromActions, prepareActionsOpening, createGitHubClient } from './actions-intake.mjs';

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
    assert.deepEqual(await prepareActionsOpening(args(patch), github), { status: 'ignored' });
  }
  assert.deepEqual(github.writes, []);
});

test('classification returns only a decision and writes affiliation without other metadata', async () => {
  const github = api();
  const result = await prepareActionsOpening(args(), github);
  assert.equal(result.status, 'classified');
  assert.equal(result.decision.affiliation, 'unverified');
  assert.deepEqual(Object.keys(result).sort(), ['decision', 'status']);
  assert.deepEqual(github.writes, [{ op: 'add', number: 17, labels: ['affiliation/unverified'] }]);
});

test('fresh reviewed metadata corrects affiliation while preserving unrelated labels and assignees', async () => {
  const current = raw({ labels: [{ name: 'affiliation/unverified' }, { name: 'bug' }, { name: 'triage/alert-requested' }] });
  const before = structuredClone(current);
  const github = api(current);
  const result = await prepareActionsOpening(args({ reviewedDecisions: [review()] }), github);
  assert.equal(result.decision.affiliation, 'external');
  assert.deepEqual(github.writes, [
    { op: 'add', number: 17, labels: ['affiliation/external'] },
    { op: 'remove', number: 17, label: 'affiliation/unverified' },
  ]);
  assert.deepEqual(current, before);
  assert.equal(JSON.stringify(result).includes('synthetic-private-evidence'), false);
  assert.equal(JSON.stringify(result).includes('synthetic-reviewer'), false);
});

test('repeat classification with an existing old marker performs no label writes', async () => {
  const github = api(raw({ labels: [{ name: 'affiliation/unverified' }, { name: 'triage/alert-requested' }] }));
  assert.equal((await prepareActionsOpening(args(), github)).status, 'classified');
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
    const result = await prepareActionsOpening(args({ reviewedDecisions: reviews }), github);
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
    await assert.rejects(prepareActionsOpening(args(), github), /identity|scope|metadata/);
    assert.deepEqual(github.writes, []);
  });
}

test('a failed affiliation write is surfaced', async () => {
  const github = api(); github.addLabels = async () => { throw new Error('synthetic write failure'); };
  await assert.rejects(prepareActionsOpening(args(), github), /write failure/);
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
  for (const label of ['bug', 'triage/alert-requested', 'affiliation/unknown']) {
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
    await assert.rejects(prepareActionsOpening(args(), github), error =>
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
  await assert.rejects(prepareActionsOpening(args(), github), error =>
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
  const result = await prepareActionsOpening(args(), github);
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
    payload = event(), obsoleteEnvironment = {} } = {}) {
  const directory = mkdtempSync(join(tmpdir(), 'intake-actions-cli-'));
  try {
    const eventPath = join(directory, 'event.json');
    const outputPath = join(directory, 'output');
    const callsPath = join(directory, 'calls');
    writeFileSync(eventPath, JSON.stringify(payload));
    writeFileSync(outputPath, ''); writeFileSync(callsPath, '');
    const preload = 'data:text/javascript,' + encodeURIComponent(String.raw`
      import {appendFileSync} from 'node:fs';
      import https from 'node:https';
      import {syncBuiltinESMExports} from 'node:module';
      https.request = url => {
        appendFileSync(process.env.TEST_API_CALLS, JSON.stringify({url, method: 'HTTPS'}) + '\n');
        throw new Error('unexpected HTTPS transport');
      };
      syncBuiltinESMExports();
      globalThis.fetch = async (url, options) => {
        appendFileSync(process.env.TEST_API_CALLS, JSON.stringify({url, method: options.method,
          payload: options.body ? JSON.parse(options.body) : null}) + '\n');
        if (!url.startsWith('https://api.github.com/repos/dsx-ai-factory/health-validation/')) throw new Error('unexpected fetch URL');
        return {ok: true, status: 200, json: async () => options.method === 'POST'
          ? JSON.parse(options.body).labels.map(name => ({name})) : options.method === 'DELETE'
          ? [] : JSON.parse(process.env.TEST_CURRENT_ITEM)};
      };
    `);
    const env = { ...nodeRuntimeEnvironment(), HEALTH_VALIDATION_APPLY_LABELS: apply, GITHUB_TOKEN: githubToken,
      GITHUB_EVENT_NAME: eventName, GITHUB_EVENT_PATH: eventPath, GITHUB_OUTPUT: outputPath,
      TEST_API_CALLS: callsPath, TEST_CURRENT_ITEM: JSON.stringify(current), ...obsoleteEnvironment };
    if (registry !== null) env.HEALTH_VALIDATION_REVIEWED_DECISIONS_JSON = registry;
    const script = fileURLToPath(new URL('./actions-intake.mjs', import.meta.url));
    const run = spawnSync(process.execPath, ['--import', preload, script, mode], {
      env, encoding: 'utf8', timeout: 5000,
    });
    return { ...run, output: readFileSync(outputPath, 'utf8'),
      calls: readFileSync(callsPath, 'utf8').trim().split('\n').filter(Boolean).map(line => JSON.parse(line)) };
  } finally { rmSync(directory, { recursive: true, force: true }); }
}

for (const [name, routingJson, slackToken] of [
  ['invalid configuration', 'PRIVATE_OBSOLETE_ROUTING', ''],
  ['valid configuration', '{"channel_id":"C0123456789","mention_user_ids":["U0123456789"]}', 'synthetic-obsolete-token'],
]) {
  test('classification ignores obsolete enabled alerts and Slack configuration with zero Slack I/O: ' + name, () => {
    const run = runIntakeCli({ obsoleteEnvironment: {
      HEALTH_VALIDATION_ALERTS_ENABLED: 'true', HEALTH_VALIDATION_INTAKE_ROUTING_JSON: routingJson,
      HEALTH_VALIDATION_SLACK_BOT_TOKEN: slackToken,
    } });
    assert.equal(run.status, 0, run.stderr);
    assert.deepEqual(run.calls.map(call => ({ url: call.url, method: call.method })), [
      { url: 'https://api.github.com/repos/dsx-ai-factory/health-validation/pulls/17', method: 'GET' },
      { url: 'https://api.github.com/repos/dsx-ai-factory/health-validation/issues/17/labels', method: 'POST' },
    ]);
    assert.deepEqual(run.calls[1].payload.labels, ['affiliation/unverified']);
    assert.equal(run.output, '');
    assert.match(run.stdout, /classified/);
    assert.equal((run.stdout + run.stderr).includes('PRIVATE_OBSOLETE_ROUTING'), false);
  });
}

test('classification CLI preserves an existing old marker without a failure', () => {
  const run = runIntakeCli({ current: raw({ labels: [{ name: 'affiliation/unverified' }, { name: 'triage/alert-requested' }] }) });
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
