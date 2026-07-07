# alibaba-cloud-csi-operator

OLM Operator that installs and manages [Alibaba Cloud CSI Driver](https://github.com/kubernetes-sigs/alibaba-cloud-csi-driver)
on OpenShift clusters running in **External Platform** mode.

> [!TIP]
> **New here? Run `make demo`.** It runs the operator against a throwaway local
> [kind](https://kind.sigs.k8s.io/) cluster and watches it reconcile a full CSI
> driver in ~5 minutes — **no Alibaba account, no storage spend**. Then read
> **[docs/QUICKSTART.md](docs/QUICKSTART.md)** for real-cluster (OLM) use and to
> figure out [which project you need](docs/QUICKSTART.md#3-which-project-do-i-need).

## Overview

When OpenShift runs in External Platform mode, no cloud-provider CSI driver is installed automatically.
This operator fills that gap — it deploys and reconciles all CSI components declaratively through a
single `AlibabaCloudCSIDriver` custom resource.

| Phase | Storage type | Status |
|-------|-------------|--------|
| P1    | Disk / EBS (cloud_efficiency, cloud_essd; Block + Filesystem) | ✅ Implemented |
| P1    | NAS file storage (ReadWriteMany, for VM live migration) | ✅ Implemented |
| —     | OSS object storage | Backup target only (via OADP), **not a mount driver** — see Scope |

## Positioning — driver vs. operator

This project is **not** a reimplementation of the CSI driver. The data-plane driver
(provision / attach / mount / expand / snapshot against Alibaba Cloud APIs) is the upstream
[`kubernetes-sigs/alibaba-cloud-csi-driver`](https://github.com/kubernetes-sigs/alibaba-cloud-csi-driver).
This repo is the **OpenShift-native lifecycle + integration layer** around that driver — the same
relationship as `aws-ebs-csi-driver-operator` ↔ `aws-ebs-csi-driver`.

| | Upstream driver | This operator |
|---|---|---|
| Layer | Data plane (CSI gRPC) | Control plane (OLM lifecycle) |
| Responsibility | Talk to ECS/NAS APIs, mount volumes | Install / reconcile / upgrade the driver on OpenShift |

Why it must exist: AWS / Azure / GCP / vSphere all ship a first-party CSI operator inside OpenShift,
but **Alibaba Cloud is not an OpenShift-supported platform**, so in External / `none` platform mode
nothing installs or manages the Alibaba CSI driver for you. This operator provides what the bare
upstream driver does not:

- **OLM packaging** (CatalogSource / bundle / CSV) and an independent release line.
- **OpenShift / RHCOS glue**: `seLinuxMount: true`, SCC `privileged` binding, HA controller pinned to
  control-plane nodes, IMDSv2 RAM-role auth (`ramToken: v2`, no AK/SK).
- **OpenShift Virtualization (OKV/KubeVirt) integration**: CDI does not recognise the Alibaba drivers,
  so the operator patches `StorageProfile` (claimPropertySets / cloneStrategy), exposes a per-class
  `volumeMode` (Block for VM OS disks, Filesystem for general PVCs), and a disk `VolumeSnapshotClass`.
- **Air-gap** image strategy and opinionated, ready-to-use StorageClasses.

## Scope — Alibaba Cloud storage service coverage

CSI is only meaningful for *volume*-type storage. This operator targets what general OpenShift and
OKV workloads actually need — block (EBS) and file (NAS):

| Alibaba service | Volume-type? | Upstream plugin | This operator | Rationale |
|---|---|---|---|---|
| **EBS / cloud disk (ESSD)** | Block | ✅ diskplugin | ✅ **core** | RWO; VM OS disk (Block) + general PVCs; CSI snapshots |
| **NAS file storage** | File | ✅ nasplugin | ✅ **core** | RWX; VM live migration + shared data |
| **OSS object storage** | Object (FUSE-mountable, but shouldn't) | ✅ ossplugin (ossfs) | **Backup only** | ossfs has poor random I/O and non-POSIX semantics → used as the OADP backup target (S3) / CDI import source, not a runtime volume |
| **Tablestore** | ❌ not a volume (NoSQL, SDK/API) | ❌ (impossible by nature) | ❌ N/A | Like DynamoDB — apps use the SDK; never a PV |
| **Apsara File Storage for HDFS** | HDFS protocol (not POSIX mount) | ❌ (no standard CSI) | ❌ out of scope | Big-data via Hadoop/HDFS client, not a POSIX volume |
| **DBFS (database file storage)** | Database-oriented shared block | ⚠️ specialised (dbfs) | ❌ not yet | Niche (self-managed DBs); not needed for general/OKV workloads |
| **CPFS (parallel file storage)** | Parallel FS | ✅ cpfs | ❌ not yet | HPC only; very high cost (P3) |

**Not done ≠ not considered.** OSS is deliberately positioned as backup/import, not a mount driver.
Tablestore and HDFS-version are not "volumes" at all (NoSQL API / HDFS protocol) and are outside CSI
by nature. DBFS and CPFS are volume-type and have upstream plugins, but serve niche database / HPC
scenarios outside the current target (general workloads + virtualization). The `AlibabaCloudCSIDriver`
CR is already structured per backend (`disk` / `nas` / `oss`), so adding e.g. `dbfs` later is a spec
addition, not an architecture change — there is simply no scenario demand for it now.

## Architecture

```
CatalogSource (OLM)
  └─ Subscription → InstallPlan
       └─ alibaba-cloud-csi-operator Pod
            └─ watches AlibabaCloudCSIDriver CR
                 ├─ CSIDriver object
                 ├─ ServiceAccount + ClusterRole + ClusterRoleBinding
                 ├─ SCC privileged binding (OpenShift)
                 ├─ CSI Controller Deployment (control-plane nodes)
                 ├─ CSI Node DaemonSet (all nodes, privileged)
                 └─ StorageClass objects
```

## Custom Resource

```yaml
apiVersion: csi.alibabacloud.com/v1alpha1
kind: AlibabaCloudCSIDriver
metadata:
  name: cluster          # singleton, always use this name
  namespace: kube-system
spec:
  disk:
    enabled: true
    defaultStorageClass: true
    storageClasses:
      - name: alicloud-disk-efficiency
        type: cloud_efficiency
        reclaimPolicy: Delete
        allowVolumeExpansion: true
      - name: alicloud-disk-essd
        type: cloud_essd
        reclaimPolicy: Delete
        allowVolumeExpansion: true
  nas:
    enabled: false
  oss:
    enabled: false
  imageTag: v1.35.3          # upstream kubernetes-sigs/alibaba-cloud-csi-driver tag
  auth:
    ramToken: v2             # ECS instance metadata IMDSv2, no AK/SK needed
  controller:
    replicas: 2
    nodeSelector:
      node-role.kubernetes.io/master: ""
    tolerations:
      - key: node-role.kubernetes.io/master
        effect: NoSchedule
```

## Authentication

The operator uses **RAM Role Instance Principal** — no AK/SK required:
- ECS nodes must have a RAM Role attached with disk-related permissions.
- The CSI driver fetches temporary tokens from the ECS instance metadata endpoint automatically.

Required RAM policy actions on the node RAM Role:
```
ecs:AttachDisk, ecs:DetachDisk, ecs:DescribeDisks, ecs:CreateDisk, ecs:DeleteDisk,
ecs:ResizeDisk, ecs:CreateSnapshot, ecs:DeleteSnapshot, ecs:DescribeSnapshots,
ecs:CreateAutoSnapshotPolicy, ecs:ApplyAutoSnapshotPolicy, ecs:DeleteAutoSnapshotPolicy,
ecs:DescribeAutoSnapshotPolicyEx, ecs:ModifyDiskAttribute
```

## OLM Install (via CatalogSource)

Apply these manifests at install-time (or via `extraManifests` in install-config):

```bash
# 1. CatalogSource — register the operator catalog
kubectl apply -f 04-csi-catalogsource.yaml

# 2. OperatorGroup — scope to kube-system
kubectl apply -f 04-csi-operatorgroup.yaml

# 3. Subscription — trigger OLM install
kubectl apply -f 04-csi-subscription.yaml

# 4. CR — configure the CSI driver (after operator Pod is Running)
kubectl apply -f 04-csi-driver-cr.yaml
```

## Build

### Prerequisites

- Go 1.24+
- operator-sdk v1.42.2
- Docker / Podman

### Three-layer image build

```bash
# 1. Operator image
make docker-build docker-push IMG=quay.io/samzhu/alibaba-cloud-csi-operator:v1.35.3

# 2. Bundle image (OLM metadata + CSV)
make bundle-build bundle-push \
  BUNDLE_IMG=quay.io/samzhu/alibaba-cloud-csi-operator-bundle:v1.35.3

# 3. Catalog image
make catalog-build catalog-push \
  CATALOG_IMG=quay.io/samzhu/alibaba-cloud-csi-operator-catalog:latest \
  BUNDLE_IMGS=quay.io/samzhu/alibaba-cloud-csi-operator-bundle:v1.35.3
```

### Local development

```bash
make generate manifests   # regenerate DeepCopy + CRDs
make fmt vet              # format and vet
go test ./...             # run unit tests (no envtest required)
```

## Testing

Four tiers, fastest first:

| Tier | Command | Needs | Covers |
|------|---------|-------|--------|
| **Unit** | `go test ./internal/...` | nothing (fake client) | reconcile helpers, claim-property-sets, conditions |
| **kind smoke** | `make test-kind-smoke` | kind + docker/podman + go | runs the operator out-of-cluster against a real API server, applies a CR, and asserts it creates the CSIDriver objects, StorageClasses (incl. the Block VM class), disk+NAS controller Deployments + node DaemonSets, RBAC, the disk VolumeSnapshotClass (and **no** NAS one), and `status` Available/Ready. **Hermetic — no Alibaba cloud.** Runs in CI. |
| **e2e** | `make test-e2e` | kind + image build | operator-sdk scaffold e2e (operator deploys in-cluster, metrics endpoint) |
| **live** | n/a (manual) | real OpenShift on Alibaba | actual PVC / NAS / snapshot provisioning against the cloud (tracked as #32-34) |

The kind smoke is the practical "does the operator actually work" gate: it exercises
the real reconcile loop end-to-end without any cloud credentials. The CSI driver Pods
themselves won't become Ready in kind (no cloud, no `/dev`) — that is the live tier's job.

```bash
make test-kind-smoke                 # create cluster, run, assert, tear down
KEEP_CLUSTER=1 ./hack/kind-smoke.sh  # leave it up to inspect (prints KUBECONFIG)
```

## Status

```
$ kubectl get alicsid -n kube-system
NAME      DISKREADY   AVAILABLE   AGE
cluster   true        True        5m
```

## License

Apache License 2.0
