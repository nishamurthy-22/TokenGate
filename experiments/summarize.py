"""Print a summary table from Locust *_stats.csv files.

Usage: python3 experiments/summarize.py results/a_stats.csv results/b_stats.csv

Rows shown per file:
  Aggregated  all requests (POST rows measure time until response headers)
  FULL        time until the last token, per app
  TTFT        time to first token, per app
"""

import csv
import os
import sys


def rows(path):
    with open(path, newline="") as f:
        for row in csv.DictReader(f):
            kind = row.get("Type", "")
            name = row.get("Name", "")
            if name == "Aggregated" or kind in ("FULL", "TTFT"):
                yield kind or "ALL", name, row


def main(paths):
    if not paths:
        sys.exit(__doc__)
    cols = ["run", "type", "name", "requests", "failures", "fail_%", "req/s", "p50_ms", "p95_ms", "p99_ms"]
    print("| " + " | ".join(cols) + " |")
    print("|" + "---|" * len(cols))
    for path in paths:
        run = os.path.basename(path).replace("_stats.csv", "")
        for kind, name, r in rows(path):
            reqs = int(r.get("Request Count", 0) or 0)
            fails = int(r.get("Failure Count", 0) or 0)
            pct = 100 * fails / reqs if reqs else 0
            values = [
                run, kind, name, str(reqs), str(fails), f"{pct:.1f}",
                f"{float(r.get('Requests/s', 0) or 0):.1f}",
                r.get("50%", ""), r.get("95%", ""), r.get("99%", ""),
            ]
            print("| " + " | ".join(values) + " |")


if __name__ == "__main__":
    main(sys.argv[1:])
