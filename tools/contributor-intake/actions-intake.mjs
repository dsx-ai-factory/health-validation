// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { setTimeout as sleep } from 'node:timers/promises';
import { planIntake, validateReviewedDecisions } from './intake-plan.mjs';

const REPO = { id: 1295779810, full_name: 'dsx-ai-factory/health-validation' };
const ORGANIZATIONS = [{ login: 'dsx-ai-factory', id: 311157577 }, { login: 'NVIDIA', id: 1728152 }];
const AFFILIATIONS = new Set(['nvidia', 'external', 'unverified', 'bot'].map(value => `affiliation/${value}`));
const isRecord = value => value !== null && typeof value === 'object' && !Array.isArray(value);
const positiveInteger = value => Number.isSafeInteger(value) && value > 0;

class IntakeError extends Error {
  constructor(code, status) {
    const suffix = Number.isInteger(status) && status >= 100 && status <= 599 ? `_HTTP_${status}` : '';
    super(code + suffix);
    this.code = code + suffix;
  }
}

function assertRepositoryScope(repository) {
  if (!isRecord(repository) || repository.id !== REPO.id || repository.full_name !== REPO.full_name) throw new IntakeError('REPOSITORY_OUT_OF_SCOPE');
}

function normalizeItem(raw, kind) {
  if (!isRecord(raw)) throw new IntakeError('ITEM_METADATA_INVALID');
  if (kind === 'issue' && raw.pull_request) throw new IntakeError('ITEM_KIND_MISMATCH');
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
  if (decision.input_error.length) throw new IntakeError('OPENING_METADATA_INVALID');
  return item;
}

export async function classifyFromActions({ eventName, payload, evaluatedAt, reviewedDecisions = [] }, github) {
  let eventItem;
  if (eventName === 'workflow_dispatch') {
    assertRepositoryScope(payload?.repository);
    const { kind, number } = payload?.inputs ?? {};
    if (!['issue', 'pr'].includes(kind) || typeof number !== 'string' || !/^[1-9][0-9]*$/.test(number) ||
        !positiveInteger(Number(number))) throw new IntakeError('MANUAL_TARGET_INVALID');
    eventItem = { kind, number: Number(number) };
  } else {
    eventItem = openingFromActions(eventName, payload);
  }
  if (!eventItem) return { status: 'ignored' };
  const current = normalizeItem(await github.readItem(eventItem.kind, eventItem.number), eventItem.kind);
  if ((eventItem.node_id && current.node_id !== eventItem.node_id) || current.number !== eventItem.number) throw new IntakeError('ITEM_IDENTITY_MISMATCH');
  const input = { evaluated_at: evaluatedAt, item: current, reviewed_decisions: reviewedDecisions };
  let decision = planIntake(input);
  if (decision.input_error.length) throw new IntakeError('ITEM_METADATA_INVALID');
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
  if (typeof token !== 'string' || !token.trim()) throw new IntakeError('GITHUB_CREDENTIAL_UNAVAILABLE');
  const root = `https://api.github.com/repos/${REPO.full_name}`;
  const alreadyAbsent = Symbol('label already absent');
  const labelArray = value => Array.isArray(value) && Array.from(value).every(label => isRecord(label) && typeof label.name === 'string' && label.name.trim());
  const checkedNumber = value => { if (!positiveInteger(value)) throw new IntakeError('ITEM_NUMBER_INVALID'); return value; };
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
        if (attempt === 2) throw new IntakeError('GITHUB_REQUEST_UNAVAILABLE');
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
    if (!response.ok) throw new IntakeError('GITHUB_REQUEST_FAILED', response.status);
    try { return await response.json(); } catch { throw new IntakeError('GITHUB_RESPONSE_INVALID'); }
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
            organization: organization.login, organization_id: organization.id }, unavailable };
        } catch { unavailable = true; }
      }
      return { membership: null, unavailable };
    },
    async readItem(kind, itemNumber) {
      if (!['issue', 'pr'].includes(kind)) throw new IntakeError('ITEM_KIND_INVALID');
      return request(`/${kind === 'pr' ? 'pulls' : 'issues'}/${checkedNumber(itemNumber)}`);
    },
    async addLabels(itemNumber, labels) {
      if (!Array.isArray(labels) || !labels.length || !Array.from(labels).every(label => typeof label === 'string' && AFFILIATIONS.has(label.toLowerCase()))) throw new IntakeError('LABEL_OUT_OF_SCOPE');
      const response = await request(`/issues/${checkedNumber(itemNumber)}/labels`, 'POST', { labels });
      if (!labelArray(response) ||
          !labels.every(name => response.some(label => label.name.toLowerCase() === name.toLowerCase()))) {
        throw new IntakeError('LABEL_WRITE_UNVERIFIED');
      }
      return response;
    },
    async removeLabel(itemNumber, label) {
      if (typeof label !== 'string' || !AFFILIATIONS.has(label.toLowerCase())) throw new IntakeError('LABEL_OUT_OF_SCOPE');
      const response = await request(`/issues/${checkedNumber(itemNumber)}/labels/${encodeURIComponent(label)}`, 'DELETE');
      if (response === alreadyAbsent) return null;
      if (!labelArray(response) || response.some(current => current.name.toLowerCase() === label.toLowerCase())) {
        throw new IntakeError('LABEL_REMOVAL_UNVERIFIED');
      }
      return response;
    },
  };
}

function reportFailure(error) {
  const code = error instanceof IntakeError ? error.code : 'INTAKE_UNEXPECTED_FAILURE';
  process.stderr.write(`::error::Intake step failed: ${code}. Reconcile any attempted label changes.\n`);
  process.exitCode = 1;
}

async function cli() {
  if (process.argv.length !== 3 || process.argv[2] !== 'intake') throw new IntakeError('CLI_ARGUMENTS_INVALID');
  if (process.env.HEALTH_VALIDATION_APPLY_LABELS !== 'true') throw new IntakeError('LABEL_APPLICATION_DISABLED');
  const github = createGitHubClient({ token: process.env.GITHUB_TOKEN, membershipTokens: {
    'dsx-ai-factory': process.env.HEALTH_VALIDATION_DSX_MEMBERSHIP_TOKEN,
    NVIDIA: process.env.HEALTH_VALIDATION_NVIDIA_MEMBERSHIP_TOKEN,
  } });
  const eventPath = process.env.GITHUB_EVENT_PATH;
  if (!eventPath) throw new IntakeError('ACTIONS_EVENT_UNAVAILABLE');
  let payload;
  try { payload = JSON.parse(readFileSync(eventPath, 'utf8')); } catch { throw new IntakeError('ACTIONS_EVENT_INVALID'); }
  const evaluatedAt = new Date().toISOString();
  let reviewedDecisions = [], registryInvalid = false;
  try {
    const parsed = JSON.parse(process.env.HEALTH_VALIDATION_REVIEWED_DECISIONS_JSON || '[]');
    reviewedDecisions = Array.isArray(parsed) ? parsed : null;
    registryInvalid = validateReviewedDecisions(parsed, evaluatedAt).length > 0;
  } catch { registryInvalid = true; reviewedDecisions = null; }
  const result = await classifyFromActions({ eventName: process.env.GITHUB_EVENT_NAME, payload,
    evaluatedAt, reviewedDecisions }, github);
  if (result.status === 'classified') {
    if (registryInvalid) reportFailure(new IntakeError('REVIEWED_DECISIONS_INVALID'));
    if (result.decision.diagnostics.includes('membership_unavailable')) reportFailure(new IntakeError('MEMBERSHIP_UNAVAILABLE'));
  }
  process.stdout.write(`Intake result: ${result.status}.\n`);
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  cli().catch(reportFailure);
}
