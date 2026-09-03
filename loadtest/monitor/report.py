#!/usr/bin/env python3
"""Turn one or more monitor CSVs into a self-contained HTML report: per-metric
charts, and — when two or more --run flags are given — a side-by-side
comparison table (used for the Level A vs. Level B headroom report). No
third-party dependencies (stdlib only), so it runs anywhere Python 3 does.

Usage:
  python3 report.py --run "300 candidates=loadtest/results/cert300-.../monitor.csv" --out report.html
  python3 report.py --run "200 candidates=.../monitor.csv" --run "300 candidates=.../monitor.csv" --out compare.html
"""
import argparse
import csv
import html
import statistics as stats

NUMERIC_COLUMNS = [
    "db_cpu_pct", "db_mem_mb", "piston_cpu_pct", "piston_mem_mb",
    "runner_cpu_pct", "runner_mem_mb", "pg_connections", "api_cpu_pct", "api_rss_mb",
]


def load_csv(path):
    with open(path, newline="") as f:
        return list(csv.DictReader(f))


def to_float(v):
    try:
        return float(v)
    except (TypeError, ValueError):
        return None


def column_values(rows, col):
    return [x for x in (to_float(r.get(col)) for r in rows) if x is not None]


def summarize(rows):
    out = {}
    for col in NUMERIC_COLUMNS:
        vals = column_values(rows, col)
        out[col] = None if not vals else {
            "avg": stats.mean(vals), "max": max(vals), "min": min(vals), "n": len(vals),
        }
    return out


def sparkline_svg(vals, width=480, height=80, color="#2563eb"):
    if not vals:
        return "<em>no data</em>"
    lo, hi = min(vals), max(vals)
    span = (hi - lo) or 1.0
    step = width / max(len(vals) - 1, 1)
    points = " ".join(
        f"{i * step:.1f},{height - ((v - lo) / span) * height:.1f}" for i, v in enumerate(vals)
    )
    return (
        f'<svg viewBox="0 0 {width} {height}" width="{width}" height="{height}" '
        f'xmlns="http://www.w3.org/2000/svg" style="background:#f8fafc;border:1px solid #e2e8f0">'
        f'<polyline fill="none" stroke="{color}" stroke-width="2" points="{points}" /></svg>'
    )


def fmt(v):
    return "-" if v is None else f"{v:.1f}"


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument(
        "--run", action="append", required=True, metavar="LABEL=CSV_PATH",
        help="a run to render; pass twice for a comparison report (Level A vs Level B)",
    )
    ap.add_argument("--out", required=True, help="output HTML file path")
    args = ap.parse_args()

    runs = []
    for spec in args.run:
        label, sep, path = spec.partition("=")
        if not sep:
            raise SystemExit(f"--run must be LABEL=CSV_PATH, got: {spec!r}")
        rows = load_csv(path)
        runs.append((label, path, rows, summarize(rows)))

    parts = [
        "<!doctype html><meta charset=utf-8><title>Load test report</title>",
        "<style>body{font-family:system-ui,sans-serif;margin:2rem;color:#0f172a}"
        "table{border-collapse:collapse;margin:1rem 0}td,th{border:1px solid #e2e8f0;"
        "padding:.4rem .7rem;text-align:right}th{text-align:left;background:#f1f5f9}"
        "h2{margin-top:2rem}.meta{color:#64748b;font-weight:normal}</style>",
    ]

    if len(runs) > 1:
        parts.append("<h1>Load test comparison report</h1>")
        parts.append(
            "<table><tr><th>Metric</th>"
            + "".join(f"<th>{html.escape(label)}</th>" for label, _, _, _ in runs)
            + "</tr>"
        )
        for col in NUMERIC_COLUMNS:
            cells = []
            for _, _, _, summary in runs:
                s = summary[col]
                cells.append("<td>no data</td>" if s is None else f"<td>avg {fmt(s['avg'])} / max {fmt(s['max'])}</td>")
            parts.append(f"<tr><td>{col}</td>{''.join(cells)}</tr>")
        parts.append("</table>")
    else:
        parts.append(f"<h1>Load test report — {html.escape(runs[0][0])}</h1>")

    for label, path, rows, summary in runs:
        parts.append(
            f"<h2>{html.escape(label)} <span class=meta>({html.escape(path)}, {len(rows)} samples)</span></h2>"
        )
        for col in NUMERIC_COLUMNS:
            vals = column_values(rows, col)
            s = summary[col]
            stat_line = "no data" if s is None else f"avg {s['avg']:.1f}, max {s['max']:.1f}, min {s['min']:.1f} (n={s['n']})"
            parts.append(f"<div><strong>{col}</strong> — {stat_line}<br>{sparkline_svg(vals)}</div>")

    with open(args.out, "w") as f:
        f.write("\n".join(parts))
    print(f"wrote {args.out}")


if __name__ == "__main__":
    main()
