#!/usr/bin/env python3
import datetime as dt
import json
import math
import os
import pathlib
import urllib.error
import urllib.request
from collections import defaultdict

GRAPHQL_URL = "https://api.github.com/graphql"


def github_graphql(token: str, query: str, variables: dict) -> dict:
    payload = json.dumps({"query": query, "variables": variables}).encode("utf-8")
    req = urllib.request.Request(
        GRAPHQL_URL,
        data=payload,
        headers={
            "Authorization": f"Bearer {token}",
            "Content-Type": "application/json",
            "User-Agent": "profile-visual-generator",
        },
        method="POST",
    )
    with urllib.request.urlopen(req, timeout=30) as resp:
        data = json.loads(resp.read().decode("utf-8"))
    if "errors" in data:
        raise RuntimeError(str(data["errors"]))
    return data["data"]


def fetch_profile_data(user: str, token: str) -> tuple[list[dict], dict[str, int], int]:
    to_date = dt.datetime.utcnow().replace(hour=23, minute=59, second=59, microsecond=0)
    from_date = to_date - dt.timedelta(days=365)

    query = """
    query($login: String!, $from: DateTime!, $to: DateTime!) {
      user(login: $login) {
        contributionsCollection(from: $from, to: $to) {
          contributionCalendar {
            weeks {
              contributionDays {
                date
                contributionCount
              }
            }
            totalContributions
          }
        }
        repositories(
          first: 100,
          ownerAffiliations: OWNER,
          isFork: false,
          orderBy: {field: UPDATED_AT, direction: DESC}
        ) {
          nodes {
            languages(first: 10, orderBy: {field: SIZE, direction: DESC}) {
              edges {
                size
                node {
                  name
                }
              }
            }
          }
        }
      }
    }
    """

    data = github_graphql(
        token,
        query,
        {
            "login": user,
            "from": from_date.isoformat() + "Z",
            "to": to_date.isoformat() + "Z",
        },
    )

    user_data = data["user"]
    weeks = user_data["contributionsCollection"]["contributionCalendar"]["weeks"]
    total = user_data["contributionsCollection"]["contributionCalendar"]["totalContributions"]

    days: list[dict] = []
    for week in weeks:
        for day in week["contributionDays"]:
            days.append(
                {
                    "date": day["date"],
                    "count": int(day["contributionCount"]),
                }
            )

    language_sizes: dict[str, int] = defaultdict(int)
    for repo in user_data["repositories"]["nodes"]:
        if not repo or not repo.get("languages"):
            continue
        for edge in repo["languages"].get("edges", []):
            language = edge["node"]["name"]
            size = int(edge["size"])
            language_sizes[language] += size

    return days, dict(language_sizes), int(total)


def generate_commit_skyline(days: list[dict], total: int, username: str) -> str:
    weekly = []
    for i in range(0, len(days), 7):
        weekly.append(sum(d["count"] for d in days[i : i + 7]))

    weekly = weekly[-32:]
    max_value = max(weekly) if weekly else 1

    width, height = 960, 320
    base_y = 250
    bar_w = 20
    gap = 8
    start_x = 80

    bars = []
    windows = []
    for idx, value in enumerate(weekly):
        h = 30 + int((value / max_value) * 170)
        x = start_x + idx * (bar_w + gap)
        y = base_y - h
        delay = round(idx * 0.06, 2)
        bars.append(
            f'<rect class="building" x="{x}" y="{y}" width="{bar_w}" height="{h}" style="animation-delay:{delay}s" />'
        )

        win_rows = max(1, h // 22)
        for r in range(win_rows):
            wx = x + 4
            wy = base_y - (r + 1) * 18
            if wy <= y + 2:
                break
            windows.append(
                f'<rect class="window" x="{wx}" y="{wy}" width="4" height="6" style="animation-delay:{round((idx + r) * 0.1, 2)}s" />'
            )
            windows.append(
                f'<rect class="window" x="{wx + 8}" y="{wy}" width="4" height="6" style="animation-delay:{round((idx + r) * 0.12, 2)}s" />'
            )

    return f"""<svg viewBox=\"0 0 {width} {height}\" xmlns=\"http://www.w3.org/2000/svg\" role=\"img\" aria-label=\"Commit skyline\">
  <defs>
    <linearGradient id=\"sky\" x1=\"0\" y1=\"0\" x2=\"0\" y2=\"1\">
      <stop offset=\"0%\" stop-color=\"#07152d\"/>
      <stop offset=\"100%\" stop-color=\"#0f2e5c\"/>
    </linearGradient>
    <linearGradient id=\"bld\" x1=\"0\" y1=\"0\" x2=\"0\" y2=\"1\">
      <stop offset=\"0%\" stop-color=\"#51a2ff\"/>
      <stop offset=\"100%\" stop-color=\"#2668c6\"/>
    </linearGradient>
    <style>
      .title {{ font: 700 24px monospace; fill: #dce9ff; }}
      .sub {{ font: 500 13px monospace; fill: #98b8ea; }}
      .building {{ fill: url(#bld); transform-origin: center 250px; opacity: 0; animation: rise .7s ease forwards; }}
      .window {{ fill: #ffd86b; opacity: 0; animation: blink 2.4s ease-in-out infinite; }}
      @keyframes rise {{ from {{ transform: scaleY(0.05); opacity: 0; }} to {{ transform: scaleY(1); opacity: 1; }} }}
      @keyframes blink {{ 0%, 35%, 100% {{ opacity: .2; }} 50% {{ opacity: 1; }} }}
    </style>
  </defs>
  <rect width=\"100%\" height=\"100%\" fill=\"url(#sky)\"/>
  <text x=\"36\" y=\"44\" class=\"title\">Commit Skyline</text>
  <text x=\"36\" y=\"66\" class=\"sub\">@{username} • {total} contributions in the last year</text>
  <rect x=\"0\" y=\"250\" width=\"100%\" height=\"70\" fill=\"#071224\"/>
  {''.join(bars)}
  {''.join(windows)}
</svg>
"""


def generate_tech_orbit(language_sizes: dict[str, int], username: str) -> str:
    palette = ["#61dafb", "#f7df1e", "#3178c6", "#9cdbff", "#00d8ff", "#c678dd", "#ff9e64", "#5fd3bc"]
    top = sorted(language_sizes.items(), key=lambda x: x[1], reverse=True)[:8]
    if not top:
        top = [("TypeScript", 10), ("JavaScript", 9), ("Solidity", 8), ("Go", 7)]

    total = sum(v for _, v in top) or 1
    circles = []
    labels = []

    cx, cy = 480, 190
    for i, (name, size) in enumerate(top):
        angle = (2 * math.pi / len(top)) * i
        radius = 105 + (i % 3) * 24
        x = cx + math.cos(angle) * (180 + (i % 2) * 28)
        y = cy + math.sin(angle) * (82 + (i % 3) * 14)
        dot_r = 7 + int((size / total) * 28)
        color = palette[i % len(palette)]
        delay = round(i * 0.23, 2)
        circles.append(
            f'<circle class="orb" cx="{x:.1f}" cy="{y:.1f}" r="{dot_r}" fill="{color}" style="animation-delay:{delay}s"/>'
        )
        labels.append(
            f'<text class="label" x="{x:.1f}" y="{y + dot_r + 15:.1f}" text-anchor="middle">{name}</text>'
        )
        circles.append(
            f'<ellipse class="path" cx="{cx}" cy="{cy}" rx="{radius}" ry="{radius * 0.45:.1f}" transform="rotate({i * 22} {cx} {cy})"/>'
        )

    return f"""<svg viewBox=\"0 0 960 360\" xmlns=\"http://www.w3.org/2000/svg\" role=\"img\" aria-label=\"Tech orbit\">
  <defs>
    <radialGradient id=\"bg\" cx=\"50%\" cy=\"50%\" r=\"70%\">
      <stop offset=\"0%\" stop-color=\"#141c33\"/>
      <stop offset=\"100%\" stop-color=\"#080d1c\"/>
    </radialGradient>
    <style>
      .title {{ font: 700 24px monospace; fill: #e6efff; }}
      .sub {{ font: 500 13px monospace; fill: #9cb2df; }}
      .core {{ fill: #1f4a8a; stroke: #8ebeff; stroke-width: 2; animation: pulse 3s ease-in-out infinite; }}
      .path {{ fill: none; stroke: #2b3f65; stroke-width: 1.1; opacity: .7; }}
      .orb {{ filter: drop-shadow(0 0 6px rgba(124,189,255,.4)); transform-origin: 480px 190px; animation: orbit 13s linear infinite; }}
      .label {{ font: 500 11px monospace; fill: #bfd4ff; }}
      @keyframes orbit {{ from {{ transform: rotate(0deg); }} to {{ transform: rotate(360deg); }} }}
      @keyframes pulse {{ 0%,100% {{ r: 44; }} 50% {{ r: 50; }} }}
    </style>
  </defs>
  <rect width=\"100%\" height=\"100%\" fill=\"url(#bg)\"/>
  <text x=\"36\" y=\"44\" class=\"title\">Tech Orbit</text>
  <text x=\"36\" y=\"66\" class=\"sub\">Top languages from owned repositories • @{username}</text>
  {''.join(circles)}
  <circle class=\"core\" cx=\"480\" cy=\"190\" r=\"44\"/>
  <text x=\"480\" y=\"195\" text-anchor=\"middle\" style=\"font:700 12px monospace; fill:#d9ebff;\">CORE</text>
  {''.join(labels)}
</svg>
"""


def generate_activity_pulse(days: list[dict], username: str) -> str:
    recent = days[-90:] if len(days) >= 90 else days
    values = [d["count"] for d in recent]
    if not values:
        values = [0]
    max_value = max(values) or 1

    width, height = 960, 280
    left, top = 60, 70
    chart_w, chart_h = 860, 150

    points = []
    bars = []
    for i, v in enumerate(values):
        x = left + (i / max(len(values) - 1, 1)) * chart_w
        y = top + chart_h - ((v / max_value) * (chart_h - 8))
        points.append(f"{x:.1f},{y:.1f}")

        bh = max(2, (v / max_value) * 46)
        bx = left + i * (chart_w / max(len(values), 1))
        by = top + chart_h + 8
        bars.append(
            f'<rect class="beat" x="{bx:.1f}" y="{by:.1f}" width="4" height="{bh:.1f}" style="animation-delay:{round(i * 0.03, 2)}s"/>'
        )

    avg = sum(values) / len(values)
    streak = 0
    max_streak = 0
    for v in values:
        if v > 0:
            streak += 1
            max_streak = max(max_streak, streak)
        else:
            streak = 0

    return f"""<svg viewBox=\"0 0 {width} {height}\" xmlns=\"http://www.w3.org/2000/svg\" role=\"img\" aria-label=\"Activity pulse\">
  <defs>
    <linearGradient id=\"bg\" x1=\"0\" y1=\"0\" x2=\"1\" y2=\"1\">
      <stop offset=\"0%\" stop-color=\"#111827\"/>
      <stop offset=\"100%\" stop-color=\"#0b1220\"/>
    </linearGradient>
    <linearGradient id=\"line\" x1=\"0\" y1=\"0\" x2=\"1\" y2=\"0\">
      <stop offset=\"0%\" stop-color=\"#31c7ff\"/>
      <stop offset=\"100%\" stop-color=\"#7af59a\"/>
    </linearGradient>
    <style>
      .title {{ font: 700 24px monospace; fill: #e9f2ff; }}
      .sub {{ font: 500 13px monospace; fill: #9cb0d2; }}
      .grid {{ stroke: #22304d; stroke-width: 1; opacity: .5; }}
      .line {{ fill: none; stroke: url(#line); stroke-width: 3; stroke-linecap: round; stroke-linejoin: round; stroke-dasharray: 1600; stroke-dashoffset: 1600; animation: draw 2.2s ease forwards; }}
      .beat {{ fill: #55e3ff; opacity: .25; transform-origin: center; animation: pulse 1.6s ease-in-out infinite; }}
      @keyframes draw {{ to {{ stroke-dashoffset: 0; }} }}
      @keyframes pulse {{ 0%,100% {{ opacity: .2; }} 50% {{ opacity: .75; }} }}
    </style>
  </defs>
  <rect width=\"100%\" height=\"100%\" fill=\"url(#bg)\"/>
  <text x=\"36\" y=\"42\" class=\"title\">Activity Pulse Timeline</text>
  <text x=\"36\" y=\"63\" class=\"sub\">Last 90 days • avg/day {avg:.1f} • best streak {max_streak} days • @{username}</text>
  <g>
    <line class=\"grid\" x1=\"60\" y1=\"220\" x2=\"920\" y2=\"220\"/>
    <line class=\"grid\" x1=\"60\" y1=\"170\" x2=\"920\" y2=\"170\"/>
    <line class=\"grid\" x1=\"60\" y1=\"120\" x2=\"920\" y2=\"120\"/>
    <line class=\"grid\" x1=\"60\" y1=\"70\" x2=\"920\" y2=\"70\"/>
  </g>
  <polyline class=\"line\" points=\"{' '.join(points)}\"/>
  {''.join(bars)}
</svg>
"""


def main() -> None:
    token = os.environ.get("GITHUB_TOKEN")
    user = os.environ.get("PROFILE_USERNAME") or os.environ.get("GITHUB_REPOSITORY_OWNER")

    if not token:
        raise RuntimeError("GITHUB_TOKEN is required")
    if not user:
        raise RuntimeError("PROFILE_USERNAME or GITHUB_REPOSITORY_OWNER is required")

    days, language_sizes, total = fetch_profile_data(user, token)

    out_dir = pathlib.Path("dist")
    out_dir.mkdir(parents=True, exist_ok=True)

    (out_dir / "commit-skyline.svg").write_text(generate_commit_skyline(days, total, user), encoding="utf-8")
    (out_dir / "tech-orbit.svg").write_text(generate_tech_orbit(language_sizes, user), encoding="utf-8")
    (out_dir / "activity-pulse.svg").write_text(generate_activity_pulse(days, user), encoding="utf-8")


if __name__ == "__main__":
    main()
