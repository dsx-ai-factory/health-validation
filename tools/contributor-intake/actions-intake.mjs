// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { planIntake } from './intake-plan.mjs';

const REPO = { id: 1295779810, full_name: 'dsx-ai-factory/health-validation' };
const AFFILIATIONS = new Set(['nvidia', 'external', 'unverified', 'bot'].map(value => `affiliation/${value}`));
const isRecord = value => value !== null && typeof value === 'object' && !Array.isArray(value);
const positiveInteger = value => Number.isSafeInteger(value) && value > 0;

function assertRepositoryScope(repository) {
  if (!isRecord(repository) || repository.id !== REPO.id || repository.full_name !== REPO.full_name) throw new Error('repository outside intake scope');
}

function normalizeItem(raw, kind) {
  if (!isRecord(raw)) throw new Error('missing item metadata');
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

export async function prepareActionsOpening({ eventName, payload, evaluatedAt, reviewedDecisions = [] }, github) {
  const eventItem = openingFromActions(eventName, payload);
  if (!eventItem) return { status: 'ignored' };
  const current = normalizeItem(await github.readItem(eventItem.kind, eventItem.number), eventItem.kind);
  if (current.node_id !== eventItem.node_id || current.number !== eventItem.number) throw new Error('authoritative item identity mismatch');
  const decision = planIntake({ evaluated_at: evaluatedAt, item: current,
    reviewed_decisions: reviewedDecisions });
  if (decision.input_error.length) throw new Error('invalid authoritative item metadata');
  if (decision.label_changes.add.length) await github.addLabels(current.number, decision.label_changes.add);
  for (const label of decision.label_changes.remove) await github.removeLabel(current.number, label);
  return { status: 'classified', decision };
}

export function createGitHubClient({ token, fetchFn = globalThis.fetch }) {
  if (typeof token !== 'string' || !token.trim()) throw new Error('GitHub credential unavailable');
  const root = `https://api.github.com/repos/${REPO.full_name}`;
  const alreadyAbsent = Symbol('label already absent');
  const labelArray = value => Array.isArray(value) && Array.from(value).every(label => isRecord(label) && typeof label.name === 'string' && label.name.trim());
  const checkedNumber = value => { if (!positiveInteger(value)) throw new Error('invalid item number'); return value; };
  async function request(path, method = 'GET', body) {
    let response;
    try {
      response = await fetchFn(root + path, { method, redirect: 'error', signal: AbortSignal.timeout(10000),
        headers: { Authorization: `Bearer ${token}`, Accept: 'application/vnd.github+json',
          'Content-Type': 'application/json', 'X-GitHub-Api-Version': '2022-11-28' },
        body: body === undefined ? undefined : JSON.stringify(body) });
    } catch { throw new Error('GitHub request unavailable; reconcile any attempted write'); }
    if (method === 'DELETE' && response.status === 404) return alreadyAbsent;
    if (!response.ok) throw new Error(`GitHub request failed (${response.status})`);
    try { return await response.json(); } catch { throw new Error('GitHub response invalid; reconcile any attempted write'); }
  }
  return {
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
  const github = createGitHubClient({ token: process.env.GITHUB_TOKEN });
  const eventPath = process.env.GITHUB_EVENT_PATH;
  if (!eventPath) throw new Error('Actions event unavailable');
  let payload;
  try { payload = JSON.parse(readFileSync(eventPath, 'utf8')); } catch { throw new Error('Actions event invalid'); }
  let reviewedDecisions = [], evidenceWarning = false;
  try {
    const parsed = JSON.parse(process.env.HEALTH_VALIDATION_REVIEWED_DECISIONS_JSON);
    if (!Array.isArray(parsed)) throw new Error('reviewed evidence must be an array');
    reviewedDecisions = parsed;
  } catch { evidenceWarning = true; }
  if (evidenceWarning) warnEvidence();
  const result = await prepareActionsOpening({ eventName: process.env.GITHUB_EVENT_NAME, payload,
    evaluatedAt: new Date().toISOString(), reviewedDecisions }, github);
  if (!evidenceWarning && result.decision?.diagnostics.includes('review_malformed')) warnEvidence();
  process.stdout.write(`Intake result: ${result.status}.\n`);
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  cli().catch(() => { process.stderr.write('Intake step failed. Check permissions/configuration and any attempted label changes.\n'); process.exitCode = 1; });
}
