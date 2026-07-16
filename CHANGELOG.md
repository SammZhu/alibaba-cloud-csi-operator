# Changelog

All notable changes to this project are documented here. The format loosely
follows [Keep a Changelog](https://keepachangelog.com/); the project follows
semantic versioning within the `v0.1.x` line.

## [Unreleased]

## [v0.1.14]
- **Backport the fixes to OpenShift 4.15–4.20** (bundle-metadata only; operand image
  is byte-identical to v0.1.13, same digest — no rebuild). Until now the fixes only
  reached 4.21–4.22: v0.1.8 was the 4.15–4.20 head (old code) while v0.1.9→v0.1.13
  formed a disjoint 4.21–4.22 track. This release widens
  `com.redhat.openshift.versions` to **`v4.15-v4.22`** so every supported OpenShift
  gets the owner-reference garbage collection (clean uninstall) and the robust SCC
  detection. Update graph: **`spec.skips: [v0.1.8, v0.1.13]`** — a single channel
  cannot have two heads, so instead of a 4.15–4.20-only bundle that would replace
  v0.1.8 (and leave v0.1.13 as a second head), v0.1.14 spans the full range and
  skips both prior heads, becoming the sole head. `skips` avoids the
  `check_replaces_availability` constraint (a `replaces` target must exist in every
  target OCP catalog, which v0.1.8/v0.1.13 do not across the widened range).

## [v0.1.13]
- **Robust OpenShift SCC detection**: `sccAvailable()` no longer trusts only the
  manager's cached RESTMapper. If the operator Pod's first API discovery ran while
  the apiserver was degraded (observed on OpenShift 4.22 / CRC when the node was
  under DiskPressure and the operator restarted), the stale cached mapper missed
  `security.openshift.io`, so the operator skipped the privileged SCC binding and
  fell back to Pod Security Admission on a real OpenShift cluster — which can get
  the node CSI DaemonSet denied privileged admission (mount failures). A cached
  mapper hit stays authoritative; a miss is now re-checked against a fresh
  discovery client before concluding the SCC API is absent. Genuine non-OpenShift
  clusters still take the PSA path.

## [v0.1.12]
- **Clean uninstall via owner references**: every resource the operator creates —
  the cluster-scoped CSIDriver / StorageClass / ClusterRole / ClusterRoleBinding /
  VolumeSnapshotClass and the namespaced controller Deployments / node DaemonSets /
  ServiceAccount in `kube-system` — now carries a controller owner reference back to
  the (cluster-scoped) `AlibabaCloudCSIDriver` CR. Deleting the CR
  (`oc delete alibabacloudcsidriver cluster`) now garbage-collects all of them
  instead of orphaning them. Because GC is a control-plane function, teardown works
  even if the operator Pod is already gone. Existing resources from an older
  operator version are **adopted** on the next reconcile (the owner reference is
  added in place), so upgraded clusters get clean teardown too. Fixes the leftover
  CSIDriver/StorageClass/RBAC/workload objects observed after uninstall on
  OpenShift 4.22 (CRC). Garbage collection is a control-plane function, so teardown
  works even if the operator Pod is already gone. The controller does **not** watch
  the created workloads (`Owns()`): the apply helpers write them unconditionally
  every reconcile, and an active `Owns()` watch would self-trigger a reconcile hot
  loop whose racing updates conflict — owner references drive GC without needing a
  watch. Apply helpers retry on update conflicts. Verified end-to-end on a real API
  server (kind): deleting the CR garbage-collects all owned objects; no reconcile
  loop (2 reconciles, 0 errors).

## [v0.1.10]
- **OperatorHub listing metadata** (bundle-only; operand image is byte-identical to
  v0.1.9, reusing the same digest — no rebuild):
  - raise the capability level from `Basic Install` to **`Seamless Upgrades`** — the
    operator ships a published, tested OLM upgrade graph;
  - add **Documentation** (QUICKSTART) and **Support / Report an Issue** (GitHub
    issues) entries to `spec.links` so the console's documentation/support fields
    resolve instead of showing N/A.
  - Update graph: `spec.replaces: alibaba-cloud-csi-operator.v0.1.9` (same OCP range
    `v4.21-v4.22`, so `check_replaces_availability` is satisfied); the v0.1.8 skip is
    preserved by v0.1.9's already-published CSV.

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
