"""Minimal self-contained SVG line-chart generator for the static dashboard.

No JS runtime and no vendored charting library: the dashboard is meant to be
opened over an SSH tunnel from a plain browser (see docs/development/perflab.md),
so every chart is rendered server-side as inline SVG. Colors and spacing follow
the dataviz skill's reference palette (references/palette.md): single-hue
sequential blue for the primary series, the fixed status palette for
regression/improvement markers -- never color alone, every marker also carries
a `<title>` (native tooltip) with the value and status word.
"""

from __future__ import annotations

from dataclasses import dataclass

# references/palette.md
SERIES_BLUE = "#2a78d6"
INK_PRIMARY = "#0b0b0b"
INK_SECONDARY = "#52514e"
INK_MUTED = "#898781"
GRIDLINE = "#e1e0d9"
BASELINE = "#c3c2b7"
SURFACE = "#fcfcfb"
STATUS_GOOD = "#0ca30c"
STATUS_WARNING = "#fab219"
STATUS_SERIOUS = "#ec835a"
STATUS_CRITICAL = "#d03b3b"

STATUS_COLOR = {
    "regressed": STATUS_CRITICAL,
    "suspected": STATUS_WARNING,
    "improved": STATUS_GOOD,
    "harness-changed": INK_MUTED,
}


@dataclass
class Point:
    x_label: str   # short commit sha or date
    value: float
    status: str = "neutral"  # neutral|regressed|suspected|improved|harness-changed


def line_chart(points: list[Point], *, width: int = 760, height: int = 220,
               unit: str = "ns/op", title: str = "") -> str:
    """Render `points` (oldest first) as an accessible inline SVG line chart."""
    pad_l, pad_r, pad_t, pad_b = 56, 16, 20, 28
    plot_w, plot_h = width - pad_l - pad_r, height - pad_t - pad_b

    if not points:
        return f'<svg width="{width}" height="{height}" role="img" aria-label="{title}: no data"></svg>'

    values = [p.value for p in points]
    v_min, v_max = min(values), max(values)
    if v_min == v_max:
        v_min, v_max = v_min * 0.95, v_max * 1.05 if v_max else 1.0
    span = v_max - v_min or 1.0

    def sx(i: int) -> float:
        return pad_l + (i / max(len(points) - 1, 1)) * plot_w

    def sy(v: float) -> float:
        return pad_t + (1 - (v - v_min) / span) * plot_h

    coords = [(sx(i), sy(p.value)) for i, p in enumerate(points)]
    path_d = "M " + " L ".join(f"{x:.1f},{y:.1f}" for x, y in coords)

    parts = [
        f'<svg width="{width}" height="{height}" viewBox="0 0 {width} {height}" '
        f'role="img" aria-label="{title} over time, {unit}" '
        f'xmlns="http://www.w3.org/2000/svg" font-family="system-ui, -apple-system, '
        f'\'Segoe UI\', sans-serif">',
        f'<rect x="0" y="0" width="{width}" height="{height}" fill="{SURFACE}"/>',
    ]

    for frac in (0.0, 0.5, 1.0):
        gy = pad_t + frac * plot_h
        gv = v_max - frac * span
        parts.append(f'<line x1="{pad_l}" y1="{gy:.1f}" x2="{width - pad_r}" y2="{gy:.1f}" '
                      f'stroke="{GRIDLINE}" stroke-width="1"/>')
        parts.append(f'<text x="{pad_l - 8}" y="{gy + 4:.1f}" font-size="11" fill="{INK_MUTED}" '
                      f'text-anchor="end">{gv:,.0f}</text>')

    parts.append(f'<line x1="{pad_l}" y1="{pad_t + plot_h}" x2="{width - pad_r}" '
                  f'y2="{pad_t + plot_h}" stroke="{BASELINE}" stroke-width="1"/>')

    parts.append(f'<path d="{path_d}" fill="none" stroke="{SERIES_BLUE}" '
                  f'stroke-width="2" stroke-linejoin="round" stroke-linecap="round"/>')

    step = max(1, len(points) // 12)
    for i, (p, (x, y)) in enumerate(zip(points, coords)):
        color = STATUS_COLOR.get(p.status, SERIES_BLUE)
        r = 4 if p.status != "neutral" else 2.5
        parts.append(f'<circle cx="{x:.1f}" cy="{y:.1f}" r="{r}" fill="{color}" '
                      f'stroke="{SURFACE}" stroke-width="1"><title>{p.x_label}: '
                      f'{p.value:,.0f} {unit} ({p.status})</title></circle>')
        if i % step == 0 or i == len(points) - 1:
            parts.append(f'<text x="{x:.1f}" y="{height - 8}" font-size="10" '
                          f'fill="{INK_MUTED}" text-anchor="middle">{p.x_label}</text>')

    parts.append("</svg>")
    return "\n".join(parts)


def sparkline(values: list[float], *, width: int = 140, height: int = 32,
              status: str = "neutral") -> str:
    """A tiny trend-only line, no axes -- for the overview table."""
    if not values:
        return f'<svg width="{width}" height="{height}"></svg>'
    v_min, v_max = min(values), max(values)
    span = (v_max - v_min) or 1.0
    n = len(values)
    pts = [
        (4 + (i / max(n - 1, 1)) * (width - 8), height - 4 - ((v - v_min) / span) * (height - 8))
        for i, v in enumerate(values)
    ]
    path_d = "M " + " L ".join(f"{x:.1f},{y:.1f}" for x, y in pts)
    color = STATUS_COLOR.get(status, SERIES_BLUE)
    return (
        f'<svg width="{width}" height="{height}" viewBox="0 0 {width} {height}" '
        f'role="img" aria-hidden="true" xmlns="http://www.w3.org/2000/svg">'
        f'<path d="{path_d}" fill="none" stroke="{color}" stroke-width="2" '
        f'stroke-linejoin="round" stroke-linecap="round"/></svg>'
    )
