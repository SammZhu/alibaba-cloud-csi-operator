# Release process

Cutting a new operator release, e.g. `v0.1.8`. The bundle CSV is **hand-
maintained** — it does not round-trip through `make bundle` (that reflows the
whole file and rewrites `createdAt`), so we bump versions by hand and re-pin the
operator image digest with a helper, rather than regenerating.

Prerequisites: `podman login quay.io` (push rights), `operator-sdk v1.42.2`,
Go 1.26+.

## 0. Pre-flight (after code changes)

```sh
make manifests generate   # ONLY if you changed API types (api/*_types.go)
make fmt vet test         # unit tests (fake client, no cluster needed)
```

> If you changed **API types**, `make manifests` updates `config/crd/`. Also copy
> the regenerated CRDs into `bundle/manifests/` and commit both — CI's *Validate
> generated manifests* job fails on `config/crd` drift. Controller-logic-only
> changes can skip this step.

## 1. Bump the version (four spots)

Replace the old version with the new one in:

| File | Field |
|---|---|
| `Makefile` | `VERSION ?= 0.1.8` |
| `config/manager/kustomization.yaml` | `newTag: v0.1.8` |
| `bundle/manifests/…clusterserviceversion.yaml` | `name: alibaba-cloud-csi-operator.v0.1.8` |
| `bundle/manifests/…clusterserviceversion.yaml` | `version: 0.1.8` (and add `replaces: alibaba-cloud-csi-operator.v0.1.7`) |

Also roll `CHANGELOG.md`: rename the `[Unreleased]` heading to `[v0.1.8]` and add a
fresh empty `[Unreleased]` above it. Optionally refresh the CSV `createdAt`
timestamp to the release date.

> The deploy repo's `ansible/vars/images.yml` `csi_operator_image_tag` is bumped
> **automatically** by the `sync-deploy-tag` CI job on the tag push — don't edit it
> by hand. See that file's comment for the tag-vs-digest split.

## 2. Build + push the operator image

The digest must exist in the registry before we can pin it:

```sh
make docker-build docker-push VERSION=0.1.8
```

## 3. Pin the operator's own image by digest in the bundle

```sh
make bundle-pin-operator VERSION=0.1.8
```

This resolves the pushed image's digest and rewrites the **three** operator-image
references in the CSV (`containerImage` annotation, manager Deployment `image:`,
and the `relatedImages` `manager` entry) to `…@sha256:…`. The six sig-storage
sidecars are already digest-pinned and are left untouched. Idempotent, and it runs
`operator-sdk bundle validate` at the end.

> If you also bumped a **sidecar** version, update its `RELATED_IMAGE_*` value in
> `config/manager/manager.yaml` and the matching `relatedImages` digest in the CSV
> by hand — those are the only other images.

## 4. Commit, tag, push

```sh
git add Makefile config/manager/kustomization.yaml bundle/ CHANGELOG.md
git commit -m "release: v0.1.8"
git tag v0.1.8
git push origin main --tags
```

On the `v*` tag, CI:
- asserts `tag == v$(VERSION)` — fails loudly if you didn't bump all four spots;
- **guards** that the committed bundle references this release's operator image by
  digest — fails if you skipped step 3 (`make bundle-pin-operator`);
- builds + pushes the `-bundle` and `-catalog` images **from the committed bundle
  dir** (the SSOT — no regeneration), for the air-gap / CatalogSource path;
- runs `sync-deploy-tag` to bump `csi_operator_image_tag` in the deploy repo.

Both guards **fail loudly** — a half-done release never ships silently.

## 5. Verify

```sh
# CI green (esp. the release job's guard + `Validate OLM bundle`), then:
git show HEAD:bundle/manifests/*.clusterserviceversion.yaml | grep containerImage
#   -> quay.io/samzhu/alibaba-cloud-csi-operator@sha256:<new digest>
```

The **committed `bundle/`** is the single source of truth for the
community-operators submission (fully digest-pinned) and for `Validate OLM bundle`
in CI. See [docs/QUICKSTART.md](docs/QUICKSTART.md) for the install paths.

## Why the bundle isn't regenerated in CI

The CI catalog-build path builds the bundle image **from the committed dir**
(`make bundle-build bundle-push`), not by regenerating with `make bundle`.
Regenerating reflows the CSV and, with `--use-image-digests`, merges
`relatedImages` into duplicate entries — so the committed bundle is the SSOT, kept
digest-pinned by step 3.
