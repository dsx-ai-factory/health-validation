// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { setTimeout as sleep } from 'node:timers/promises';
import { planIntake } from './intake-plan.mjs';

const REPO = { id: 1295779810, full_name: 'dsx-ai-factory/health-validation' };
const ORGANIZATIONS = [{ login: 'dsx-ai-factory', id: 311157577 }, { login: 'NVIDIA', id: 1728152 }];
const AFFILIATIONS = new Set(['nvidia', 'external', 'unverified', 'bot'].map(value => `affiliation/${value}`));
const isRecord = value => value !== null && typeof value === 'object' && !Array.isArray(value);
const positiveInteger = value => Number.isSafeInteger(value) && value > 0;

function assertRepositoryScope(repository) {
  if (!isRecord(repository) || repository.id !== REPO.id || repository.full_name !== REPO.full_name) throw new Error('repository outside intake scope');
}

function normalizeItem(raw, kind) {
  if (!isRecord(raw)) throw new Error('missing item metadata');
  if (kind === 'issue' && raw.pull_request) throw new Error('item kind mismatch');
  if (kind === 'pr' && raw.base?.repo) assertRepositoryScope(raw.base.repo);
  return { repository: { ...REPO }, node_id: raw.node_id, number: raw.number, kind,
    state: raw.state, draft: kind === 'pr' ? raw.draft : false,
    created_at: raw.created_at, user: raw.user ?? null,
    labels: Array.isArray(raw.labels) ? raw.labels.map(label => typeof label === 'string' ? label : label?.name) : raw.labels };
}

export function openingFromActions(eventName, payload) {
  if (!['issues', 'pull_request_target'].includes(eventName) || payload?.action !== 'opened') return null;
  assertRepositoryScope(payload.repository);
  const kind = eventName === 'issues' ? 'issue' : 'pr';
  const item = normalizeItem(kind === 'pr' ? payload.pull_request : payload.issue, kind);
  const decision = planIntake({ evaluated_at: item.created_at, item });
  if (decision.input_error.length) throw new Error('invalid opening metadata');
  return item;
}

export async function classifyFromActions({ eventName, payload, evaluatedAt, reviewedDecisions = [] }, github) {
  let eventItem;
  if (eventName === 'workflow_dispatch') {
    assertRepositoryScope(payload?.repository);
    const { kind, number } = payload?.inputs ?? {};
    if (!['issue', 'pr'].includes(kind) || typeof number !== 'string' || !/^[1-9][0-9]*$/.test(number) ||
        !positiveInteger(Number(number))) throw new Error('invalid manual target');
    eventItem = { kind, number: Number(number) };
  } else {
    eventItem = openingFromActions(eventName, payload);
  }
  if (!eventItem) return { status: 'ignored' };
  const current = normalizeItem(await github.readItem(eventItem.kind, eventItem.number), eventItem.kind);
  if ((eventItem.node_id && current.node_id !== eventItem.node_id) || current.number !== eventItem.number) throw new Error('authoritative item identity mismatch');
  const input = { evaluated_at: evaluatedAt, item: current, reviewed_decisions: reviewedDecisions };
  let decision = planIntake(input);
  if (decision.input_error.length) throw new Error('invalid authoritative item metadata');
  if (current.user?.type === 'User' && !decision.diagnostics.includes('author_unavailable') && github.findNvidiaMembership) {
    let result;
    try { result = await github.findNvidiaMembership(current.user); } catch { result = { unavailable: true }; }
    decision = planIntake({ ...input, membership: result.membership });
    if (result.unavailable) decision.diagnostics.push('membership_unavailable');
  }
  if (decision.label_changes.add.length) await github.addLabels(current.number, decision.label_changes.add);
  for (const label of decision.label_changes.remove) await github.removeLabel(current.number, label);
  return { status: 'classified', decision };
}

export function createGitHubClient({ token, membershipTokens = {}, fetchFn = globalThis.fetch, sleepFn = sleep }) {
  if (typeof token !== 'string' || !token.trim()) throw new Error('GitHub credential unavailable');
  const root = `https://api.github.com/repos/${REPO.full_name}`;
  const alreadyAbsent = Symbol('label already absent');
  const labelArray = value => Array.isArray(value) && Array.from(value).every(label => isRecord(label) && typeof label.name === 'string' && label.name.trim());
  const checkedNumber = value => { if (!positiveInteger(value)) throw new Error('invalid item number'); return value; };
  async function request(path, method = 'GET', body, context = {}) {
    const { credential = token, base = root, allowMissing = false, maxRetryDelayMs = 60000 } = context;
    let response;
    for (let attempt = 0; attempt < 3; attempt++) {
      try {
        response = await fetchFn(base + path, { method, redirect: 'error', signal: AbortSignal.timeout(10000),
          headers: { Authorization: `Bearer ${credential}`, Accept: 'application/vnd.github+json',
            'Content-Type': 'application/json', 'X-GitHub-Api-Version': '2022-11-28' },
          body: body === undefined ? undefined : JSON.stringify(body) });
      } catch {
        if (attempt === 2) throw new Error('GitHub request unavailable; reconcile any attempted write');
        await sleepFn(1000 * 2 ** attempt);
        continue;
      }
      const retryAfter = response.headers?.get('retry-after');
      const limited = response.status === 429 || (response.status === 403 &&
        (retryAfter != null || response.headers?.get('x-ratelimit-remaining') === '0'));
      if (attempt === 2 || (!limited && !(response.status >= 500 && response.status <= 599))) break;
      let delay = limited ? 60000 : 1000 * 2 ** attempt;
      if (retryAfter != null) {
        delay = /^[0-9]+$/.test(retryAfter) ? Number(retryAfter) * 1000 : NaN;
      } else if (limited && response.headers?.get('x-ratelimit-reset')) {
        const reset = response.headers.get('x-ratelimit-reset');
        delay = /^[0-9]+$/.test(reset) ? Math.max(1000, Number(reset) * 1000 - Date.now()) : NaN;
      }
      // Longer rate limits need a new run; never retry before GitHub permits it.
      if (!Number.isFinite(delay) || delay > maxRetryDelayMs) break;
      await sleepFn(delay);
    }
    if (method === 'DELETE' && response.status === 404) return alreadyAbsent;
    if (method === 'GET' && allowMissing && response.status === 404) return null;
    if (!response.ok) throw new Error(`GitHub request failed (${response.status})`);
    try { return await response.json(); } catch { throw new Error('GitHub response invalid; reconcile any attempted write'); }
  }
  return {
    async findNvidiaMembership(user) {
      if (!isRecord(user) || !positiveInteger(user.id) || user.type !== 'User' ||
          typeof user.login !== 'string' || !/^[A-Za-z0-9][A-Za-z0-9-]{0,38}$/.test(user.login) ||
          user.login.toLowerCase() === 'ghost') return { membership: null, unavailable: false };
      let unavailable = false;
      for (const organization of ORGANIZATIONS) {
        const credential = membershipTokens?.[organization.login];
        if (typeof credential !== 'string' || !credential.trim()) { unavailable = true; continue; }
        try {
          const result = await request(`/memberships/${encodeURIComponent(user.login)}`, 'GET', undefined,
            { credential, base: `https://api.github.com/orgs/${organization.login}`, allowMissing: true, maxRetryDelayMs: 10000 });
          if (result === null) continue;
          if (!isRecord(result) || result.user?.id !== user.id || result.user?.type !== 'User' ||
              result.organization?.id !== organization.id || result.organization?.login !== organization.login ||
              !['active', 'pending'].includes(result.state)) throw new Error('invalid membership response');
          if (result.state === 'active') return { membership: { author_id: user.id,
            organization: organization.login, organization_id: organization.id }, unavailable: false };
        } catch { unavailable = true; }
      }
      return { membership: null, unavailable };
    },
    async readItem(kind, itemNumber) {
      if (!['issue', 'pr'].includes(kind)) throw new Error('invalid item kind');
      return request(`/${kind === 'pr' ? 'pulls' : 'issues'}/${checkedNumber(itemNumber)}`);
    },
    async addLabels(itemNumber, labels) {
      if (!Array.isArray(labels) || !labels.length || !Array.from(labels).every(label => AFFILIATIONS.has(label))) throw new Error('label outside intake scope');
      const response = await request(`/issues/${checkedNumber(itemNumber)}/labels`, 'POST', { labels });
      if (!labelArray(response) ||
          !labels.every(name => response.some(label => label.name === name))) {
        throw new Error('GitHub label write unverified; reconcile attempted write');
      }
      return response;
    },
    async removeLabel(itemNumber, label) {
      if (!AFFILIATIONS.has(label)) throw new Error('label outside intake scope');
      const response = await request(`/issues/${checkedNumber(itemNumber)}/labels/${encodeURIComponent(label)}`, 'DELETE');
      if (response === alreadyAbsent) return null;
      if (!labelArray(response) || response.some(current => current.name === label)) {
        throw new Error('GitHub label removal unverified; reconcile attempted write');
      }
      return response;
    },
  };
}

const warnEvidence = () => process.stderr.write('Affiliation evidence unavailable or malformed; unresolved authors remain unverified.\n');

async function cli() {
  if (process.argv.length !== 3 || process.argv[2] !== 'intake') throw new Error('use intake');
  if (process.env.HEALTH_VALIDATION_APPLY_LABELS !== 'true') throw new Error('label application is disabled');
  const github = createGitHubClient({ token: process.env.GITHUB_TOKEN, membershipTokens: {
    'dsx-ai-factory': process.env.HEALTH_VALIDATION_DSX_MEMBERSHIP_TOKEN,
    NVIDIA: process.env.HEALTH_VALIDATION_NVIDIA_MEMBERSHIP_TOKEN,
  } });
  const eventPath = process.env.GITHUB_EVENT_PATH;
  if (!eventPath) throw new Error('Actions event unavailable');
  let payload;
  try { payload = JSON.parse(readFileSync(eventPath, 'utf8')); } catch { throw new Error('Actions event invalid'); }
  let reviewedDecisions = [], evidenceWarning = false;
  try {
    const parsed = JSON.parse(process.env.HEALTH_VALIDATION_REVIEWED_DECISIONS_JSON || '[]');
    if (!Array.isArray(parsed)) throw new Error('reviewed evidence must be an array');
    reviewedDecisions = parsed;
  } catch { evidenceWarning = true; reviewedDecisions = null; }
  const result = await classifyFromActions({ eventName: process.env.GITHUB_EVENT_NAME, payload,
    evaluatedAt: new Date().toISOString(), reviewedDecisions }, github);
  if (evidenceWarning || result.decision?.diagnostics.some(code => ['review_malformed', 'membership_unavailable'].includes(code))) warnEvidence();
  process.stdout.write(`Intake result: ${result.status}.\n`);
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  cli().catch(() => { process.stderr.write('Intake step failed. Check permissions/configuration and any attempted label changes.\n'); process.exitCode = 1; });
}
