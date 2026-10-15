export const EXTERNAL_LABEL = 'origin/external';
// Set by a maintainer for staff the commit history cannot place.
export const INTERNAL_LABEL = 'origin/internal';
// Posting is tracked apart from origin/external, so adding that label by hand
// does not suppress a post and removing it does not repeat one.
export const NOTIFIED_LABEL = 'triage/notified';

// Excess items stay unposted for the next run, which bounds a burst such as a
// change in how authors are classified.
export const MAX_POSTS_PER_RUN = 10;

const isInternalEmail = email => Boolean(email?.toLowerCase().endsWith('@nvidia.com'));

function labelNames(item) {
  return new Set((item.labels ?? []).map(label => label.name.toLowerCase()));
}

// A labeled item is settled and isn't looked up again.
export async function classifyAuthor(item, history) {
  const login = item.user?.login;
  // GitHub reassigns a deleted account's items to "ghost".
  if (!login || login === 'ghost') return 'deleted';
  if (item.user.type === 'Bot' || login.endsWith('[bot]')) return 'bot';
  const labels = labelNames(item);
  if (labels.has(INTERNAL_LABEL)) return 'internal';
  if (labels.has(EXTERNAL_LABEL)) return 'external';
  return (await history.isInternal(login)) ? 'internal' : 'external';
}

export function planItem(item, origin) {
  const labels = labelNames(item);
  const external = origin === 'external';
  return {
    add: external && !labels.has(EXTERNAL_LABEL),
    remove: (origin === 'internal' || origin === 'bot') && labels.has(EXTERNAL_LABEL),
    post: external && !labels.has(NOTIFIED_LABEL) && item.state === 'open',
  };
}

export async function reconcile({ api, notify, dryRun = false, historyOf = commitHistory }) {
  const items = await api.listAll();
  // An empty list means the scan saw nothing, not that there is nothing to do.
  if (!items.length) throw new Error('listed no issues or PRs');
  const history = historyOf(api, items);
  const known = (await history.defaultBranchAuthors()).size;
  // With nobody known, every author would be posted as external.
  if (!known) throw new Error('found no nvidia.com authors on the default branch');
  console.log(`nvidia.com authors on the default branch: ${known}`);
  if (!dryRun) await api.ensureLabels();
  const failed = [];
  let attempts = 0;
  const counts = { internal: 0, bot: 0, deleted: 0, external: 0, notified: 0, deferred: 0 };
  for (const item of items) {
    try {
      const origin = await classifyAuthor(item, history);
      counts[origin]++;
      const plan = planItem(item, origin);
      const post = plan.post && attempts < MAX_POSTS_PER_RUN;
      if (post) attempts++;
      else if (plan.post) counts.deferred++;
      const actions = [plan.add && 'label', plan.remove && 'unlabel', post && 'post'].filter(Boolean);
      if (actions.length) console.log(`#${item.number}: ${actions.join(', ')}`);
      if (dryRun) {
        if (post) counts.notified++;
        continue;
      }
      if (plan.add) await api.addLabel(item.number, EXTERNAL_LABEL);
      if (plan.remove) await api.removeLabel(item.number, EXTERNAL_LABEL);
      if (post) {
        await notify(item);
        // If this fails after a successful post, the next run posts again.
        await api.addLabel(item.number, NOTIFIED_LABEL);
        counts.notified++;
      }
    } catch (error) {
      failed.push(`#${item.number}`);
      console.error(`#${item.number}: ${error.message}`);
    }
  }
  console.log(`${dryRun ? 'dry run, ' : ''}all ${items.length} issues and PRs: ${counts.internal} internal, `
    + `${counts.bot} bot, ${counts.deleted} deleted, ${counts.external} external, ${failed.length} failed; `
    + `${counts.notified} ${dryRun ? 'would be ' : ''}posted, ${counts.deferred} deferred`);
  if (failed.length) throw new Error(`failed on ${failed.join(', ')}`);
  return counts;
}

function escapeSlack(text) {
  return text.replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('>', '&gt;');
}

export function slackMessage(item) {
  const kind = item.pull_request ? 'PR' : 'issue';
  return {
    text: `External ${kind} #${item.number} by ${item.user.login}: ${escapeSlack(item.title)}\n${item.html_url}`,
    // Titles can carry bare URLs; don't unfurl them.
    unfurl_links: false,
    unfurl_media: false,
  };
}

export function slackNotifier(webhook, fetcher = fetch) {
  return async item => {
    let response;
    try {
      response = await fetcher(webhook, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(slackMessage(item)),
      });
    } catch {
      // The error text can include the webhook URL, which is a secret.
      throw new Error('Slack webhook request failed');
    }
    if (!response.ok) throw new Error(`Slack webhook: HTTP ${response.status}`);
  };
}

export function githubApi(token, repository, fetcher = fetch) {
  const base = `https://api.github.com/repos/${repository}`;
  const headers = {
    Accept: 'application/vnd.github+json',
    Authorization: `Bearer ${token}`,
    'X-GitHub-Api-Version': '2022-11-28',
  };
  async function request(path, { method = 'GET', body, allow404 = false } = {}) {
    const response = await fetcher(`${base}${path}`, {
      method,
      headers: body ? { ...headers, 'Content-Type': 'application/json' } : headers,
      body: body && JSON.stringify(body),
    });
    if (allow404 && response.status === 404) return null;
    if (!response.ok) throw new Error(`${method} ${path}: HTTP ${response.status}`);
    return response.status === 204 ? null : response.json();
  }
  async function paginate(path) {
    const all = [];
    for (let page = 1; ; page++) {
      const batch = await request(`${path}${path.includes('?') ? '&' : '?'}per_page=100&page=${page}`);
      all.push(...batch);
      if (batch.length < 100) return all;
    }
  }
  return {
    async ensureLabels() {
      for (const label of [
        { name: EXTERNAL_LABEL, color: 'b60205', description: 'Opened by someone outside NVIDIA' },
        { name: INTERNAL_LABEL, color: '0e8a16', description: 'NVIDIA author; skips contributor triage' },
        { name: NOTIFIED_LABEL, color: 'c5def5', description: 'Contributor triage posted this to Slack' },
      ]) {
        if (!(await request(`/labels/${encodeURIComponent(label.name)}`, { allow404: true }))) {
          await request('/labels', { method: 'POST', body: label });
        }
      }
    },
    // An item opened mid-scan shifts the pages, so one can be returned twice.
    // Oldest first, so a burst of new items cannot keep older ones deferred.
    listAll: async () => [...new Map((await paginate('/issues?state=all&sort=created&direction=asc'))
      .map(item => [item.number, item])).values()],
    listCommits: () => paginate('/commits'),
    listPullCommits: number => paginate(`/pulls/${number}/commits`),
    addLabel: (number, label) => request(`/issues/${number}/labels`, { method: 'POST', body: { labels: [label] } }),
    removeLabel: (number, label) => request(`/issues/${number}/labels/${encodeURIComponent(label)}`, { method: 'DELETE' }),
  };
}

// The workflow token cannot see private org membership, so an author counts as
// internal when one of their commits, on the default branch or in one of their
// PRs, uses an nvidia.com address.
export function commitHistory(api, items) {
  const pullsByAuthor = new Map();
  for (const item of items) {
    const login = item.user?.login?.toLowerCase();
    if (!login || !item.pull_request) continue;
    pullsByAuthor.set(login, [...(pullsByAuthor.get(login) ?? []), item.number]);
  }
  // Many staff addresses are not on their GitHub account, so an unlinked commit
  // counts. One linked to someone else, such as a maintainer's fixup, does not.
  const ownInternal = (entry, login) => (!entry.author || entry.author.login?.toLowerCase() === login)
    && isInternalEmail(entry.commit.author?.email);
  let defaultBranch;
  const decided = new Map();
  function defaultBranchAuthors() {
    defaultBranch ??= api.listCommits().then(commits => new Set(commits
      .filter(entry => entry.author?.login && isInternalEmail(entry.commit.author?.email))
      .map(entry => entry.author.login.toLowerCase())));
    return defaultBranch;
  }
  async function decide(login) {
    if ((await defaultBranchAuthors()).has(login)) return true;
    for (const number of pullsByAuthor.get(login) ?? []) {
      if ((await api.listPullCommits(number)).some(entry => ownInternal(entry, login))) return true;
    }
    return false;
  }
  return {
    defaultBranchAuthors,
    isInternal(login) {
      const key = login.toLowerCase();
      if (!decided.has(key)) decided.set(key, decide(key));
      return decided.get(key);
    },
  };
}

export function readSettings(env) {
  if (!env.GITHUB_TOKEN || !env.GITHUB_REPOSITORY) throw new Error('GITHUB_TOKEN and GITHUB_REPOSITORY are required');
  const dryRun = env.DRY_RUN === 'true';
  // Without it every open external item would fail at the post.
  if (!env.SLACK_WEBHOOK_URL && !dryRun) throw new Error('SLACK_WEBHOOK_URL is not set');
  return { token: env.GITHUB_TOKEN, repository: env.GITHUB_REPOSITORY, webhook: env.SLACK_WEBHOOK_URL, dryRun };
}

async function main() {
  const { token, repository, webhook, dryRun } = readSettings(process.env);
  const api = githubApi(token, repository);
  await reconcile({ api, notify: webhook ? slackNotifier(webhook) : null, dryRun });
}

if (process.argv[1] === import.meta.filename) {
  main().catch(error => { console.error(error.message); process.exitCode = 1; });
}
