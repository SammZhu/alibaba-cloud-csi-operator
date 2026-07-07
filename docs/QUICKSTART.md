# Quick Start

Get hands-on with the Alibaba Cloud CSI operator in the order that wastes the
least of your time:

1. **[See it work in ~5 minutes](#1-see-it-work-in-5-minutes-no-cloud-account)** — one command, on your laptop, **no Alibaba account and no storage spend**.
2. **[Use it for real](#2-use-it-for-real)** — install it via OLM on an OpenShift cluster and provision real disk/NAS volumes.
3. **[Pick the right project](#3-which-project-do-i-need)** — this operator is one piece; the worker plane and the full OpenShift-on-Alibaba install live in sibling repos.

> **Unofficial, community-maintained.** Not affiliated with or endorsed by
> Alibaba Cloud, Red Hat, or OpenShift. Apache-2.0, provided as-is.

---

## 1. See it work in 5 minutes (no cloud account)

This spins up a throwaway [kind](https://kind.sigs.k8s.io/) cluster, runs **this
operator** out-of-cluster against it, applies an `AlibabaCloudCSIDriver` custom
resource, and asserts the operator reconciles **every** managed object plus the
CR status. **Hermetic: no Alibaba credentials, no volumes are provisioned.**

**Prerequisites** (all `brew install …` on macOS): `kind`, `kubectl`, `go`, and a
container runtime — Docker if present, otherwise Podman (bump the Podman machine
to ≥ 4 GiB).

```sh
make demo
```

You'll watch it print `✓` for each assertion and finish with
`✅ kind smoke PASSED`:

| Stage | What it proves |
|---|---|
| `[install]` | the operator CRD + external-snapshotter CRDs apply; the manager starts and its cache syncs against a real API server. |
| `[reconcile]` | applying the CR makes the operator create the `CSIDriver` objects, the StorageClasses (including the Block `volumeMode` VM class with the OKV virt-default annotation), the disk + NAS controller Deployments and node DaemonSets, the shared RBAC, and the disk `VolumeSnapshotClass` (and correctly **no** NAS one). |
| `[status]` | the CR reports `diskDriverReady` / `nasDriverReady` / `Available=True`. |

> The CSI **driver pods** themselves won't become Ready in kind (no cloud, no
> `/dev`) — the demo asserts the **operator** created and wired everything, which
> is the real "does the operator work" gate. Actual volume provisioning is the
> live tier (real OpenShift on Alibaba).

Want to poke at the cluster afterwards instead of tearing it down:

```sh
KEEP_CLUSTER=1 ./hack/kind-smoke.sh   # prints the KUBECONFIG + operator log path
# ... explore ...
kind delete cluster --name csi-op-smoke
```

This is the same run as `make test-kind-smoke` in CI — so a green `make demo` is
also your local signal that a build is healthy.

---

## 2. Use it for real

On a real OpenShift-on-Alibaba cluster the operator installs via **OLM** and the
driver authenticates through the node **RAM role** (IMDSv2, no AK/SK).

```bash
# 1. Install the operator via OLM (CatalogSource → OperatorGroup → Subscription):
kubectl apply -f 04-csi-catalogsource.yaml
kubectl apply -f 04-csi-operatorgroup.yaml
kubectl apply -f 04-csi-subscription.yaml

# 2. Once the operator Pod is Running, configure the driver with a CR:
kubectl apply -f 04-csi-driver-cr.yaml

# 3. Verify:
kubectl get alicsid -n kube-system   # DISKREADY=true AVAILABLE=True
```

- **RAM permissions** the node role needs (attach/detach/create/resize/snapshot):
  see the [README § Authentication](../README.md#authentication).
- **What the operator manages, why the operator exists, and storage-service
  scope** (disk/NAS core, OSS as backup-only): [README § Positioning](../README.md#positioning--driver-vs-operator)
  and [§ Scope](../README.md#scope--alibaba-cloud-storage-service-coverage).
- **Air-gap image strategy** and the three-layer build: [README § Build](../README.md#build).

The full end-to-end install (cluster + this operator wired in at `site-post`) is
driven by the `alibaba-openshift` ansible repo — see below.

---

## 3. Which project do I need?

This operator is the **storage layer**. Depending on your goal you may want a
sibling project instead — or in addition:

| Your goal | Use | Quick demo |
|---|---|---|
| Provision Alibaba **block (ESSD) / file (NAS) volumes** on an existing OpenShift cluster | **This repo** (`alibaba-cloud-csi-operator`) | `make demo` (above) |
| Add Alibaba ECS **worker machines** to a CAPI/OpenShift management cluster | [`openshift-capi-alicloud`](https://github.com/SammZhu/openshift-capi-alicloud) — the day-2 worker plane | its own `make demo` |
| Install a **whole OpenShift cluster on Alibaba Cloud** end-to-end (VPC/SLB, control plane, then workers + this operator for storage) | [`alibaba-openshift`](https://github.com/SammZhu/alibaba-openshift) — the ansible automation | See its [E2E-RUNBOOK](https://github.com/SammZhu/alibaba-openshift/blob/main/docs/E2E-RUNBOOK.md) |

Typical full-stack flow: `alibaba-openshift` installs the cluster and, as part of
its `site-post` step, deploys the **worker provider** and **this CSI operator**
onto it. If you're doing the whole thing, start at `alibaba-openshift`; if you
already have a cluster and just need storage, you're in the right place.
