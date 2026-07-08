#!/usr/bin/env python3
"""Surgically pin the operator's OWN image by digest in the committed bundle CSV.

The bundle CSV is hand-maintained (it does NOT round-trip through
`operator-sdk generate bundle` — that reflows the whole file and rewrites
createdAt). So at release we do NOT regenerate; we only swap the operator image
reference to the pushed image's digest, in the three places it appears:

  1. metadata.annotations.containerImage
  2. the manager Deployment container `image:`
  3. the `relatedImages` entry named `manager`

The 6 external sig-storage sidecars are already digest-pinned and are left
untouched (they change only on a sidecar version bump). Idempotent: re-running
with the same digest is a no-op. Fails loudly if the operator image is not found
or the digest is not a sha256.

Usage:
  pin-operator-digest.py <csv> --repo <operator-repo-without-tag> --digest sha256:<hex>

`make bundle-pin-operator` resolves the digest for you and calls this.
"""
from __future__ import annotations

import argparse
import re
import sys


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("csv")
    ap.add_argument("--repo", required=True,
                    help="operator image repo WITHOUT tag/digest, "
                         "e.g. quay.io/samzhu/alibaba-cloud-csi-operator")
    ap.add_argument("--digest", required=True, help="sha256:<hex>")
    args = ap.parse_args()

    if not re.fullmatch(r"sha256:[0-9a-f]{64}", args.digest):
        print(f"ERROR: --digest must be sha256:<64 hex>, got {args.digest!r}",
              file=sys.stderr)
        return 2

    pinned = f"{args.repo}@{args.digest}"
    # Match the operator repo followed by :tag OR @sha256:... (either current form).
    ref = re.compile(re.escape(args.repo) + r"(?::[\w.\-]+|@sha256:[0-9a-f]{64})")

    with open(args.csv, encoding="utf-8") as fh:
        text = fh.read()

    matches = ref.findall(text)
    if not matches:
        print(f"ERROR: operator image {args.repo} not found in {args.csv}",
              file=sys.stderr)
        return 3

    new_text, n = ref.subn(pinned, text)
    if new_text == text:
        print(f"already pinned to {args.digest} ({n} refs) — no change")
        return 0

    with open(args.csv, "w", encoding="utf-8") as fh:
        fh.write(new_text)
    print(f"pinned operator image -> {pinned} ({n} references updated)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
