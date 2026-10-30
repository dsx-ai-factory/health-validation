// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

const REPOSITORY_ID = 1295779810;
const REPOSITORY = 'dsx-ai-factory/health-validation';
const MANAGED_LABELS = ['nvidia', 'external', 'unverified', 'bot'].map(value => `affiliation/${value}`);
const ORGANIZATIONS = { 'dsx-ai-factory': 311157577, NVIDIA: 1728152 };
const isRecord = value => value !== null && typeof value === 'object' && !Array.isArray(value);
const positiveInteger = value => Number.isSafeInteger(value) && value > 0;
const nonempty = value => typeof value === 'string' && value.trim().length > 0;
const validLogin = value => typeof value === 'string' && /^[A-Za-z0-9][A-Za-z0-9-]{0,38}$/.test(value) && value.toLowerCase() !== 'ghost';
const normalizeLogin = value => typeof value === 'string' ? value.toLowerCase() : null;
// GitHub IDs are opaque; legacy IDs may contain padded Base64 characters.
const validNode = value => typeof value === 'string' && value.length > 0 && value.length <= 512 && !/[\s\u0000-\u001f\u007f]/u.test(value);

// Date.parse normalizes impossible calendar dates; verify components first.
function timestamp(value) {
  if (typeof value !== 'string') return null;
  const match = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.(\d+))?(Z|[+-]\d{2}:\d{2})$/.exec(value);
  if (!match) return null;
  const [, yearText, monthText, dayText, hourText, minuteText, secondText, , offset] = match;
  const [year, month, day, hour, minute, second] = [yearText, monthText, dayText, hourText, minuteText, secondText].map(Number);
  const leap = year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0);
  const days = [31, leap ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31];
  if (year < 1 || month < 1 || month > 12 || day < 1 || day > days[month - 1] || hour > 23 || minute > 59 || second > 59) return null;
  if (offset !== 'Z' && (Number(offset.slice(1, 3)) > 23 || Number(offset.slice(4, 6)) > 59)) return null;
  const result = Date.parse(value);
  return Number.isFinite(result) ? result : null;
}

function authorIdentity(user) {
  if (isRecord(user) && user.type === 'Bot') return { human: false, bot: true, id: user.id, login: user.login };
  if (!isRecord(user) || !positiveInteger(user.id)) return { human: false, bot: false };
  const human = user.type === 'User' && validLogin(user.login);
  return { human, bot: false, id: user.id, login: user.login };
}

function reviewedAffiliation(author, reviews, now, diagnostics) {
  const relevant = reviews.filter(review => isRecord(review) && (
    review.author_id === author.id ||
    (typeof review.author_id === 'string' && review.author_id === String(author.id)) ||
    normalizeLogin(review.login) === normalizeLogin(author.login)
  ));
  const current = [];
  let malformed = false;
  for (const review of relevant) {
    const reviewed = timestamp(review.reviewed_at);
    const expires = timestamp(review.expires_at);
    if (!positiveInteger(review.author_id) || !validLogin(review.login) || !['nvidia', 'external'].includes(review.affiliation) ||
        !nonempty(review.reviewed_by) || !nonempty(review.evidence_ref) || !nonempty(review.revision) ||
        reviewed === null || expires === null || reviewed > now || expires <= reviewed) {
      malformed = true;
    } else if (expires <= now) {
      diagnostics.push('review_expired');
    } else {
      current.push(review);
    }
  }
  if (malformed) {
    diagnostics.push('review_malformed');
    return 'unverified';
  }
  if (current.some(review => review.author_id !== author.id)) {
    diagnostics.push('review_identity_mismatch');
    return 'unverified';
  }
  if (current.length > 1) {
    diagnostics.push('review_conflict');
    return 'unverified';
  }
  if (current.length === 0) return null;
  if (normalizeLogin(current[0].login) !== normalizeLogin(author.login)) diagnostics.push('review_login_changed');
  return current[0].affiliation;
}

export function validateReviewedDecisions(reviews, evaluatedAt) {
  const now = timestamp(evaluatedAt);
  if (now === null) throw new TypeError('evaluated_at must be a valid RFC 3339 timestamp');
  if (!Array.isArray(reviews)) return [{ code: 'registry_not_array' }];
  const errors = [], ids = new Set(), logins = new Set();
  for (const [index, review] of reviews.entries()) {
    const diagnostics = [];
    const affiliation = reviewedAffiliation({ id: review?.author_id, login: review?.login }, [review], now, diagnostics);
    if (!['nvidia', 'external'].includes(affiliation)) {
      errors.push({ record: index + 1, code: diagnostics[0] ?? 'review_malformed' });
    } else if (ids.has(review.author_id) || logins.has(normalizeLogin(review.login))) {
      errors.push({ record: index + 1, code: 'review_conflict' });
    }
    ids.add(review?.author_id);
    logins.add(normalizeLogin(review?.login));
  }
  return errors;
}

function itemErrors(item) {
  if (!isRecord(item)) return ['item_not_object'];
  const errors = [];
  if (!isRecord(item.repository) || item.repository.id !== REPOSITORY_ID || item.repository.full_name !== REPOSITORY) errors.push('repository_out_of_scope');
  if (!validNode(item.node_id)) errors.push('invalid_node_id');
  if (!positiveInteger(item.number)) errors.push('invalid_number');
  if (!['issue', 'pr'].includes(item.kind)) errors.push('invalid_kind');
  if (!['open', 'closed'].includes(item.state)) errors.push('invalid_state');
  if ((item.kind === 'pr' || item.draft !== undefined) && typeof item.draft !== 'boolean') errors.push('invalid_draft');
  if (timestamp(item.created_at) === null) errors.push('invalid_created_at');
  if (!Array.isArray(item.labels) || !Array.from(item.labels).every(nonempty) || new Set(item.labels).size !== item.labels.length) errors.push('invalid_labels');
  return errors;
}

export function planIntake(input) {
  if (!isRecord(input)) throw new TypeError('input must be an object');
  const now = timestamp(input.evaluated_at);
  if (now === null) throw new TypeError('evaluated_at must be a valid RFC 3339 timestamp');
  const item = input.item;
  const decision = { input_error: itemErrors(item), affiliation: null, label_changes: { remove: [], add: [] }, diagnostics: [] };
  if (decision.input_error.length) return decision;
  const reviews = Array.isArray(input.reviewed_decisions) ? input.reviewed_decisions : [];
  const diagnostics = input.reviewed_decisions !== undefined && !Array.isArray(input.reviewed_decisions)
    ? ['reviewed_decisions_malformed'] : [];
  const author = authorIdentity(item.user);
  let affiliation = 'unverified';
  if (author.bot) {
    affiliation = 'bot';
  } else if (author.human) {
    const reviewed = reviewedAffiliation(author, reviews, now, diagnostics);
    affiliation = reviewed ?? 'unverified';
    const membership = input.membership;
    const confirmed = isRecord(membership) && membership.author_id === author.id &&
      Object.hasOwn(ORGANIZATIONS, membership.organization) &&
      membership.organization_id === ORGANIZATIONS[membership.organization];
    if (membership != null && !confirmed) diagnostics.push('membership_invalid');
    if (confirmed && reviewed === 'external') {
      affiliation = 'unverified';
      diagnostics.push('affiliation_conflict');
    } else if (confirmed && reviewed !== 'unverified' && !diagnostics.includes('reviewed_decisions_malformed')) {
      affiliation = 'nvidia';
    }
  } else {
    diagnostics.push('author_unavailable');
  }
  const selectedLabel = `affiliation/${affiliation}`;
  const remove = item.labels.filter(label => MANAGED_LABELS.includes(label) && label !== selectedLabel);
  const add = item.labels.includes(selectedLabel) ? [] : [selectedLabel];
  return { ...decision, affiliation, label_changes: { remove, add }, diagnostics: [...new Set(diagnostics)] };
}
