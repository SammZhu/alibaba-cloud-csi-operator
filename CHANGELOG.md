# Changelog

All notable changes to this project are documented here. The format loosely
follows [Keep a Changelog](https://keepachangelog.com/); the project follows
semantic versioning within the `v0.1.x` line.

## [Unreleased]

## [v0.1.9]
- **OpenShift 4.21/4.22 support** via `com.redhat.openshift.versions: v4.21-v4.22`.
  Code is identical to v0.1.8; this is a bundle-metadata release that adds the
  operator to the 4.21/4.22 community catalogs. The range is **disjoint** from
  v0.1.8 (v4.15-v4.20) with **no `replaces`** on purpose: community-operators
  requires a `replaces` target to exist in every OCP catalog the new bundle
  publishes to, and v0.1.8 is capped at 4.20 — so v0.1.9 is a separate head on
  4.21/4.22 rather than a wide-range successor. Validated on **OpenShift 4.22
  (CRC 2.62)**: OLM install + full reconcile (CSIDriver / StorageClasses /
  controller Deployments / node DaemonSets) + `Available=True`,
  `diskDriverReady`/`nasDriverReady=true`.

## [v0.1.8]
- **bundle (community-operators)**: drop the two kubebuilder NetworkPolicy objects (OLM does not support NetworkPolicy as a bundle resource) and add `com.redhat.openshift.versions: v4.15-v4.20` to `metadata/annotations.yaml` (required when `minKubeVersion` is set).

- **Platform-adaptive privileged admission**: detect the OpenShift SCC API via the
  RESTMapper — bind the privileged SCC on OpenShift, or label the operator
  namespace for privileged Pod Security Admission on vanilla Kubernetes. Makes the
  operator installable off OpenShift (prerequisite for an operatorhub.io listing).
- **Digest-pinned bundle**: all images in the OLM bundle are pinned by `@sha256`
  and listed in `relatedImages` — the operator's own image plus the six external
  sidecars, now sourced from `registry.k8s.io/sig-storage` via `RELATED_IMAGE_*`
  (the previous `acs/*` tags didn't resolve publicly). A complete disconnected
  image set for operatorhub / air-gapped installs.
- **Release flow**: the operator image, bundle digest-pinning, and the
  bundle/catalog images are all produced by CI on a `v*` tag — the maintainer only
  bumps the version and tags. CI pins the committed bundle to the freshly-published
  image digest and commits it back to `main`. `make bundle-pin-operator` remains
  for manual repair. See [RELEASE.md](RELEASE.md).
- **CSV metadata**: icon, `provider`, `maturity`, `minKubeVersion`, and support
  contact — for a clean operatorhub listing.
- **Onboarding**: `make demo` (hermetic kind run, no cloud) + `docs/QUICKSTART.md`
  front door with a "which project do I need?" cross-reference.
- **deps**: bump `golang.org/x/net` to v0.56.0 (HIGH/CRITICAL CVE fixes —
  HTTP/2, idna, html); bump the Go toolchain to 1.26.4.
- **ci**: Trivy now scans the locally-built image tarball so the CVE gate runs on
  PRs too (previously failed with MANIFEST_UNKNOWN — the image is not pushed on PRs);
  add a `govulncheck` gate and Dependabot.
- **security**: bump the Go toolchain to 1.26.5 (GO-2026-5856, `crypto/tls`).
- **bundle metadata**: digest-pin the operator's own image (added to
  `relatedImages`); add the `repository` annotation; unify the maintainer to
  "Sam Choo" with a plus-alias contact email.
- **submission**: `hack/build-community-submission.sh` assembles the
  community-operators-prod tree from the committed bundle (RELEASE.md § 4).

## [v0.1.7]
- Grant `volumesnapshotcontents/status` RBAC so VolumeSnapshots reach `readyToUse`.

## [v0.1.6]
- Disk node plugin's `NodeGetInfo` issues zero ECS API calls
  (`MAX_VOLUMES_PERNODE` + `DISK_ALLOW_ALL_TYPE`) — removes the dependency on node
  IMDS/ECS reachability at registration (zone-c clock/egress hardening).

## [v0.1.5]
- Controller `podAntiAffinity` (spread the HA replicas) + node csi-plugin self-heal.

## [v0.1.4]
- csi-provisioner `--leader-election` (2 replicas no longer collide on
  IdempotentProcessing); driver ClusterRole gains `persistentvolumes: create/delete`
  (PV binding) and `coordination.k8s.io/leases` (leader election).

## [v0.1.3]
- Distinct liveness-probe `--health-port` per node DaemonSet.

## [v0.1.2]
- `ECS_ENDPOINT` override for air-gapped node registration.

## [v0.1.1]
- Driver pod-spec fixes (controller socket path + node host-port).

## [v0.1.0]
- Initial operator release line, decoupled from the upstream CSI driver version.
