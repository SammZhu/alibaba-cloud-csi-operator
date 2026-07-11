#!/usr/bin/env bash
# Assemble the community-operators-prod submission tree for this operator from the
# committed bundle/, then validate it. The submission uses the classic
# bundle-per-version layout:
#
#   <operators-dir>/alibaba-cloud-csi-operator/
#   ├── ci.yaml                      (operator-level: updateGraph + reviewers)
#   └── <version>/
#       ├── bundle.Dockerfile        (relative COPY, unlike the repo's bundle.Dockerfile)
#       ├── manifests/               (from bundle/manifests)
#       ├── metadata/annotations.yaml
#       └── tests/scorecard/config.yaml
#
# The committed bundle/ is the source of truth (already digest-pinned via
# `make bundle-pin-operator`). This only re-lays it into the submission shape.
#
# Usage:
#   hack/build-community-submission.sh [VERSION] [OPERATORS_DIR]
#     VERSION        default: the Makefile VERSION (e.g. 0.1.7)
#     OPERATORS_DIR  default: ./dist/community-operators/operators (throwaway).
#                    Pass your community-operators-prod fork's `operators/` dir to
#                    write the version straight into the fork.
#
# Then, in your fork:  git add operators/alibaba-cloud-csi-operator
#                      git commit -s -m "operator alibaba-cloud-csi-operator (<version>)"
#                      git push && gh pr create --repo redhat-openshift-ecosystem/community-operators-prod
set -euo pipefail

OPERATOR=alibaba-cloud-csi-operator
REVIEWERS=("SammZhu")          # community-operators ci.yaml reviewers (GitHub handles)
UPDATE_GRAPH=replaces-mode     # or semver-mode (one-way — see community docs)

here="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$here"

VERSION="${1:-$(sed -nE 's/^VERSION[[:space:]]*\?=[[:space:]]*//p' Makefile | head -1)}"
OPERATORS_DIR="${2:-$here/dist/community-operators/operators}"

bundle_csv="bundle/manifests/${OPERATOR}.clusterserviceversion.yaml"
[ -f "$bundle_csv" ] || { echo "ERROR: $bundle_csv not found — run from the operator repo"; exit 2; }

# Guard: the committed bundle must be digest-pinned — never submit a tag-based
# operator image. (`make bundle-pin-operator` sets @sha256; see RELEASE.md.)
if grep -qE "image: quay.io/samzhu/${OPERATOR}:v" "$bundle_csv"; then
  echo "ERROR: bundle references the operator image by TAG, not digest."
  echo "       Run 'make bundle-pin-operator' first (see RELEASE.md)."
  exit 3
fi

opdir="$OPERATORS_DIR/$OPERATOR"
verdir="$opdir/$VERSION"
echo ">> assembling $verdir"
rm -rf "$verdir"
mkdir -p "$verdir/tests/scorecard"

cp -R bundle/manifests "$verdir/manifests"
cp -R bundle/metadata  "$verdir/metadata"
cp bundle/tests/scorecard/config.yaml "$verdir/tests/scorecard/"

# bundle.Dockerfile: the repo's copies from bundle/ (COPY bundle/manifests ...);
# in the submission the dirs are siblings, so make the COPY paths relative.
sed -E 's#COPY bundle/#COPY #' bundle.Dockerfile > "$verdir/bundle.Dockerfile"

# ci.yaml is operator-level (shared across versions) — write it once; don't
# clobber an existing one (a maintainer may have tuned it).
if [ ! -f "$opdir/ci.yaml" ]; then
  {
    echo "# Community operator publishing settings."
    echo "updateGraph: $UPDATE_GRAPH"
    echo ""
    echo "reviewers:"
    for r in "${REVIEWERS[@]}"; do echo "  - $r"; done
  } > "$opdir/ci.yaml"
  echo ">> wrote $opdir/ci.yaml"
else
  echo ">> $opdir/ci.yaml exists — left as-is"
fi

# Validate the assembled version dir if operator-sdk is available.
if command -v operator-sdk >/dev/null 2>&1; then
  echo ">> operator-sdk bundle validate ($VERSION)"
  operator-sdk bundle validate "$verdir" --select-optional suite=operatorframework
  operator-sdk bundle validate "$verdir" --select-optional name=good-practices
else
  echo ">> (operator-sdk not on PATH — skipping validation; the community pipeline validates too)"
fi

echo
echo "OK — submission tree at: $opdir"
echo "Next (in your community-operators-prod fork):"
echo "  cp -R \"$opdir\" <fork>/operators/"
echo "  git -C <fork> add operators/$OPERATOR"
echo "  git -C <fork> commit -s -m \"operator $OPERATOR ($VERSION)\""
echo "  git -C <fork> push && gh pr create --repo redhat-openshift-ecosystem/community-operators-prod"
