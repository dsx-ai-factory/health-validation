# SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
# SPDX-License-Identifier: Apache-2.0

"""Post a daily Slack digest of pull requests and issues measured against SLAs.

SLA targets come from a TOML file (see .github/sla.toml). The repository is read
through the GitHub REST API and the digest is posted to a Slack incoming
webhook. Only the standard library is used, so it runs on a stock runner.
"""

from __future__ import annotations

import argparse
import calendar
import json
import os
import re
import sys
import tomllib
import urllib.parse
import urllib.request
from collections.abc import Iterable, Iterator
from dataclasses import dataclass
from datetime import date, datetime, time, timedelta, timezone
from zoneinfo import ZoneInfo

UNITS = ("business_days", "calendar_days", "months")
WEEKDAYS = ("Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun")

BREACHED = "SLA breached"
DUE_SOON = "Due soon"
WAITING = "Waiting on maintainer"
SECTIONS = (BREACHED, DUE_SOON, WAITING)

# Slack rejects a section block whose text exceeds 3000 characters.
SLACK_SECTION_LIMIT = 2900


class ConfigError(ValueError):
    pass


@dataclass(frozen=True)
class Target:
    duration: int
    unit: str
    due_soon: int = 0
    due_soon_unit: str = ""

    def describe(self) -> str:
        return f"{self.duration} {unit_name(self.unit, self.duration)}"


@dataclass(frozen=True)
class BusinessCalendar:
    tz: ZoneInfo
    days: frozenset[int]

    def is_business_day(self, d: date) -> bool:
        return d.weekday() in self.days

    def shift(self, at: datetime, n: int, unit: str) -> datetime:
        """Move `at` by n units, in local wall-clock time."""
        local = at.astimezone(self.tz)
        if unit == "calendar_days":
            return local + timedelta(days=n)
        if unit == "months":
            return add_months(local, n)
        # A business-day clock that starts on a non-business day starts at the
        # beginning of the next business day.
        if n >= 0 and not self.is_business_day(local.date()):
            d = local.date()
            while not self.is_business_day(d):
                d += timedelta(days=1)
            local = datetime.combine(d, time(), self.tz)
        step = timedelta(days=1 if n >= 0 else -1)
        remaining = abs(n)
        while remaining:
            local += step
            if self.is_business_day(local.date()):
                remaining -= 1
        return local

    def business_days_between(self, earlier: datetime, later: datetime) -> int:
        """Count business days after `earlier`'s date, up to `later`'s date."""
        d = earlier.astimezone(self.tz).date()
        end = later.astimezone(self.tz).date()
        count = 0
        while d < end:
            d += timedelta(days=1)
            if self.is_business_day(d):
                count += 1
        return count


@dataclass(frozen=True)
class Config:
    calendar: BusinessCalendar
    associations: frozenset[str]
    users: frozenset[str]
    ignore_users: frozenset[str]
    ignore_comment_patterns: tuple[re.Pattern[str], ...]
    pr_first_response: Target
    priority_labels: tuple[str, ...]
    issue_priority: Target
    fix_clock_starts: str
    fix_targets: dict[str, Target]
    title: str
    post_when_empty: bool
    max_items: int


@dataclass(frozen=True)
class Activity:
    kind: str  # comment, review, push, ready, review_request, label
    at: datetime
    login: str = ""
    association: str = ""
    bot: bool = False
    state: str = ""
    body: str = ""
    label: str = ""


@dataclass(frozen=True)
class Finding:
    section: str
    url: str
    ref: str
    title: str
    detail: str
    age: str
    people: str
    sort_key: datetime


@dataclass(frozen=True)
class Section:
    name: str
    shown: tuple[Finding, ...]
    total: int


@dataclass(frozen=True)
class Digest:
    heading: str
    sections: tuple[Section, ...]
    activity: str
    opened: Section | None = None

    @property
    def empty(self) -> bool:
        """True when nothing is past or close to its SLA."""
        return not self.sections

    @property
    def shown_sections(self) -> tuple[Section, ...]:
        return self.sections + ((self.opened,) if self.opened else ())


def unit_name(unit: str, n: int) -> str:
    base = {"business_days": "business day", "calendar_days": "day", "months": "month"}[
        unit
    ]
    return base if n == 1 else base + "s"


def add_months(at: datetime, n: int) -> datetime:
    year, month = divmod(at.month - 1 + n, 12)
    year += at.year
    month += 1
    day = min(at.day, calendar.monthrange(year, month)[1])
    return at.replace(year=year, month=month, day=day)


def parse_time(value: str) -> datetime:
    return datetime.fromisoformat(value.replace("Z", "+00:00"))


def _table(raw: dict, key: str, allowed: set[str]) -> dict:
    value = raw.get(key, {})
    if not isinstance(value, dict):
        raise ConfigError(f"{key}: expected a table")
    unknown = set(value) - allowed
    if unknown:
        raise ConfigError(f"{key}: unknown keys {sorted(unknown)}")
    return value


def _int(value: object, name: str, minimum: int) -> int:
    if not isinstance(value, int) or isinstance(value, bool) or value < minimum:
        raise ConfigError(f"{name}: expected an integer >= {minimum}")
    return value


def _strings(value: object, name: str) -> tuple[str, ...]:
    if not isinstance(value, list) or not all(isinstance(v, str) for v in value):
        raise ConfigError(f"{name}: expected a list of strings")
    return tuple(value)


def _target(raw: object, name: str, extra: frozenset[str] = frozenset()) -> Target:
    if not isinstance(raw, dict):
        raise ConfigError(f"{name}: expected a table")
    unknown = set(raw) - {"duration", "unit", "due_soon", "due_soon_unit"} - extra
    if unknown:
        raise ConfigError(f"{name}: unknown keys {sorted(unknown)}")
    duration = _int(raw.get("duration"), f"{name}.duration", 1)
    unit = raw.get("unit")
    if unit not in UNITS:
        raise ConfigError(f"{name}.unit: expected one of {', '.join(UNITS)}")
    due_soon = _int(raw.get("due_soon", 0), f"{name}.due_soon", 0)
    due_soon_unit = raw.get("due_soon_unit", unit)
    if due_soon_unit not in UNITS:
        raise ConfigError(f"{name}.due_soon_unit: expected one of {', '.join(UNITS)}")
    if due_soon_unit == unit and due_soon >= duration:
        raise ConfigError(f"{name}.due_soon: must be less than the duration")
    return Target(duration, unit, due_soon, due_soon_unit)


def parse_config(raw: dict) -> Config:
    top = {
        "timezone",
        "business_days",
        "maintainers",
        "pr_first_response",
        "issue_priority",
        "issue_fix",
        "digest",
    }
    unknown = set(raw) - top
    if unknown:
        raise ConfigError(f"unknown keys {sorted(unknown)}")

    try:
        tz = ZoneInfo(raw.get("timezone", "UTC"))
    except (KeyError, ValueError) as e:
        raise ConfigError(f"timezone: {e}") from e

    day_names = _strings(raw.get("business_days", list(WEEKDAYS[:5])), "business_days")
    if not day_names or any(d not in WEEKDAYS for d in day_names):
        raise ConfigError(f"business_days: expected a non-empty subset of {WEEKDAYS}")
    cal = BusinessCalendar(tz, frozenset(WEEKDAYS.index(d) for d in day_names))

    m = _table(
        raw,
        "maintainers",
        {"associations", "users", "ignore_users", "ignore_comment_patterns"},
    )
    try:
        patterns = tuple(
            re.compile(p, re.IGNORECASE)
            for p in _strings(
                m.get("ignore_comment_patterns", []),
                "maintainers.ignore_comment_patterns",
            )
        )
    except re.error as e:
        raise ConfigError(f"maintainers.ignore_comment_patterns: {e}") from e

    if "pr_first_response" not in raw:
        raise ConfigError("pr_first_response: required")
    if "issue_priority" not in raw:
        raise ConfigError("issue_priority: required")
    pr_target = _target(raw["pr_first_response"], "pr_first_response")
    priority_target = _target(
        raw["issue_priority"], "issue_priority", frozenset({"labels"})
    )
    labels = _strings(raw["issue_priority"].get("labels", []), "issue_priority.labels")
    if not labels:
        raise ConfigError("issue_priority.labels: expected at least one label")

    fix = _table(raw, "issue_fix", {"clock_starts", "targets"})
    clock_starts = fix.get("clock_starts", "label_applied")
    if clock_starts not in ("label_applied", "opened"):
        raise ConfigError("issue_fix.clock_starts: expected label_applied or opened")
    targets_raw = fix.get("targets", {})
    if not isinstance(targets_raw, dict):
        raise ConfigError("issue_fix.targets: expected a table")
    targets = {}
    for label, value in targets_raw.items():
        if label not in labels:
            raise ConfigError(
                f"issue_fix.targets.{label}: not one of issue_priority.labels"
            )
        targets[label] = _target(value, f"issue_fix.targets.{label}")

    d = _table(
        raw,
        "digest",
        {"title", "post_when_empty", "max_items_per_section"},
    )
    post_when_empty = d.get("post_when_empty", True)
    if not isinstance(post_when_empty, bool):
        raise ConfigError("digest.post_when_empty: expected true or false")

    return Config(
        calendar=cal,
        associations=frozenset(
            _strings(
                m.get("associations", []),
                "maintainers.associations",
            )
        ),
        users=frozenset(
            u.lower() for u in _strings(m.get("users", []), "maintainers.users")
        ),
        ignore_users=frozenset(
            u.lower()
            for u in _strings(m.get("ignore_users", []), "maintainers.ignore_users")
        ),
        ignore_comment_patterns=patterns,
        pr_first_response=pr_target,
        priority_labels=labels,
        issue_priority=priority_target,
        fix_clock_starts=clock_starts,
        fix_targets=targets,
        title=str(d.get("title", "")),
        post_when_empty=post_when_empty,
        max_items=_int(
            d.get("max_items_per_section", 25), "digest.max_items_per_section", 1
        ),
    )


def is_bot(user: dict | None) -> bool:
    if not user:
        return False
    return user.get("type") == "Bot" or user.get("login", "").endswith("[bot]")


def parse_timeline(events: Iterable[dict]) -> list[Activity]:
    kinds = {
        "head_ref_force_pushed": "push",
        "ready_for_review": "ready",
        "review_requested": "review_request",
        "labeled": "label",
    }
    out = []
    for e in events:
        event = e.get("event")
        if event in ("commented", "reviewed"):
            when = (
                e.get("created_at") if event == "commented" else e.get("submitted_at")
            )
            if not when:
                continue  # A pending review has no submission time.
            user = e.get("user") or {}
            out.append(
                Activity(
                    kind="comment" if event == "commented" else "review",
                    at=parse_time(when),
                    login=user.get("login", ""),
                    association=e.get("author_association") or "",
                    bot=is_bot(user),
                    state=(e.get("state") or "").lower(),
                    body=e.get("body") or "",
                )
            )
        elif event == "committed":
            when = (e.get("committer") or {}).get("date")
            if when:
                out.append(Activity(kind="push", at=parse_time(when)))
        elif event in kinds and e.get("created_at"):
            actor = e.get("actor") or {}
            out.append(
                Activity(
                    kind=kinds[event],
                    at=parse_time(e["created_at"]),
                    login=actor.get("login", ""),
                    bot=is_bot(actor),
                    label=(e.get("label") or {}).get("name", ""),
                )
            )
    out.sort(key=lambda a: a.at)
    return out


def is_response(a: Activity, author: str, cfg: Config) -> bool:
    if a.kind not in ("comment", "review") or a.bot or not a.login:
        return False
    login = a.login.lower()
    if login == author.lower() or login in cfg.ignore_users:
        return False
    if login not in cfg.users and a.association not in cfg.associations:
        return False
    if a.kind == "comment" and any(
        p.search(a.body) for p in cfg.ignore_comment_patterns
    ):
        return False
    return True


def is_author_update(a: Activity, author: str) -> bool:
    login = a.login.lower()
    if a.kind == "push":
        # Commit events carry no GitHub login; treat them as the author's.
        return not login or login == author.lower()
    return a.kind in ("comment", "review", "review_request") and login == author.lower()


def describe_gap(
    cal: BusinessCalendar, earlier: datetime, later: datetime, unit: str
) -> str:
    if unit == "business_days":
        n = cal.business_days_between(earlier, later)
        if n:
            return f"{n} {unit_name(unit, n)}"
        return "less than 1 business day"
    seconds = (later - earlier).total_seconds()
    days = int(seconds // 86400)
    if days:
        return f"{days} {unit_name('calendar_days', days)}"
    return f"{max(1, int(seconds // 3600))}h"


def describe_age(opened: datetime, now: datetime) -> str:
    days = (now - opened).days
    if days < 1:
        return "opened today"
    return f"opened {days} {unit_name('calendar_days', days)} ago"


def check_sla(
    cal: BusinessCalendar, target: Target, start: datetime, now: datetime, what: str
) -> tuple[str, str, datetime] | None:
    deadline = cal.shift(start, target.duration, target.unit)
    sla = f"SLA {target.describe()}"
    if now >= deadline:
        gap = describe_gap(cal, deadline, now, target.unit)
        return BREACHED, f"{what}, overdue by {gap} ({sla})", deadline
    if target.due_soon:
        warn_at = cal.shift(deadline, -target.due_soon, target.due_soon_unit)
        if now >= warn_at:
            gap = describe_gap(cal, now, deadline, target.unit)
            return DUE_SOON, f"{what}, due in {gap} ({sla})", deadline
    return None


def _logins(users: Iterable[dict], key: str = "login") -> list[str]:
    return [u[key] for u in users if u.get(key)]


def pr_people(pr: dict) -> str:
    reviewers = _logins(pr.get("requested_reviewers") or []) + _logins(
        pr.get("requested_teams") or [], "slug"
    )
    if reviewers:
        return "reviewers: " + ", ".join(reviewers)
    assignees = _logins(pr.get("assignees") or [])
    if assignees:
        return "assignees: " + ", ".join(assignees)
    return "no reviewer requested"


def issue_people(issue: dict) -> str:
    assignees = _logins(issue.get("assignees") or [])
    return "assignees: " + ", ".join(assignees) if assignees else "unassigned"


def evaluate_pr(
    pr: dict, acts: list[Activity], cfg: Config, now: datetime
) -> list[Finding]:
    if pr.get("draft"):
        return []
    author = (pr.get("user") or {}).get("login", "")
    opened = parse_time(pr["created_at"])
    ready = [a.at for a in acts if a.kind == "ready"]
    start = ready[-1] if ready else opened

    def finding(section: str, detail: str, sort_key: datetime) -> Finding:
        return Finding(
            section=section,
            url=pr["html_url"],
            ref=f"PR #{pr['number']}",
            title=pr.get("title", ""),
            detail=detail,
            age=describe_age(opened, now),
            people=pr_people(pr),
            sort_key=sort_key,
        )

    responses = [a for a in acts if is_response(a, author, cfg)]
    if not responses:
        result = check_sla(
            cfg.calendar, cfg.pr_first_response, start, now, "no maintainer response"
        )
        return [finding(*result)] if result else []

    latest_review: dict[str, str] = {}
    for a in responses:
        if a.kind == "review" and a.state in (
            "approved",
            "changes_requested",
            "dismissed",
        ):
            latest_review[a.login.lower()] = a.state
    if "changes_requested" not in latest_review.values():
        return []
    last_response = responses[-1].at
    for a in acts:
        if a.at > last_response and is_author_update(a, author):
            gap = describe_gap(cfg.calendar, a.at, now, "business_days")
            detail = f"author updated after changes were requested, waiting {gap}"
            return [finding(WAITING, detail, a.at)]
    return []


def issue_priority(issue: dict, cfg: Config) -> str | None:
    labels = {label.get("name") for label in issue.get("labels") or []}
    return next((p for p in cfg.priority_labels if p in labels), None)


def needs_issue_timeline(issue: dict, cfg: Config) -> bool:
    return cfg.fix_clock_starts == "label_applied" and issue_priority(issue, cfg) in (
        cfg.fix_targets
    )


def evaluate_issue(
    issue: dict, acts: list[Activity], cfg: Config, now: datetime
) -> list[Finding]:
    opened = parse_time(issue["created_at"])
    priority = issue_priority(issue, cfg)
    if priority is None:
        result = check_sla(cfg.calendar, cfg.issue_priority, opened, now, "no priority")
    elif priority in cfg.fix_targets:
        start = opened
        if cfg.fix_clock_starts == "label_applied":
            applied = [a.at for a in acts if a.kind == "label" and a.label == priority]
            if applied:
                start = applied[-1]
        result = check_sla(
            cfg.calendar, cfg.fix_targets[priority], start, now, f"{priority} open"
        )
    else:
        return []
    if not result:
        return []
    section, detail, deadline = result
    return [
        Finding(
            section=section,
            url=issue["html_url"],
            ref=f"Issue #{issue['number']}",
            title=issue.get("title", ""),
            detail=detail,
            age=describe_age(opened, now),
            people=issue_people(issue),
            sort_key=deadline,
        )
    ]


def activity_window(
    cal: BusinessCalendar, now: datetime
) -> tuple[datetime, datetime, str]:
    """Return the span since this time on the previous business day.

    Consecutive weekday digests chain without gaps; Monday's covers the weekend.
    """
    end = now.astimezone(cal.tz).replace(microsecond=0)
    start = cal.shift(end, -1, "business_days")
    label = "Last 24 hours" if _one_day(start, end) else f"Since {start:%A}"
    return start, end, f"{label} ({describe_time(start)} to {describe_time(end)})"


def _one_day(start: datetime, end: datetime) -> bool:
    return start.date() == end.date() - timedelta(days=1)


def opened_heading(start: datetime, end: datetime) -> str:
    if _one_day(start, end):
        return "Opened in the last 24 hours"
    return f"Opened since {start:%A}"


def describe_time(at: datetime) -> str:
    return f"{at:%a %b} {at.day}, {at.hour}:{at:%M %Z}"


def opened_query(repo: str, start: datetime, end: datetime) -> str:
    return f"repo:{repo} created:{start.isoformat()}..{end.isoformat()}"


def opened_finding(item: dict) -> Finding:
    """Describe a pull request or issue returned by the search API."""
    if "pull_request" in item:
        ref = f"PR #{item['number']}"
        if (item["pull_request"] or {}).get("merged_at"):
            status = "merged"
        elif item.get("state") == "closed":
            status = "closed"
        else:
            status = "draft" if item.get("draft") else "open"
    else:
        ref = f"Issue #{item['number']}"
        status = item.get("state", "open")
    return Finding(
        section="",
        url=item["html_url"],
        ref=ref,
        title=item.get("title", ""),
        detail=f"by {(item.get('user') or {}).get('login', 'unknown')}",
        age="",
        people=status,
        sort_key=parse_time(item["created_at"]),
    )


def activity_queries(
    repo: str, start: datetime, end: datetime
) -> list[tuple[str, str]]:
    span = f"{start.isoformat()}..{end.isoformat()}"
    base = f"repo:{repo}"
    return [
        ("prs_opened", f"{base} is:pr created:{span}"),
        ("prs_merged", f"{base} is:pr is:merged merged:{span}"),
        ("prs_closed", f"{base} is:pr is:closed is:unmerged closed:{span}"),
        ("issues_opened", f"{base} is:issue created:{span}"),
        ("issues_closed", f"{base} is:issue is:closed closed:{span}"),
    ]


def describe_activity(label: str, counts: dict[str, int]) -> str:
    def n(key: str, noun: str) -> str:
        value = counts.get(key, 0)
        return f"{value} {noun}"

    return (
        f"{label}: PRs {n('prs_opened', 'opened')}, {n('prs_merged', 'merged')}, "
        f"{n('prs_closed', 'closed without merging')}; issues "
        f"{n('issues_opened', 'opened')}, {n('issues_closed', 'closed')}"
    )


def build_digest(
    cfg: Config,
    repo: str,
    findings: list[Finding],
    activity: str,
    now: datetime,
    opened: Section | None = None,
) -> Digest:
    local = now.astimezone(cfg.calendar.tz)
    title = cfg.title or repo
    heading = f"{title} daily digest: {local:%a %b} {local.day}"
    sections = []
    for name in SECTIONS:
        items = sorted(
            (f for f in findings if f.section == name), key=lambda f: f.sort_key
        )
        if items:
            sections.append(Section(name, tuple(items[: cfg.max_items]), len(items)))
    return Digest(heading, tuple(sections), activity, opened)


def _details(f: Finding) -> list[str]:
    return [part for part in (f.detail, f.age, f.people) if part]


def slack_escape(text: str) -> str:
    return text.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")


def shorten(text: str, limit: int = 90) -> str:
    return text if len(text) <= limit else text[: limit - 1].rstrip() + "…"


def to_slack(digest: Digest) -> dict:
    blocks: list[dict] = [
        {"type": "header", "text": {"type": "plain_text", "text": digest.heading}}
    ]
    if digest.empty:
        blocks.append(
            {
                "type": "section",
                "text": {
                    "type": "mrkdwn",
                    "text": "All clear: nothing is past or close to its SLA.",
                },
            }
        )
    for section in digest.shown_sections:
        lines = [
            " · ".join(
                [f"• <{f.url}|{f.ref}> {slack_escape(shorten(f.title))}"]
                + [slack_escape(part) for part in _details(f)]
            )
            for f in section.shown
        ]
        hidden = section.total - len(section.shown)
        if hidden:
            lines.append(f"…and {hidden} more")
        chunk = f"*{section.name} ({section.total})*"
        for line in lines:
            if len(chunk) + len(line) + 1 > SLACK_SECTION_LIMIT:
                blocks.append(
                    {"type": "section", "text": {"type": "mrkdwn", "text": chunk}}
                )
                chunk = line
            else:
                chunk += "\n" + line
        blocks.append({"type": "section", "text": {"type": "mrkdwn", "text": chunk}})
    blocks.append(
        {"type": "context", "elements": [{"type": "mrkdwn", "text": digest.activity}]}
    )
    return {"text": digest.heading, "blocks": blocks}


def to_markdown(digest: Digest) -> str:
    out = [f"## {digest.heading}", ""]
    if digest.empty:
        out += ["All clear: nothing is past or close to its SLA.", ""]
    for section in digest.shown_sections:
        out.append(f"### {section.name} ({section.total})")
        for f in section.shown:
            title = shorten(f.title).replace("[", "\\[").replace("]", "\\]")
            out.append(" · ".join([f"- [{f.ref}]({f.url}) {title}"] + _details(f)))
        hidden = section.total - len(section.shown)
        if hidden:
            out.append(f"- …and {hidden} more")
        out.append("")
    out.append(digest.activity)
    return "\n".join(out) + "\n"


class GitHubClient:
    def __init__(self, api_url: str, token: str) -> None:
        self.api_url = api_url.rstrip("/")
        self.headers = {
            "Accept": "application/vnd.github+json",
            "Authorization": f"Bearer {token}",
            "User-Agent": "sla-digest",
            "X-GitHub-Api-Version": "2022-11-28",
        }

    def _get(self, url: str) -> tuple[object, str]:
        request = urllib.request.Request(url, headers=self.headers)
        with urllib.request.urlopen(request, timeout=30) as response:
            return json.load(response), response.headers.get("Link", "")

    def paginate(self, path: str, params: dict[str, str]) -> Iterator[dict]:
        url: str | None = f"{self.api_url}{path}?{urllib.parse.urlencode(params)}"
        while url:
            data, link = self._get(url)
            yield from data  # type: ignore[misc]
            match = re.search(r'<([^>]+)>;\s*rel="next"', link)
            url = match.group(1) if match else None

    def search(self, query: str, limit: int) -> tuple[list[dict], int]:
        """Return the oldest `limit` matches (at most 100) and the total count."""
        params = urllib.parse.urlencode(
            {
                "q": query,
                "sort": "created",
                "order": "asc",
                "per_page": str(max(1, min(limit, 100))),
            }
        )
        data, _ = self._get(f"{self.api_url}/search/issues?{params}")
        return data["items"][:limit], int(data["total_count"])  # type: ignore[index]

    def search_count(self, query: str) -> int:
        return self.search(query, 1)[1]


def collect(
    client: GitHubClient, repo: str, cfg: Config, now: datetime
) -> tuple[list[Finding], Section | None, str]:
    findings: list[Finding] = []
    page = {"state": "open", "per_page": "100"}

    def timeline(number: int) -> list[Activity]:
        return parse_timeline(
            client.paginate(
                f"/repos/{repo}/issues/{number}/timeline", {"per_page": "100"}
            )
        )

    for pr in client.paginate(f"/repos/{repo}/pulls", page):
        if not pr.get("draft"):
            findings += evaluate_pr(pr, timeline(pr["number"]), cfg, now)
    for issue in client.paginate(f"/repos/{repo}/issues", page):
        if "pull_request" in issue:
            continue
        acts = timeline(issue["number"]) if needs_issue_timeline(issue, cfg) else []
        findings += evaluate_issue(issue, acts, cfg, now)

    start, end, label = activity_window(cfg.calendar, now)
    counts = {
        key: client.search_count(query)
        for key, query in activity_queries(repo, start, end)
    }
    items, total = client.search(opened_query(repo, start, end), cfg.max_items)
    opened = None
    if total:
        shown = tuple(opened_finding(item) for item in items)
        opened = Section(opened_heading(start, end), shown, total)
    return findings, opened, describe_activity(label, counts)


def post_slack(webhook_url: str, payload: dict) -> None:
    request = urllib.request.Request(
        webhook_url,
        data=json.dumps(payload).encode(),
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    with urllib.request.urlopen(request, timeout=30) as response:
        if response.status != 200:
            raise RuntimeError(f"Slack webhook returned HTTP {response.status}")


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--config", default=".github/sla.toml")
    parser.add_argument(
        "--dry-run",
        action="store_true",
        help="print the digest and write the job summary without posting to Slack",
    )
    args = parser.parse_args(argv)

    with open(args.config, "rb") as f:
        cfg = parse_config(tomllib.load(f))
    now = datetime.now(timezone.utc)

    repo = os.environ.get("GITHUB_REPOSITORY", "")
    token = os.environ.get("GITHUB_TOKEN", "")
    if not repo or not token:
        print("GITHUB_REPOSITORY and GITHUB_TOKEN must be set.", file=sys.stderr)
        return 2
    client = GitHubClient(
        os.environ.get("GITHUB_API_URL", "https://api.github.com"), token
    )
    findings, opened, activity = collect(client, repo, cfg, now)
    digest = build_digest(cfg, repo, findings, activity, now, opened)

    markdown = to_markdown(digest)
    print(markdown)
    summary = os.environ.get("GITHUB_STEP_SUMMARY")
    if summary:
        with open(summary, "a", encoding="utf-8") as f:
            f.write(markdown)

    if args.dry_run:
        return 0
    if digest.empty and not cfg.post_when_empty:
        print("Nothing needs attention; not posting.")
        return 0
    webhook = os.environ.get("SLACK_WEBHOOK_URL", "")
    if not webhook:
        print("::warning::SLACK_WEBHOOK_URL is not set; the digest was not posted.")
        return 0
    post_slack(webhook, to_slack(digest))
    print("Posted the digest to Slack.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
