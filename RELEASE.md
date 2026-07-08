# Release process

Cutting a new operator release, e.g. `v0.1.8`. The bundle CSV is **hand-
maintained** — it does not round-trip through `make bundle` (that reflows the
whole file and rewrites `createdAt`), so we bump versions by hand and re-pin the
operator image digest with a helper, rather than regenerating.

## 1. Bump the version (four spots)

Replace the old version with the new one in:

| File | Field |
|---|---|
| `Makefile` | `VERSION ?= 0.1.8` |
| `config/manager/kustomization.yaml` | `newTag: v0.1.8` |
| `bundle/manifests/…clusterserviceversion.yaml` | `name: alibaba-cloud-csi-operator.v0.1.8` |
| `bundle/manifests/…clusterserviceversion.yaml` | `version: 0.1.8` (and add `replaces: alibaba-cloud-csi-operator.v0.1.7`) |

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
git add Makefile config/manager/kustomization.yaml bundle/
git commit -m "release: v0.1.8"
git tag v0.1.8
git push origin main --tags
```

On the `v*` tag, CI:
- pushes the operator image (again — idempotent),
- builds + pushes the `-bundle` and `-catalog` images (the air-gap / CatalogSource
  artifacts),
- runs `sync-deploy-tag` to bump `csi_operator_image_tag` in the deploy repo.

The **committed `bundle/`** you just pushed is the single source of truth for the
community-operators submission (fully digest-pinned) and for `Validate OLM bundle`
in CI. See [docs/QUICKSTART.md](docs/QUICKSTART.md) for the install paths.

## Why the bundle isn't regenerated in CI

The CI catalog-build path historically ran `make bundle` (regenerate) before
building the bundle image. That reflows the CSV and, with `--use-image-digests`,
merges `relatedImages` into duplicate entries. Since the committed bundle is the
SSOT, prefer building the image **from the committed dir** — i.e. `make
bundle-build bundle-push` without a preceding `make bundle`. (Optional cleanup;
requires a workflow edit.)
