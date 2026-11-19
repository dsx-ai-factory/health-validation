# SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
# SPDX-License-Identifier: Apache-2.0

import copy
import os
import sys
import tomllib
import unittest
from datetime import datetime, timedelta, timezone
from pathlib import Path
from zoneinfo import ZoneInfo

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import sla_digest as sd  # noqa: E402

PT = ZoneInfo("America/Los_Angeles")

RAW_CONFIG = {
    "timezone": "America/Los_Angeles",
    "business_days": ["Mon", "Tue", "Wed", "Thu", "Fri"],
    "maintainers": {
        "associations": ["OWNER", "MEMBER", "COLLABORATOR"],
        "users": ["Outside-Maintainer"],
        "ignore_users": [],
        "ignore_comment_patterns": [r"^\s*/ok to test\b"],
    },
    "pr_first_response": {"duration": 4, "unit": "business_days", "due_soon": 1},
    "issue_priority": {
        "labels": ["priority/P0", "priority/P1", "priority/P2"],
        "duration": 7,
        "unit": "calendar_days",
        "due_soon": 2,
    },
    "issue_fix": {
        "clock_starts": "label_applied",
        "targets": {
            "priority/P0": {"duration": 30, "unit": "calendar_days", "due_soon": 7},
            "priority/P1": {
                "duration": 6,
                "unit": "months",
                "due_soon": 30,
                "due_soon_unit": "calendar_days",
            },
        },
    },
    "digest": {"title": "Test Repo", "max_items_per_section": 25},
}


def config(**overrides) -> sd.Config:
    raw = copy.deepcopy(RAW_CONFIG)
    for key, value in overrides.items():
        raw[key] = value
    return sd.parse_config(raw)


def pt(*args: int) -> datetime:
    return datetime(*args, tzinfo=PT)


def iso(dt: datetime) -> str:
    return dt.astimezone(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def user(login: str, bot: bool = False) -> dict:
    return {"login": login, "type": "Bot" if bot else "User"}


def comment(
    login: str,
    at: datetime,
    association: str = "MEMBER",
    body: str = "lgtm",
    bot: bool = False,
) -> dict:
    return {
        "event": "commented",
        "created_at": iso(at),
        "user": user(login, bot),
        "author_association": association,
        "body": body,
    }


def review(login: str, at: datetime, state: str, association: str = "MEMBER") -> dict:
    return {
        "event": "reviewed",
        "submitted_at": iso(at),
        "user": user(login),
        "author_association": association,
        "state": state,
        "body": "",
    }


def actor_event(event: str, login: str, at: datetime, **extra) -> dict:
    return {"event": event, "created_at": iso(at), "actor": user(login), **extra}


def pr(number: int = 1, opened: datetime = pt(2026, 11, 16, 10), **extra) -> dict:
    return {
        "number": number,
        "html_url": f"https://github.com/o/r/pull/{number}",
        "title": f"feat: change {number}",
        "created_at": iso(opened),
        "user": user("author"),
        "draft": False,
        "requested_reviewers": [user("rev")],
        "requested_teams": [],
        "assignees": [],
        **extra,
    }


def issue(
    number: int = 10, opened: datetime = pt(2026, 11, 10, 10), labels=(), **extra
) -> dict:
    return {
        "number": number,
        "html_url": f"https://github.com/o/r/issues/{number}",
        "title": f"[Bug]: problem {number}",
        "created_at": iso(opened),
        "user": user("reporter"),
        "labels": [{"name": label} for label in labels],
        "assignees": [],
        **extra,
    }


def evaluate_pr(
    p: dict, events: list[dict], now: datetime, cfg: sd.Config | None = None
):
    return sd.evaluate_pr(p, sd.parse_timeline(events), cfg or config(), now)


def evaluate_issue(
    i: dict, events: list[dict], now: datetime, cfg: sd.Config | None = None
):
    return sd.evaluate_issue(i, sd.parse_timeline(events), cfg or config(), now)


class CalendarTest(unittest.TestCase):
    def setUp(self):
        self.cal = config().calendar

    def test_business_days_skip_weekends(self):
        self.assertEqual(
            self.cal.shift(pt(2026, 11, 16, 10), 4, "business_days"),
            pt(2026, 11, 20, 10),
        )
        self.assertEqual(
            self.cal.shift(pt(2026, 11, 19, 10), 4, "business_days"),
            pt(2026, 11, 25, 10),
        )

    def test_weekend_start_counts_from_next_business_day(self):
        # Opened Saturday: Monday through Thursday are the four business days.
        self.assertEqual(
            self.cal.shift(pt(2026, 11, 14, 10), 4, "business_days"),
            pt(2026, 11, 20, 0),
        )

    def test_negative_shift(self):
        self.assertEqual(
            self.cal.shift(pt(2026, 11, 20, 10), -1, "business_days"),
            pt(2026, 11, 19, 10),
        )
        self.assertEqual(
            self.cal.shift(pt(2026, 11, 16, 10), -1, "business_days"),
            pt(2026, 11, 13, 10),
        )

    def test_months_clamp_to_month_end(self):
        self.assertEqual(
            self.cal.shift(pt(2027, 1, 31, 10), 1, "months"), pt(2027, 2, 28, 10)
        )
        self.assertEqual(
            self.cal.shift(pt(2026, 6, 1, 10), 6, "months"), pt(2026, 12, 1, 10)
        )

    def test_business_days_between(self):
        self.assertEqual(
            self.cal.business_days_between(pt(2026, 11, 20, 10), pt(2026, 11, 23, 9)), 1
        )
        self.assertEqual(
            self.cal.business_days_between(pt(2026, 11, 20, 10), pt(2026, 11, 20, 18)),
            0,
        )


class PullRequestTest(unittest.TestCase):
    def test_no_response_breaches_after_four_business_days(self):
        [f] = evaluate_pr(pr(), [], pt(2026, 11, 23, 11))
        self.assertEqual(f.section, sd.BREACHED)
        self.assertIn(
            "no maintainer response, overdue by 1 business day (SLA 4 business days)",
            f.detail,
        )
        self.assertEqual(f.people, "reviewers: rev")
        self.assertEqual(f.age, "opened 7 days ago")

    def test_due_soon_on_the_last_business_day(self):
        [f] = evaluate_pr(pr(), [], pt(2026, 11, 19, 11))
        self.assertEqual(f.section, sd.DUE_SOON)
        self.assertIn("due in 1 business day", f.detail)

    def test_within_sla_is_not_reported(self):
        self.assertEqual(evaluate_pr(pr(), [], pt(2026, 11, 18, 11)), [])

    def test_non_responses_do_not_stop_the_clock(self):
        events = [
            comment("copy-pr-bot[bot]", pt(2026, 11, 16, 11), "NONE", bot=True),
            comment("coderabbitai", pt(2026, 11, 16, 11), "NONE", bot=True),
            comment("author", pt(2026, 11, 16, 12), "MEMBER"),
            comment("maint", pt(2026, 11, 16, 13), "MEMBER", body="/ok to test abc123"),
            comment("drive-by", pt(2026, 11, 16, 14), "NONE"),
        ]
        [f] = evaluate_pr(pr(), events, pt(2026, 11, 23, 11))
        self.assertEqual(f.section, sd.BREACHED)

    def test_maintainer_comment_or_review_is_a_response(self):
        now = pt(2026, 11, 23, 11)
        self.assertEqual(
            evaluate_pr(pr(), [comment("maint", pt(2026, 11, 17, 9))], now), []
        )
        self.assertEqual(
            evaluate_pr(pr(), [review("maint", pt(2026, 11, 17, 9), "commented")], now),
            [],
        )

    def test_listed_user_counts_regardless_of_association(self):
        events = [comment("outside-maintainer", pt(2026, 11, 17, 9), "NONE")]
        self.assertEqual(evaluate_pr(pr(), events, pt(2026, 11, 23, 11)), [])

    def test_drafts_are_excluded(self):
        self.assertEqual(evaluate_pr(pr(draft=True), [], pt(2026, 11, 30, 11)), [])

    def test_clock_starts_when_marked_ready(self):
        p = pr(opened=pt(2026, 11, 2, 10))
        events = [actor_event("ready_for_review", "author", pt(2026, 11, 19, 10))]
        self.assertEqual(evaluate_pr(p, events, pt(2026, 11, 20, 11)), [])

    def test_waiting_on_maintainer_after_author_update(self):
        events = [
            review("maint", pt(2026, 11, 17, 9), "changes_requested"),
            {"event": "committed", "committer": {"date": iso(pt(2026, 11, 18, 9))}},
        ]
        [f] = evaluate_pr(pr(), events, pt(2026, 11, 20, 10))
        self.assertEqual(f.section, sd.WAITING)
        self.assertIn(
            "author updated after changes were requested, waiting 2 business days",
            f.detail,
        )

    def test_author_reply_or_rerequest_also_counts(self):
        for update in (
            comment("author", pt(2026, 11, 18, 9)),
            actor_event("review_requested", "author", pt(2026, 11, 18, 9)),
            actor_event("head_ref_force_pushed", "author", pt(2026, 11, 18, 9)),
        ):
            events = [review("maint", pt(2026, 11, 17, 9), "changes_requested"), update]
            [f] = evaluate_pr(pr(), events, pt(2026, 11, 20, 10))
            self.assertEqual(f.section, sd.WAITING)

    def test_not_waiting_once_approved_or_before_update(self):
        approved = [
            review("maint", pt(2026, 11, 17, 9), "changes_requested"),
            comment("author", pt(2026, 11, 18, 9)),
            review("maint", pt(2026, 11, 18, 12), "approved"),
            comment("author", pt(2026, 11, 18, 13)),
        ]
        self.assertEqual(evaluate_pr(pr(), approved, pt(2026, 11, 20, 10)), [])
        no_update = [review("maint", pt(2026, 11, 17, 9), "changes_requested")]
        self.assertEqual(evaluate_pr(pr(), no_update, pt(2026, 11, 20, 10)), [])


class IssueTest(unittest.TestCase):
    def test_priority_sla(self):
        [f] = evaluate_issue(issue(), [], pt(2026, 11, 17, 11))
        self.assertEqual(f.section, sd.BREACHED)
        self.assertIn("no priority, overdue by 1h (SLA 7 days)", f.detail)
        [f] = evaluate_issue(issue(), [], pt(2026, 11, 15, 11))
        self.assertEqual(f.section, sd.DUE_SOON)
        self.assertEqual(evaluate_issue(issue(), [], pt(2026, 11, 12, 11)), [])

    def test_p2_has_no_fix_commitment(self):
        self.assertEqual(
            evaluate_issue(issue(labels=["priority/P2"]), [], pt(2027, 6, 1)), []
        )

    def test_p0_clock_starts_when_labeled(self):
        i = issue(opened=pt(2026, 9, 1, 10), labels=["bug", "priority/P0"])
        events = [
            actor_event(
                "labeled", "maint", pt(2026, 10, 1, 10), label={"name": "priority/P0"}
            )
        ]
        [f] = evaluate_issue(i, events, pt(2026, 11, 17, 11))
        self.assertEqual(f.section, sd.BREACHED)
        self.assertIn("priority/P0 open, overdue by 17 days (SLA 30 days)", f.detail)

    def test_p0_clock_can_start_when_opened(self):
        raw_fix = copy.deepcopy(RAW_CONFIG["issue_fix"])
        raw_fix["clock_starts"] = "opened"
        i = issue(opened=pt(2026, 9, 1, 10), labels=["priority/P0"])
        [f] = evaluate_issue(i, [], pt(2026, 11, 17, 11), config(issue_fix=raw_fix))
        self.assertIn("overdue by 47 days", f.detail)

    def test_p1_months_with_day_warning(self):
        i = issue(opened=pt(2026, 5, 1, 10), labels=["priority/P1"])
        events = [
            actor_event(
                "labeled", "maint", pt(2026, 6, 1, 10), label={"name": "priority/P1"}
            )
        ]
        [f] = evaluate_issue(i, events, pt(2026, 11, 17, 11))
        self.assertEqual(f.section, sd.DUE_SOON)
        self.assertIn("due in 13 days (SLA 6 months)", f.detail)
        self.assertEqual(evaluate_issue(i, events, pt(2026, 10, 17, 11)), [])

    def test_highest_listed_priority_wins(self):
        i = issue(opened=pt(2026, 9, 1, 10), labels=["priority/P2", "priority/P0"])
        [f] = evaluate_issue(i, [], pt(2026, 11, 17, 11))
        self.assertIn("priority/P0", f.detail)


class DigestTest(unittest.TestCase):
    def finding(
        self, n: int, section: str = sd.BREACHED, title: str = "t"
    ) -> sd.Finding:
        return sd.Finding(
            section=section,
            url=f"https://github.com/o/r/pull/{n}",
            ref=f"PR #{n}",
            title=title,
            detail="no maintainer response, overdue by 1 business day (SLA 4 business days)",
            age="opened 7 days ago",
            people="reviewers: rev",
            sort_key=pt(2026, 11, 1) + timedelta(hours=n),
        )

    def test_all_clear(self):
        digest = sd.build_digest(
            config(), "o/r", [], "Yesterday: …", pt(2026, 11, 17, 9)
        )
        self.assertTrue(digest.empty)
        self.assertEqual(digest.heading, "Test Repo daily digest: Tue Nov 17")
        self.assertIn("All clear", sd.to_slack(digest)["blocks"][1]["text"]["text"])
        self.assertIn("All clear", sd.to_markdown(digest))

    def test_sections_are_ordered_sorted_and_capped(self):
        raw_digest = dict(RAW_CONFIG["digest"], max_items_per_section=2)
        findings = [
            self.finding(3),
            self.finding(1),
            self.finding(2),
            self.finding(4, sd.WAITING),
            self.finding(5, sd.DUE_SOON),
        ]
        digest = sd.build_digest(
            config(digest=raw_digest), "o/r", findings, "x", pt(2026, 11, 17, 9)
        )
        self.assertEqual(
            [s.name for s in digest.sections], [sd.BREACHED, sd.DUE_SOON, sd.WAITING]
        )
        breached = digest.sections[0]
        self.assertEqual(breached.total, 3)
        self.assertEqual([f.ref for f in breached.shown], ["PR #1", "PR #2"])
        text = sd.to_slack(digest)["blocks"][1]["text"]["text"]
        self.assertTrue(text.startswith("*SLA breached (3)*"))
        self.assertIn("…and 1 more", text)

    def test_slack_text_is_escaped_and_blocks_stay_under_the_limit(self):
        findings = [self.finding(n, title="<b> & " + "x" * 200) for n in range(1, 60)]
        payload = sd.to_slack(
            sd.build_digest(config(), "o/r", findings, "x", pt(2026, 11, 17, 9))
        )
        texts = [b["text"]["text"] for b in payload["blocks"] if b["type"] == "section"]
        self.assertGreater(len(texts), 1)
        self.assertTrue(all(len(t) <= 3000 for t in texts))
        self.assertIn("&lt;b&gt; &amp; ", texts[0])
        self.assertNotIn("<b>", texts[0])

    def test_activity_window_is_the_last_24_hours_on_weekdays(self):
        cal = config().calendar
        start, end, label = sd.activity_window(cal, pt(2026, 11, 19, 9, 0, 41))
        self.assertEqual(
            (start, end), (pt(2026, 11, 18, 9, 0, 41), pt(2026, 11, 19, 9, 0, 41))
        )
        self.assertEqual(
            label, "Last 24 hours (Wed Nov 18, 9:00 PST to Thu Nov 19, 9:00 PST)"
        )

    def test_activity_window_on_monday_covers_the_weekend(self):
        start, _, label = sd.activity_window(config().calendar, pt(2026, 11, 23, 9))
        self.assertEqual(start, pt(2026, 11, 20, 9))
        self.assertEqual(
            label, "Since Friday (Fri Nov 20, 9:00 PST to Mon Nov 23, 9:00 PST)"
        )

    def test_activity_window_for_weekend_manual_runs(self):
        cal = config().calendar
        start, _, label = sd.activity_window(cal, pt(2026, 11, 21, 14))
        self.assertEqual(start, pt(2026, 11, 20, 14))
        self.assertTrue(label.startswith("Last 24 hours"))
        start, _, label = sd.activity_window(cal, pt(2026, 11, 22, 14))
        self.assertEqual(start, pt(2026, 11, 20, 14))
        self.assertTrue(label.startswith("Since Friday"))

    def test_activity_window_across_daylight_saving(self):
        # Clocks went back on Sun Nov 1, 2026; Friday 9:00 PDT is still the start.
        start, _, label = sd.activity_window(config().calendar, pt(2026, 11, 2, 9))
        self.assertEqual(start, pt(2026, 10, 30, 9))
        self.assertEqual(
            label, "Since Friday (Fri Oct 30, 9:00 PDT to Mon Nov 2, 9:00 PST)"
        )

    def test_activity_window_drops_fractional_seconds(self):
        now = datetime(2026, 11, 19, 17, 0, 41, 123456, tzinfo=timezone.utc)
        start, end, _ = sd.activity_window(config().calendar, now)
        self.assertEqual((start.microsecond, end.microsecond), (0, 0))


def search_item(n, created, pr=True, state="open", draft=False, merged_at=None):
    item = {
        "number": n,
        "title": f"Item {n}",
        "html_url": f"https://github.com/o/r/{'pull' if pr else 'issues'}/{n}",
        "user": {"login": "someone"},
        "state": state,
        "created_at": iso(created),
    }
    if pr:
        item["draft"] = draft
        item["pull_request"] = {"merged_at": merged_at}
    return item


class FakeClient:
    def __init__(self, pulls, issues, timelines, opened=(), opened_total=None):
        self.pulls = pulls
        self.issues = issues
        self.timelines = timelines
        self.opened = list(opened)
        self.opened_total = len(self.opened) if opened_total is None else opened_total
        self.timeline_requests = []
        self.queries = []
        self.searches = []

    def paginate(self, path, params):
        if path.endswith("/pulls"):
            return iter(self.pulls)
        if path.endswith("/issues"):
            return iter(self.issues)
        number = int(path.split("/")[-2])
        self.timeline_requests.append(number)
        return iter(self.timelines.get(number, []))

    def search_count(self, query):
        self.queries.append(query)
        return 2

    def search(self, query, limit):
        self.searches.append((query, limit))
        return self.opened[:limit], self.opened_total


class CollectTest(unittest.TestCase):
    def test_collect(self):
        p0 = issue(11, pt(2026, 9, 1, 10), ["priority/P0"])
        client = FakeClient(
            pulls=[pr(1), pr(2, draft=True)],
            issues=[
                issue(10),
                p0,
                issue(12, labels=["priority/P2"]),
                {"pull_request": {}, **issue(1)},
            ],
            timelines={
                11: [
                    actor_event(
                        "labeled",
                        "maint",
                        pt(2026, 10, 1, 10),
                        label={"name": "priority/P0"},
                    )
                ]
            },
        )
        findings, opened, activity = sd.collect(
            client, "o/r", config(), pt(2026, 11, 23, 11)
        )
        self.assertIsNone(opened)
        self.assertEqual(
            client.searches,
            [
                (
                    "repo:o/r created:2026-11-20T11:00:00-08:00..2026-11-23T11:00:00-08:00",
                    25,
                )
            ],
        )
        self.assertEqual(
            sorted(f.ref for f in findings), ["Issue #10", "Issue #11", "PR #1"]
        )
        self.assertEqual(client.timeline_requests, [1, 11])
        self.assertEqual(len(client.queries), 5)
        self.assertIn(
            "repo:o/r is:pr is:merged merged:2026-11-20T11:00:00-08:00..2026-11-23T11:00:00-08:00",
            client.queries,
        )
        self.assertTrue(
            activity.startswith(
                "Since Friday (Fri Nov 20, 11:00 PST to Mon Nov 23, 11:00 PST): "
                "PRs 2 opened, 2 merged"
            )
        )

    def test_opened_items_are_listed(self):
        opened = [
            search_item(301, pt(2026, 11, 20, 12)),
            search_item(302, pt(2026, 11, 21, 8), draft=True),
            search_item(
                300,
                pt(2026, 11, 20, 13),
                state="closed",
                merged_at="2026-11-20T23:00:00Z",
            ),
            search_item(299, pt(2026, 11, 22, 9), state="closed"),
            search_item(40, pt(2026, 11, 22, 10), pr=False),
        ]
        client = FakeClient([], [], {}, opened=opened, opened_total=7)
        now = pt(2026, 11, 23, 11)
        findings, section, activity = sd.collect(client, "o/r", config(), now)
        self.assertEqual(section.name, "Opened since Friday")
        self.assertEqual(section.total, 7)
        self.assertEqual(
            [(f.ref, f.people) for f in section.shown],
            [
                ("PR #301", "open"),
                ("PR #302", "draft"),
                ("PR #300", "merged"),
                ("PR #299", "closed"),
                ("Issue #40", "open"),
            ],
        )

        digest = sd.build_digest(config(), "o/r", findings, activity, now, section)
        self.assertTrue(digest.empty)
        markdown = sd.to_markdown(digest)
        self.assertIn("All clear", markdown)
        self.assertIn("### Opened since Friday (7)", markdown)
        self.assertIn(
            "- [Issue #40](https://github.com/o/r/issues/40) Item 40 · by someone · open",
            markdown,
        )
        self.assertIn("- …and 2 more", markdown)
        slack = sd.to_slack(digest)["blocks"][2]["text"]["text"]
        self.assertTrue(slack.startswith("*Opened since Friday (7)*"))
        self.assertIn(
            "<https://github.com/o/r/pull/302|PR #302> Item 302 · by someone · draft",
            slack,
        )

    def test_opened_heading_on_weekdays(self):
        start, end, _ = sd.activity_window(config().calendar, pt(2026, 11, 19, 9))
        self.assertEqual(sd.opened_heading(start, end), "Opened in the last 24 hours")


class ConfigTest(unittest.TestCase):
    def test_committed_config_parses(self):
        try:
            from python.runfiles import runfiles

            path = runfiles.Create().Rlocation("_main/.github/sla.toml")
        except ImportError:
            path = None
        if not path:
            path = Path(__file__).resolve().parents[2] / ".github" / "sla.toml"
        with open(path, "rb") as f:
            cfg = sd.parse_config(tomllib.load(f))
        self.assertEqual(
            cfg.pr_first_response, sd.Target(5, "business_days", 1, "business_days")
        )
        self.assertEqual(set(cfg.fix_targets), {"priority/P0", "priority/P1"})
        self.assertEqual(cfg.associations, frozenset())
        self.assertTrue(cfg.users, "the committed config must name maintainers")

    def test_invalid_configs_are_rejected(self):
        cases = {
            "unit": {"pr_first_response": {"duration": 4, "unit": "weeks"}},
            "due_soon": {
                "pr_first_response": {
                    "duration": 4,
                    "unit": "business_days",
                    "due_soon": 4,
                }
            },
            "unknown keys": {"pr_first_respons": {}},
            "not one of": {
                "issue_fix": {
                    "targets": {"P9": {"duration": 1, "unit": "calendar_days"}}
                }
            },
            "timezone": {"timezone": "Mars/Olympus"},
            "business_days": {"business_days": ["Funday"]},
        }
        for message, overrides in cases.items():
            with self.subTest(message):
                with self.assertRaisesRegex(sd.ConfigError, message):
                    config(**overrides)


if __name__ == "__main__":
    unittest.main()
