#!/usr/bin/env python3
"""SOFR−IORB spread in basis points.

For each SOFR date, take the IORB observation that is already effective
(latest effective_date <= sofr_date). Do not pair a SOFR date with a future
IORB that is not yet effective (FRED publishes IORB ~7 days ahead).

Input JSON: {"sofr": [{date, value}, ...], "iorb": [{date, value}, ...]}
This script is offline. It is not a registered metric.
"""

from __future__ import annotations

import json
import sys
from datetime import date, datetime
from typing import Any, Optional, Tuple


def parse_date(s: str) -> date:
    return datetime.strptime(s, "%Y-%m-%d").date()


def match_iorb(sofr_date: date, iorbs: list) -> Optional[float]:
    best: Optional[Tuple[date, float]] = None
    for d, v in iorbs:
        if d > sofr_date:
            continue
        if best is None or d > best[0]:
            best = (d, v)
    return None if best is None else best[1]


def spread_bp(sofr: float, iorb: float) -> float:
    return (sofr - iorb) * 100


def main(argv: list[str]) -> int:
    raw = sys.stdin.read() if not argv else open(argv[0], encoding="utf-8").read()
    payload: dict[str, Any] = json.loads(raw)
    iorbs = [(parse_date(r["date"]), float(r["value"])) for r in payload.get("iorb", [])]
    for row in payload.get("sofr", []):
        d = parse_date(row["date"])
        iorb = match_iorb(d, iorbs)
        if iorb is None:
            print(f"{row['date']} unmatched", file=sys.stderr)
            continue
        print(f"{row['date']} {spread_bp(float(row['value']), iorb):.4f}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
