#!/usr/bin/env python3
"""Make libovsdb modelgen output identical on every CPU architecture.

libovsdb v0.8.1 ovsdb.BaseType.MaxLength() returns int(math.Pow(2, 63)) - 1
for strings without a maxLength. The float -> int conversion overflows, which Go
leaves implementation-defined:
  - amd64 yields MinInt64, -1 wraps to MaxInt64 == modelgen's "no limit"
    default, so no tag is emitted;
  - arm64 saturates to MaxInt64, -1 gives 9223372036854775806, so modelgen emits
    a meaningless validate:"max=9223372036854775806".

The amd64 output is the intended one. This removes that token from validate
tags, then the wrappers it leaves empty, exactly as amd64 modelgen omits them.
On amd64 it changes nothing. Tags only feed write-side validation, which this
read-only repository never runs.

Usage: normalize-models.py DIR [DIR...]
"""
import pathlib
import re
import sys

BOGUS = "max=9223372036854775806"
TAG = re.compile(r' validate:"([^"]*)"')


def normalize(rules: str) -> str:
    parts = [p for p in rules.split(",") if p != BOGUS]
    out = []
    i = 0
    while i < len(parts):
        # an empty map-key section left behind
        if parts[i] == "keys" and i + 1 < len(parts) and parts[i + 1] == "endkeys":
            i += 2
            continue
        out.append(parts[i])
        i += 1
    while out and out[-1] == "dive":  # nothing left to dive into
        out.pop()
    if out == ["omitempty"]:  # nothing left to skip when empty
        out = []
    return ",".join(out)


def fix(match: re.Match) -> str:
    rules = normalize(match.group(1))
    return f' validate:"{rules}"' if rules else ""


def main(dirs):
    changed = 0
    for d in dirs:
        for path in sorted(pathlib.Path(d).glob("*.go")):
            src = path.read_text()
            if BOGUS not in src:
                continue
            path.write_text(TAG.sub(fix, src))
            changed += 1
    if changed:
        print(f"normalized {changed} model file(s) (arm64 maxLength overflow)")


if __name__ == "__main__":
    if len(sys.argv) < 2:
        sys.exit(__doc__)
    main(sys.argv[1:])
