#!/usr/bin/env bash
# kind smoke test for alibaba-cloud-csi-operator.
#
# Brings up a throwaway kind cluster, runs THIS operator out-of-cluster against
# it, applies an AlibabaCloudCSIDriver CR, and asserts the operator reconciles
# every managed object plus the CR status.
#
#   [install]    operator CRD + external-snapshotter CRDs apply; the manager
#                starts and its cache syncs against a real API server.
#   [reconcile]  applying the CR makes the operator create the CSIDriver objects,
#                StorageClasses (incl. the Block volumeMode VM class), the disk +
#                NAS controller Deployments and node DaemonSets, the shared RBAC,
#                and the disk VolumeSnapshotClass.
#   [status]     the CR reports DiskDriverReady / NASDriverReady / Available=True.
#
# HERMETIC: no Alibaba AK/SK, no real volumes are provisioned. The CSI driver
# pods themselves will NOT become Ready in kind (no cloud, no /dev) — this test
# asserts the OPERATOR created the objects, not that the driver works. That is
# the live-cluster job (#32-34).
#
# Requires: kind, kubectl, go. Container runtime: docker if present, else podman
# (KIND_EXPERIMENTAL_PROVIDER=podman; bump the podman machine to >=4GiB).
#
# Usage:  hack/kind-smoke.sh            # or: make test-kind-smoke
#   KEEP_CLUSTER=1 hack/kind-smoke.sh   # leave the cluster + operator up to debug
set -euo pipefail

CLUSTER="csi-op-smoke"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"
KUBECONFIG_FILE="$(mktemp -t csi-op-smoke-kubeconfig.XXXXXX)"
export KUBECONFIG="$KUBECONFIG_FILE"
OPERATOR_PID=""
OPERATOR_LOG="$(mktemp -t csi-op-smoke.XXXXXX.log)"
SNAP_VERSION="v8.2.0"

cleanup() {
  local rc=$?
  [[ -n "$OPERATOR_PID" ]] && kill "$OPERATOR_PID" >/dev/null 2>&1 || true
  if [[ "${KEEP_CLUSTER:-}" == "1" ]]; then
    echo
    echo "KEEP_CLUSTER=1 — leaving kind cluster '$CLUSTER' + operator (pid $OPERATOR_PID) up."
    echo "  export KUBECONFIG=$KUBECONFIG_FILE"
    echo "  operator log: $OPERATOR_LOG"
    echo "  delete with: kind delete cluster --name $CLUSTER"
  else
    kind delete cluster --name "$CLUSTER" >/dev/null 2>&1 || true
    rm -f "$KUBECONFIG_FILE" "$OPERATOR_LOG"
  fi
  exit $rc
}
trap cleanup EXIT

# ---- preflight -------------------------------------------------------------
for bin in kind kubectl go; do
  command -v "$bin" >/dev/null 2>&1 || { echo "ERROR: '$bin' not found in PATH" >&2; exit 1; }
done
if command -v docker >/dev/null 2>&1; then
  echo "container runtime: docker"
else
  export KIND_EXPERIMENTAL_PROVIDER=podman
  echo "container runtime: podman (KIND_EXPERIMENTAL_PROVIDER=podman)"
fi
echo "kind: $(kind version 2>/dev/null)   kubectl: $(kubectl version --client -o yaml 2>/dev/null | awk '/gitVersion/{print $2; exit}')"

# ---- kind cluster ----------------------------------------------------------
echo "==> creating kind cluster '$CLUSTER'"
kind create cluster --name "$CLUSTER" --kubeconfig "$KUBECONFIG_FILE" --wait 120s

# ---- CRDs ------------------------------------------------------------------
echo "==> installing operator CRD"
kubectl apply -f config/crd/bases/
kubectl wait --for=condition=Established crd/alibabacloudcsidrivers.csi.alibabacloud.com --timeout=60s

echo "==> installing external-snapshotter CRDs ($SNAP_VERSION) for the VolumeSnapshotClass path"
SNAP_OK=1
SNAP_BASE="https://raw.githubusercontent.com/kubernetes-csi/external-snapshotter/${SNAP_VERSION}/client/config/crd"
for c in volumesnapshotclasses volumesnapshotcontents volumesnapshots; do
  kubectl apply -f "${SNAP_BASE}/snapshot.storage.k8s.io_${c}.yaml" || SNAP_OK=0
done
[[ "$SNAP_OK" == "1" ]] && kubectl wait --for=condition=Established \
  crd/volumesnapshotclasses.snapshot.storage.k8s.io --timeout=60s || \
  echo "WARN: snapshot CRDs unavailable — operator will skip VolumeSnapshotClass (assertion relaxed)"

# ---- build + run the operator out-of-cluster -------------------------------
echo "==> building operator binary"
go build -o bin/manager ./cmd/main.go
echo "==> starting operator (out-of-cluster, against kind)"
./bin/manager --health-probe-bind-address=:18081 --metrics-bind-address=0 >"$OPERATOR_LOG" 2>&1 &
OPERATOR_PID=$!
sleep 3
kill -0 "$OPERATOR_PID" 2>/dev/null || { echo "ERROR: operator exited early"; cat "$OPERATOR_LOG"; exit 1; }

# ---- apply the CR ----------------------------------------------------------
echo "==> applying AlibabaCloudCSIDriver CR (disk[3 SCs incl Block]+nas+snapshot+storageProfile)"
kubectl apply -f - <<'CR'
apiVersion: csi.alibabacloud.com/v1alpha1
kind: AlibabaCloudCSIDriver
metadata:
  name: cluster
spec:
  disk:
    enabled: true
    defaultStorageClass: true
    storageClasses:
      - { name: alicloud-disk-essd,        type: cloud_essd,       volumeMode: Filesystem }
      - { name: alicloud-disk-essd-block,  type: cloud_essd,       volumeMode: Block, virtDefault: true }
      - { name: alicloud-disk-efficiency,  type: cloud_efficiency, volumeMode: Filesystem }
    snapshot:       { enabled: true, className: alibaba-cloud-disk-snapclass }
    storageProfile: { patch: true }     # CDI absent in kind → operator skips silently
  nas:
    enabled: true
    storageClasses:
      - { name: alicloud-nas-performance, storageType: Performance, mountProtocol: nfs }
    storageProfile: { patch: true }
  oss: { enabled: false }
  imageTag: v1.35.3
  auth: { ramToken: v2 }
CR

# ---- wait for reconcile ----------------------------------------------------
echo "==> waiting for status Available=True"
avail=""
for _ in $(seq 1 30); do
  avail="$(kubectl get alicsid cluster -o jsonpath='{.status.conditions[?(@.type=="Available")].status}' 2>/dev/null || true)"
  [[ "$avail" == "True" ]] && break
  sleep 2
done

# ---- assertions ------------------------------------------------------------
fail=0
ok()   { echo "  ✓ $*"; }
bad()  { echo "  ✗ $*"; fail=1; }
have() { kubectl get "$@" >/dev/null 2>&1; }

echo "[assert] CSIDriver objects"
for d in diskplugin.csi.alibabacloud.com nasplugin.csi.alibabacloud.com; do
  have csidriver "$d" && ok "CSIDriver $d" || bad "CSIDriver $d missing"
done

echo "[assert] StorageClasses"
for sc in alicloud-disk-essd alicloud-disk-essd-block alicloud-disk-efficiency alicloud-nas-performance; do
  have storageclass "$sc" && ok "StorageClass $sc" || bad "StorageClass $sc missing"
done
# Block VM class carries the OKV virt-default annotation.
vd="$(kubectl get storageclass alicloud-disk-essd-block -o jsonpath='{.metadata.annotations.storageclass\.kubevirt\.io/is-default-virt-class}' 2>/dev/null || true)"
[[ "$vd" == "true" ]] && ok "essd-block is virt-default" || bad "essd-block missing virt-default annotation"

echo "[assert] controller Deployments + node DaemonSets (kube-system)"
for dep in csi-disk-controller csi-nas-controller; do
  have -n kube-system deployment "$dep" && ok "Deployment $dep" || bad "Deployment $dep missing"
done
for ds in csi-disk-node csi-nas-node; do
  have -n kube-system daemonset "$ds" && ok "DaemonSet $ds" || bad "DaemonSet $ds missing"
done

echo "[assert] shared RBAC"
have -n kube-system serviceaccount alibaba-cloud-csi-sa && ok "ServiceAccount" || bad "ServiceAccount missing"
have clusterrole alibaba-cloud-csi-role && ok "ClusterRole" || bad "ClusterRole missing"

if [[ "$SNAP_OK" == "1" ]]; then
  echo "[assert] VolumeSnapshotClass (disk only — NAS has none by design)"
  have volumesnapshotclass alibaba-cloud-disk-snapclass && ok "VolumeSnapshotClass disk" || bad "VolumeSnapshotClass disk missing"
  have volumesnapshotclass alibaba-cloud-nas-snapclass 2>/dev/null && bad "unexpected NAS VolumeSnapshotClass" || ok "no NAS VolumeSnapshotClass (correct)"
fi

echo "[assert] CR status"
[[ "$avail" == "True" ]] && ok "Available=True" || bad "Available=$avail (expected True)"
dr="$(kubectl get alicsid cluster -o jsonpath='{.status.diskDriverReady}' 2>/dev/null || true)"
nr="$(kubectl get alicsid cluster -o jsonpath='{.status.nasDriverReady}' 2>/dev/null || true)"
[[ "$dr" == "true" ]] && ok "diskDriverReady=true" || bad "diskDriverReady=$dr"
[[ "$nr" == "true" ]] && ok "nasDriverReady=true" || bad "nasDriverReady=$nr"

echo
if [[ "$fail" == "0" ]]; then
  echo "✅ kind smoke PASSED — operator reconciles all objects + status against a real API server"
else
  echo "❌ kind smoke FAILED — see assertions above"
  echo "--- operator log (tail) ---"; tail -30 "$OPERATOR_LOG"
fi
exit "$fail"
