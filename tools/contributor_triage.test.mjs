import assert from 'node:assert/strict';
import test from 'node:test';

import {
  EXTERNAL_LABEL,
  MAX_POSTS_PER_RUN,
  NOTIFIED_LABEL,
  classifyAuthor,
  commitHistory,
  githubApi,
  planItem,
  readSettings,
  reconcile,
  slackMessage,
  slackNotifier,
} from './contributor_triage.mjs';

function item(number, login, extra = {}) {
  return {
    number,
    state: 'open',
    title: `item ${number}`,
    html_url: `https://github.com/o/r/issues/${number}`,
    user: { login, type: 'User' },
    labels: [],
    ...extra,
  };
}

const labeled = { labels: [{ name: EXTERNAL_LABEL }] };
const notified = { labels: [{ name: EXTERNAL_LABEL }, { name: NOTIFIED_LABEL }] };

function history({ internal = ['y'], known = internal } = {}) {
  return {
    defaultBranchAuthors: async () => new Set(known),
    isInternal: async login => internal.includes(login.toLowerCase()),
  };
}

function plan(add, remove, post) {
  return { add, remove, post };
}

test('classification follows the commit history, not author_association', async () => {
  assert.equal(await classifyAuthor(item(1, 'Y'), history()), 'internal');
  for (const association of ['MEMBER', 'OWNER', 'COLLABORATOR', 'NONE']) {
    assert.equal(await classifyAuthor(item(2, 'someone', { author_association: association }), history()), 'external');
  }
});

test('a labeled item is settled without a lookup', async () => {
  const lookups = [];
  const counting = { isInternal: async login => { lookups.push(login); return true; } };
  assert.equal(await classifyAuthor(item(1, 'y', labeled), counting), 'external');
  assert.deepEqual(lookups, []);
});

test('the origin/internal label overrides the commit history', async () => {
  const marked = item(1, 'someone', { labels: [{ name: 'Origin/Internal' }, { name: EXTERNAL_LABEL }] });
  assert.equal(await classifyAuthor(marked, history()), 'internal');
  assert.deepEqual(planItem(marked, 'internal'), plan(false, true, false));
});

test('bots are recognized by type or by login suffix', async () => {
  const byType = item(1, 'renovate', { user: { login: 'renovate', type: 'Bot' } });
  assert.equal(await classifyAuthor(byType, history()), 'bot');
  assert.equal(await classifyAuthor(item(2, 'some-app[bot]'), history()), 'bot');
});

test('a deleted author is its own category', async () => {
  assert.equal(await classifyAuthor(item(1, 'x', { user: null }), history()), 'deleted');
  assert.equal(await classifyAuthor(item(2, 'ghost'), history()), 'deleted');
});

test('plan labels and posts an open external item until it is marked notified', () => {
  assert.deepEqual(planItem(item(1, 'x'), 'external'), plan(true, false, true));
  assert.deepEqual(planItem(item(1, 'x', notified), 'external'), plan(false, false, false));
});

test('an existing label without the notified marker does not suppress the post', () => {
  assert.deepEqual(planItem(item(1, 'x', labeled), 'external'), plan(false, false, true));
});

test('an item is posted however old it is', () => {
  const old = item(1, 'x', { created_at: '2020-01-01T00:00:00Z' });
  assert.equal(planItem(old, 'external').post, true);
});

test('plan matches labels case-insensitively', () => {
  const upper = item(1, 'x', { labels: [{ name: 'Origin/External' }, { name: 'Triage/Notified' }] });
  assert.deepEqual(planItem(upper, 'external'), plan(false, false, false));
});

test('plan labels a closed external item without posting', () => {
  assert.deepEqual(planItem(item(1, 'x', { state: 'closed' }), 'external'), plan(true, false, false));
});

test('plan removes a stale label from an internal or bot author only', () => {
  const stale = item(1, 'y', labeled);
  assert.deepEqual(planItem(stale, 'internal'), plan(false, true, false));
  assert.deepEqual(planItem(stale, 'bot'), plan(false, true, false));
  assert.deepEqual(planItem(stale, 'deleted'), plan(false, false, false));
});

function fakeApi(items) {
  const calls = [];
  return {
    calls,
    listAll: async () => items,
    ensured: false,
    async ensureLabels() { this.ensured = true; },
    addLabel: async (number, label) => { calls.push(`add ${number} ${label}`); },
    removeLabel: async (number, label) => { calls.push(`remove ${number} ${label}`); },
  };
}

function recorder({ fail = false, calls = [] } = {}) {
  return async entry => {
    calls.push(`post ${entry.number}`);
    if (fail) throw new Error('slack down');
  };
}

test('reconcile labels, posts, then marks the item notified, and counts every item', async () => {
  const bot = item(4, 'app[bot]');
  const gone = item(5, 'ghost');
  const api = fakeApi([item(1, 'x'), item(2, 'y'), bot, gone]);
  const counts = await reconcile({ api, historyOf: () => history(), notify: recorder({ calls: api.calls }) });
  assert.deepEqual(api.calls, [`add 1 ${EXTERNAL_LABEL}`, 'post 1', `add 1 ${NOTIFIED_LABEL}`]);
  assert.equal(api.ensured, true);
  assert.deepEqual(counts, { internal: 1, bot: 1, deleted: 1, external: 1, notified: 1, deferred: 0 });
});

test('a failed post leaves the item unmarked so the next run retries it', async () => {
  const api = fakeApi([item(1, 'x'), item(2, 'z', labeled)]);
  await assert.rejects(
    reconcile({ api, historyOf: () => history(), notify: recorder({ fail: true, calls: api.calls }) }),
    /failed on #1, #2/,
  );
  assert.deepEqual(api.calls, [`add 1 ${EXTERNAL_LABEL}`, 'post 1', 'post 2']);
});

test('a failed marker after a successful post counts as a failure, not a post', async () => {
  const api = fakeApi([item(1, 'x', labeled)]);
  api.addLabel = async () => { throw new Error('HTTP 502'); };
  const posted = [];
  await assert.rejects(reconcile({ api, historyOf: () => history(), notify: recorder({ calls: posted }) }), /failed on #1/);
  assert.deepEqual(posted, ['post 1']);
});

test('posts over the per-run cap are deferred but still labeled', async () => {
  const items = Array.from({ length: MAX_POSTS_PER_RUN + 2 }, (_, i) => item(i + 1, 'x'));
  const api = fakeApi(items);
  const counts = await reconcile({ api, historyOf: () => history(), notify: recorder({ calls: api.calls }) });
  assert.equal(api.calls.filter(call => call.startsWith('post')).length, MAX_POSTS_PER_RUN);
  assert.equal(api.calls.filter(call => call.endsWith(NOTIFIED_LABEL)).length, MAX_POSTS_PER_RUN);
  assert.equal(api.calls.filter(call => call.endsWith(EXTERNAL_LABEL)).length, MAX_POSTS_PER_RUN + 2);
  assert.equal(counts.notified, MAX_POSTS_PER_RUN);
  assert.equal(counts.deferred, 2);
});

test('failed posts use up the cap without being counted as posted', async () => {
  const items = Array.from({ length: MAX_POSTS_PER_RUN + 1 }, (_, i) => item(i + 1, 'x', labeled));
  const posted = [];
  await assert.rejects(reconcile({ api: fakeApi(items), historyOf: () => history(), notify: recorder({ fail: true, calls: posted }) }));
  assert.equal(posted.length, MAX_POSTS_PER_RUN);
});

test('reconcile clears the external label from an item marked origin/internal', async () => {
  const api = fakeApi([item(1, 'x', { labels: [{ name: 'origin/internal' }, { name: EXTERNAL_LABEL }] })]);
  await reconcile({ api, historyOf: () => history(), notify: recorder() });
  assert.deepEqual(api.calls, [`remove 1 ${EXTERNAL_LABEL}`]);
});

test('an empty listing fails before creating labels', async () => {
  const api = fakeApi([]);
  await assert.rejects(reconcile({ api, historyOf: () => history(), notify: recorder() }), /listed no issues/);
  assert.equal(api.ensured, false);
});

test('a failed commit-history lookup fails the run before any write', async () => {
  const api = fakeApi([item(1, 'x')]);
  const failing = { defaultBranchAuthors: async () => { throw new Error('GET /commits: HTTP 403'); } };
  await assert.rejects(reconcile({ api, historyOf: () => failing, notify: recorder() }), /HTTP 403/);
  assert.deepEqual(api.calls, []);
  assert.equal(api.ensured, false);
});

test('finding no nvidia.com authors on the default branch fails the run before any write', async () => {
  const api = fakeApi([item(1, 'x')]);
  await assert.rejects(reconcile({ api, historyOf: () => history({ known: [] }), notify: recorder() }), /no nvidia.com authors/);
  assert.deepEqual(api.calls, []);
  assert.equal(api.ensured, false);
});

test('a dry run writes nothing and posts nothing', async () => {
  const api = fakeApi([item(1, 'x'), item(2, 'y')]);
  const counts = await reconcile({ api, historyOf: () => history(), notify: recorder({ calls: api.calls }), dryRun: true });
  assert.deepEqual(api.calls, []);
  assert.equal(api.ensured, false);
  assert.equal(counts.notified, 1);
});

test('settings require a webhook except for a dry run', () => {
  const base = { GITHUB_TOKEN: 't', GITHUB_REPOSITORY: 'o/r' };
  assert.throws(() => readSettings(base), /SLACK_WEBHOOK_URL/);
  assert.throws(() => readSettings({ ...base, DRY_RUN: 'false' }), /SLACK_WEBHOOK_URL/);
  assert.equal(readSettings({ ...base, DRY_RUN: 'true' }).dryRun, true);
  assert.equal(readSettings({ ...base, SLACK_WEBHOOK_URL: 'w', DRY_RUN: 'TRUE' }).dryRun, false);
  assert.throws(() => readSettings({ SLACK_WEBHOOK_URL: 'w' }), /GITHUB_TOKEN/);
});

test('slack message names the kind and escapes control characters in the title', () => {
  const pr = item(1, 'x', { title: 'fix <!channel> & <http://a|b>', pull_request: {} });
  const message = slackMessage(pr);
  assert.match(message.text, /^External PR #1 by x: fix &lt;!channel&gt; &amp; &lt;http:\/\/a\|b&gt;/);
  assert.match(message.text, /https:\/\/github\.com\/o\/r\/issues\/1$/);
  assert.equal(message.unfurl_links, false);
  assert.match(slackMessage(item(2, 'y')).text, /^External issue #2/);
});

test('slack notifier throws on an HTTP error and never reveals the webhook URL', async () => {
  const secret = 'https://hooks.example/SECRET';
  await assert.rejects(slackNotifier(secret, async () => ({ ok: false, status: 500 }))(item(1, 'x')), /HTTP 500/);
  const failing = slackNotifier(secret, async () => { throw new Error(`bad URL ${secret}`); });
  await assert.rejects(failing(item(1, 'x')), error => !error.message.includes('SECRET'));
});

function fakeFetch(routes) {
  const requests = [];
  const fetcher = async (url, init = {}) => {
    const method = init.method ?? 'GET';
    const path = url.replace('https://api.github.com/repos/o/r', '');
    requests.push(`${method} ${path}`);
    const [status, body] = routes(method, path) ?? [200, {}];
    return { ok: status < 300, status, json: async () => body };
  };
  return { fetcher, requests };
}

const pageNumber = path => new URLSearchParams(path.split('?')[1]).get('page');
const numbered = (from, count) => Array.from({ length: count }, (_, i) => ({ number: from + i }));

test('api lists oldest first, pages until a short page, and labels through the issues endpoint', async () => {
  const { fetcher, requests } = fakeFetch((method, path) => {
    if (pageNumber(path) === '1') return [200, numbered(1, 100)];
    if (pageNumber(path) === '2') return [200, numbered(101, 3)];
  });
  const api = githubApi('t', 'o/r', fetcher);
  assert.equal((await api.listAll()).length, 103);
  const query = new URLSearchParams(requests[0].split('?')[1]);
  assert.equal(query.get('sort'), 'created');
  assert.equal(query.get('direction'), 'asc');
  await api.addLabel(1, EXTERNAL_LABEL);
  await api.removeLabel(2, EXTERNAL_LABEL);
  assert.deepEqual(requests.slice(2), ['POST /issues/1/labels', 'DELETE /issues/2/labels/origin%2Fexternal']);
});

test('api reads default-branch commits and PR commits', async () => {
  const { fetcher, requests } = fakeFetch(() => [200, []]);
  const api = githubApi('t', 'o/r', fetcher);
  await api.listCommits();
  await api.listPullCommits(7);
  assert.deepEqual(requests, ['GET /commits?per_page=100&page=1', 'GET /pulls/7/commits?per_page=100&page=1']);
});

test('api stops after a page shorter than 100', async () => {
  const { fetcher, requests } = fakeFetch(() => [200, numbered(1, 99)]);
  assert.equal((await githubApi('t', 'o/r', fetcher).listAll()).length, 99);
  assert.equal(requests.length, 1);
});

test('api drops an item returned on two pages', async () => {
  const { fetcher } = fakeFetch((method, path) => (pageNumber(path) === '1' ? [200, numbered(1, 100)] : [200, numbered(100, 2)]));
  const numbers = (await githubApi('t', 'o/r', fetcher).listAll()).map(entry => entry.number);
  assert.equal(numbers.length, 101);
  assert.deepEqual(numbers.slice(-2), [100, 101]);
});

test('ensureLabels creates only missing labels and surfaces other errors', async () => {
  const missing = fakeFetch((method, path) => (method === 'GET' && path === '/labels/triage%2Fnotified' ? [404, {}] : [200, { name: 'x' }]));
  await githubApi('t', 'o/r', missing.fetcher).ensureLabels();
  assert.deepEqual(missing.requests, ['GET /labels/origin%2Fexternal', 'GET /labels/origin%2Finternal', 'GET /labels/triage%2Fnotified', 'POST /labels']);

  const none = fakeFetch(method => (method === 'GET' ? [404, {}] : [201, {}]));
  await githubApi('t', 'o/r', none.fetcher).ensureLabels();
  assert.equal(none.requests.filter(request => request === 'POST /labels').length, 3);

  const broken = fakeFetch(() => [500, {}]);
  await assert.rejects(githubApi('t', 'o/r', broken.fetcher).ensureLabels(), /HTTP 500/);
});

const commit = (login, email) => ({ author: login ? { login } : null, commit: { author: { email } } });

function historyApi(pullCommits = {}) {
  const calls = [];
  return {
    calls,
    listCommits: async () => {
      calls.push('commits');
      return [commit('Main-Staff', 'a@nvidia.com'), commit('partner', 'p@partner.example'), commit(null, 'b@nvidia.com')];
    },
    listPullCommits: async number => { calls.push(number); return pullCommits[number] ?? []; },
  };
}

test('default-branch authors are logins linked to nvidia.com commits', async () => {
  const api = historyApi();
  const repo = commitHistory(api, []);
  assert.deepEqual([...await repo.defaultBranchAuthors()], ['main-staff']);
  await repo.defaultBranchAuthors();
  assert.deepEqual(api.calls, ['commits']);
});

test('a PR author is internal through their own or unlinked nvidia.com commits only', async () => {
  const api = historyApi({
    2: [commit(null, 'dev@gmail.com'), commit(null, 'dev@NVIDIA.com')],
    3: [commit('PR-Owner', 'dev@nvidia.com')],
    4: [commit('maintainer', 'm@nvidia.com'), commit('partner', 'p@partner.example')],
    5: [commit(null, 'dev@nvidia.com.example')],
  });
  const items = [
    item(1, 'main-staff', { pull_request: {} }),
    item(2, 'Unlinked-Staff', { pull_request: {} }),
    item(3, 'PR-Owner', { pull_request: {} }),
    item(4, 'partner', { pull_request: {} }),
    item(5, 'lookalike', { pull_request: {} }),
    item(6, 'issue-only'),
  ];
  const repo = commitHistory(api, items);
  const results = [];
  for (const login of ['Main-Staff', 'unlinked-staff', 'pr-owner', 'partner', 'lookalike', 'issue-only', 'partner']) {
    results.push(await repo.isInternal(login));
  }
  assert.deepEqual(results, [true, true, true, false, false, false, false]);
  assert.deepEqual(api.calls, ['commits', 2, 3, 4, 5]);
});

test('a failed PR lookup fails the items of that author and the run, not other items', async () => {
  const api = fakeApi([item(1, 'x', { pull_request: {} }), item(2, 'x'), item(3, 'z')]);
  api.listCommits = async () => [commit('y', 'y@nvidia.com')];
  api.listPullCommits = async () => { throw new Error('GET /pulls/1/commits: HTTP 502'); };
  await assert.rejects(reconcile({ api, notify: recorder({ calls: api.calls }) }), /failed on #1, #2/);
  assert.deepEqual(api.calls, [`add 3 ${EXTERNAL_LABEL}`, 'post 3', `add 3 ${NOTIFIED_LABEL}`]);
});

test('a PR author is settled by the first internal PR', async () => {
  const api = historyApi({ 1: [commit(null, 'dev@nvidia.com')] });
  const repo = commitHistory(api, [item(1, 'staff', { pull_request: {} }), item(2, 'staff', { pull_request: {} })]);
  assert.equal(await repo.isInternal('staff'), true);
  assert.deepEqual(api.calls, ['commits', 1]);
});
