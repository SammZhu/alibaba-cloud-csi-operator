# alibaba-cloud-csi-operator

OLM Operator that installs and manages [Alibaba Cloud CSI Driver](https://github.com/kubernetes-sigs/alibaba-cloud-csi-driver)
on OpenShift clusters running in **External Platform** mode.

## Overview

When OpenShift runs in External Platform mode, no cloud-provider CSI driver is installed automatically.
This operator fills that gap — it deploys and reconciles all CSI components declaratively through a
single `AlibabaCloudCSIDriver` custom resource.

| Phase | Storage type | Status |
|-------|-------------|--------|
| P1    | Disk (cloud_efficiency, cloud_essd) | ✅ Implemented |
| P2    | NAS file storage | 🔜 Planned |
| P3    | OSS object storage | 🔜 Planned |

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

## Status

```
$ kubectl get alicsid -n kube-system
NAME      DISKREADY   AVAILABLE   AGE
cluster   true        True        5m
```

## License

Apache License 2.0
