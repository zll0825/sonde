#!/usr/bin/env python3
"""Wednesday-level net liquidity: WALCL - WDTGAL - WLRRAOL (millions × 1e6).

Reads a JSON object mapping series id -> [{date, value}, ...] from a file or
stdin. Exits non-zero if WLRRAL (or any non-whitelist series) is present.

This script is offline. It is not a registered metric.
"""

from __future__ import annotations

import json
import sys
from typing import Any

ALLOWED = {"WALCL": 1e6, "WDTGAL": 1e6, "WLRRAOL": 1e6}
BANNED = {"WLRRAL"}


def scale_to_usd(series: str, raw: float) -> float:
    if series == "WLRRAL":
        raise SystemExit("WLRRAL is ~99.85% foreign official reverse repo; use WLRRAOL")
    if series == "RRPONTSYD":
        return raw * 1e9
    if series not in ALLOWED:
        raise SystemExit(f"series {series} is not in the net-liquidity whitelist")
    return raw * ALLOWED[series]


def net_liquidity_usd(walcl: float, wdtgal: float, wlrraol: float) -> float:
    return walcl - wdtgal - wlrraol


def main(argv: list[str]) -> int:
    raw = sys.stdin.read() if not argv else open(argv[0], encoding="utf-8").read()
    payload: dict[str, Any] = json.loads(raw)
    for series in payload:
        if series in BANNED or series not in ALLOWED:
            print(f"banned or unknown series: {series}", file=sys.stderr)
            return 1
    by_date: dict[str, dict[str, float]] = {}
    for series, rows in payload.items():
        for row in rows:
            by_date.setdefault(row["date"], {})[series] = scale_to_usd(series, float(row["value"]))
    for date, vals in sorted(by_date.items()):
        if set(vals) != ALLOWED.keys():
            continue
        print(f"{date} {net_liquidity_usd(vals['WALCL'], vals['WDTGAL'], vals['WLRRAOL']):.0f}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
