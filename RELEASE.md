# Release process

Cutting a new operator release, e.g. `v0.1.8`. The maintainer only **bumps the
version and tags** — CI builds the release image and pins the bundle to it. The
bundle CSV is hand-maintained (it does not round-trip through `make bundle`, which
reflows the whole file and dupes `relatedImages`), so CI re-pins the operator
image digest with a surgical helper rather than regenerating.

Prerequisite for tagging: push access to `main`. CI holds the quay push
credentials — you do **not** build or push images locally.

## 0. Pre-flight (after code changes)

```sh
make manifests generate   # ONLY if you changed API types (api/*_types.go)
make fmt vet test         # unit tests (fake client, no cluster needed)
```

> If you changed **API types**, `make manifests` updates `config/crd/`. Also copy
> the regenerated CRDs into `bundle/manifests/` and commit both — CI's *Validate
> generated manifests* job fails on `config/crd` drift. Controller-logic-only
> changes can skip this.

## 1. Bump the version + roll the changelog

Replace the old version with the new one in three spots. The base CSV under
`config/manifests/bases/` stays at its `0.0.0` placeholder — **don't touch it**
(operator-sdk injects the version at generation time).

| File | Field |
|---|---|
| `Makefile` | `VERSION ?= 0.1.8` |
| `config/manager/kustomization.yaml` | `newTag: v0.1.8` |
| `bundle/manifests/…clusterserviceversion.yaml` | `name: …v0.1.8` **and** `version: 0.1.8` |

Roll `CHANGELOG.md`: rename `[Unreleased]` → `[v0.1.8]`, add a fresh empty
`[Unreleased]`. Optionally refresh the CSV `createdAt`.

> **`replaces`**: add `replaces: alibaba-cloud-csi-operator.v<prev>` to the CSV
> only when the previous version is already published to the catalog you're
> updating. The **first** community-operators submission has no predecessor —
> omit it.

> You do NOT pin the operator image digest by hand — CI does it (§ 2). The
> committed bundle may still reference the previous release's digest at tag time;
> CI overwrites it with the new one and commits the result back to `main`.

## 2. Commit, tag, push

```sh
git add Makefile config/manager/kustomization.yaml bundle/ CHANGELOG.md
git commit -m "release: v0.1.8"
git tag v0.1.8
git push origin main --tags
```

On the `v*` tag, CI (the `container-build` job):

- asserts `tag == v$(VERSION)` — fails if the bump wasn't in lockstep;
- **builds + pushes the operator image** `:v0.1.8` (`linux/amd64`, from the tag
  source — so it carries the current Go toolchain / CVE fixes) and `:latest`;
- **pins the committed bundle** to that image's freshly-published digest
  (`hack/pin-operator-digest.py`), then builds + pushes the `-bundle` and
  `-catalog` images from the pinned committed dir (no `make bundle` regeneration);
- **commits the re-pinned bundle back to `main`** (with `[skip ci]`) so the git
  SSOT matches the published image;
- `sync-deploy-tag` bumps `csi_operator_image_tag` in the deploy repo.

CI is the single image builder, so the bundle always references exactly the image
this tag published — no local build, no pre-pin, no non-reproducible digest
mismatch.

## 3. Verify

```sh
git checkout main && git pull            # pick up CI's re-pin commit
git show HEAD:bundle/manifests/*.clusterserviceversion.yaml | grep containerImage
#   -> quay.io/samzhu/alibaba-cloud-csi-operator@sha256:<new digest>
```

Confirm CI is green and `main` carries the re-pinned bundle. The committed
`bundle/` is the SSOT for the community-operators submission (§ 4) and for
`Validate OLM bundle` in CI.

## 4. Publish to OperatorHub (community-operators-prod)

Optional, per release. The OpenShift embedded OperatorHub "Community" tab is
sourced from [`redhat-openshift-ecosystem/community-operators-prod`](https://github.com/redhat-openshift-ecosystem/community-operators-prod)
— a classic bundle-per-version layout (`operators/<name>/<version>/…` + an
operator-level `ci.yaml`). Assemble the submission tree from the committed bundle
(after § 3, so it's digest-pinned):

```sh
hack/build-community-submission.sh                 # VERSION from Makefile
#   -> dist/community-operators/operators/alibaba-cloud-csi-operator/{ci.yaml,<ver>/}
# or write straight into your fork's operators/ dir:
hack/build-community-submission.sh 0.1.8 <fork>/operators
```

It copies `bundle/{manifests,metadata,tests}`, rewrites `bundle.Dockerfile` to
relative `COPY`, writes `ci.yaml` (`updateGraph: replaces-mode` + reviewers), and
runs `operator-sdk bundle validate`. It **refuses to run** if the operator image
isn't digest-pinned.

Then, in a fork of `community-operators-prod`, DCO-sign and PR:

```sh
git add operators/alibaba-cloud-csi-operator
git commit -s -m "operator alibaba-cloud-csi-operator (0.1.8)"
git push && gh pr create --repo redhat-openshift-ecosystem/community-operators-prod
```

The community pipeline validates + a reviewer/bot approves (like the CAPA upstream
PR). First submission = new operator dir; updates = add a new `<version>/` dir.
The **author cannot self-`/lgtm`** (Prow) — a repo maintainer approves.

### Update graph — `replaces` vs `skips` (CSV `spec`)

A new version added to the `stable` channel must connect to its predecessor or the
predecessor **dangles** (`check_dangling_bundles`, a channel-graph check that
**ignores** the OCP range). How you connect depends on the OCP range:

- **First submission**: no `replaces` (no predecessor).
- **Same/narrower OCP range as the predecessor**: add
  `spec.replaces: alibaba-cloud-csi-operator.v<prev>`.
- **WIDER OCP range than the predecessor** (e.g. predecessor caps at 4.20, you
  want 4.22): **use `spec.skips: [alibaba-cloud-csi-operator.v<prev>]`, NOT
  `replaces`** — and set `com.redhat.openshift.versions` to the *disjoint* new
  range (e.g. `v4.21-v4.22`). `replaces` triggers `check_replaces_availability`,
  which requires the replaced bundle to exist in **every** OCP catalog the new
  bundle targets — impossible when the (immutable) predecessor's range is
  narrower. `skips` connects the channel graph without that per-catalog
  requirement. (The truly general answer for per-OCP graphs is **FBC**; the
  pipeline warns to migrate. `skips` is the classic-format workaround.)

`v0.1.9` is the worked example: OCP `v4.21-v4.22`, `spec.skips: [...v0.1.8]`, no
`replaces`.

## Manual pin (rarely needed)

`make bundle-pin-operator VERSION=x.y.z` resolves the pushed `:vx.y.z` image's
digest and re-pins the committed bundle locally. CI does this automatically on a
tag; run it by hand only to repair a bundle out of band.

## Why the bundle isn't regenerated in CI

The CI catalog build builds the bundle image **from the committed dir**
(`make bundle-build bundle-push`), not by regenerating with `make bundle`.
Regenerating reflows the CSV and, with `--use-image-digests`, merges
`relatedImages` into duplicate entries — so the committed bundle is the SSOT, kept
digest-pinned by CI's re-pin step.
