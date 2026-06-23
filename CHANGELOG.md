# Changelog

All notable changes to this project are documented here. The format loosely
follows [Keep a Changelog](https://keepachangelog.com/); the project follows
semantic versioning within the `v0.1.x` line.

## [Unreleased]

- **Platform-adaptive privileged admission**: detect the OpenShift SCC API via the
  RESTMapper — bind the privileged SCC on OpenShift, or label the operator
  namespace for privileged Pod Security Admission on vanilla Kubernetes. Makes the
  operator installable off OpenShift (prerequisite for an operatorhub.io listing).
- **deps**: bump `golang.org/x/net` to v0.56.0 (HIGH/CRITICAL CVE fixes —
  HTTP/2, idna, html).
- **ci**: Trivy now scans the locally-built image tarball so the CVE gate runs on
  PRs too (previously failed with MANIFEST_UNKNOWN — the image is not pushed on PRs).

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
