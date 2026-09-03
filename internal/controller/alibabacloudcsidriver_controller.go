/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	csiv1alpha1 "github.com/SammZhu/alibaba-cloud-csi-operator/api/v1alpha1"
)

const (
	diskDriverName     = "diskplugin.csi.alibabacloud.com"
	nasDriverName      = "nasplugin.csi.alibabacloud.com"
	ossDriverName      = "ossplugin.csi.alibabacloud.com"
	serviceAccountName = "alibaba-cloud-csi-sa"
	operatorNamespace  = "kube-system"
	csiImage           = "registry.cn-hangzhou.aliyuncs.com/acs/csi-plugin"

	// External CSI sidecars default to the canonical, publicly-resolvable upstream
	// registry.k8s.io/sig-storage images, **digest-pinned** (disconnected best
	// practice + works on vanilla Kubernetes / operatorhub.io out of the box). The
	// Alibaba ACR (acs/) only carries some of these under bare tags, so referencing
	// acs/ directly breaks a direct (non-mirror) pull. Each can be overridden at
	// deploy time via a RELATED_IMAGE_* env var (see imageFor) — the air-gap path
	// points them at the mirror that way without changing these defaults.
	// Digests resolved 2026-06-23 (registry.k8s.io/sig-storage):
	defaultProvisionerImage = "registry.k8s.io/sig-storage/csi-provisioner@sha256:d078dc174323407e8cc6f0f9abd4efaac5db27838f1564d0253d5e3233e3f17f"           // v3.5.0
	defaultAttacherImage    = "registry.k8s.io/sig-storage/csi-attacher@sha256:4eb73137b66381b7b5dfd4d21d460f4b4095347ab6ed4626e0199c29d8d021af"              // v4.3.0
	defaultResizerImage     = "registry.k8s.io/sig-storage/csi-resizer@sha256:2e2b44393539d744a55b9370b346e8ebd95a77573064f3f9a8caf18c22f4d0d0"               // v1.8.0
	defaultSnapshotterImage = "registry.k8s.io/sig-storage/csi-snapshotter@sha256:93fbf54dd67f586428ea339743160e1a5110c058afde3c2e824f11fbc6efae53"           // v6.3.0
	defaultRegistrarImage   = "registry.k8s.io/sig-storage/csi-node-driver-registrar@sha256:f6717ce72a2615c7fbc746b4068f788e78579c54c43b8716e5ce650d97af2df1" // v2.8.0
	defaultLivenessImage    = "registry.k8s.io/sig-storage/livenessprobe@sha256:4dc0b87ccd69f9865b89234d8555d3a614ab0a16ed94a3016ffd27f8106132ce"             // v2.10.0

	envProvisioner = "RELATED_IMAGE_CSI_PROVISIONER"
	envAttacher    = "RELATED_IMAGE_CSI_ATTACHER"
	envResizer     = "RELATED_IMAGE_CSI_RESIZER"
	envSnapshotter = "RELATED_IMAGE_CSI_SNAPSHOTTER"
	envRegistrar   = "RELATED_IMAGE_CSI_NODE_DRIVER_REGISTRAR"
	envLiveness    = "RELATED_IMAGE_LIVENESSPROBE"

	fieldManager = "alibaba-cloud-csi-operator"

	defaultDiskSnapClassName = "alibaba-cloud-disk-snapclass"
	// NOTE: there is intentionally no NAS snapshot class. The upstream NAS CSI
	// controller (nasplugin.csi.alibabacloud.com) does NOT implement
	// CreateSnapshot/DeleteSnapshot — its ControllerGetCapabilities advertises
	// only CREATE_DELETE_VOLUME and EXPAND_VOLUME. A VolumeSnapshotClass for NAS
	// would therefore be a footgun (every VolumeSnapshot would fail at the driver).
	// NAS-backed volumes are protected via OADP file-system backup (kopia) to OSS.
)

// imageFor returns the RELATED_IMAGE_* env override if set, else the default.
// This is the OLM convention for operand images: the CSV sets RELATED_IMAGE_*
// env on the operator Deployment (and lists them in spec.relatedImages), and a
// disconnected/air-gap deploy points them at a mirror without code changes.
func imageFor(env, def string) string {
	if v := os.Getenv(env); v != "" {
		return v
	}
	return def
}

// Resolved sidecar images (env override → default). Read once at init; the
// RELATED_IMAGE_* env on the operator pod is fixed for its lifetime.
var (
	csiProvisionerImage = imageFor(envProvisioner, defaultProvisionerImage)
	csiAttacherImage    = imageFor(envAttacher, defaultAttacherImage)
	csiResizerImage     = imageFor(envResizer, defaultResizerImage)
	csiSnapshotterImage = imageFor(envSnapshotter, defaultSnapshotterImage)
	nodeRegistrarImage  = imageFor(envRegistrar, defaultRegistrarImage)
	livenessProbeImage  = imageFor(envLiveness, defaultLivenessImage)
)

// AlibabaCloudCSIDriverReconciler reconciles a AlibabaCloudCSIDriver object
type AlibabaCloudCSIDriverReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// Config is the manager's rest.Config. sccAvailable() uses it to build a
	// fresh discovery client when the cached RESTMapper misses the SCC API, so a
	// stale/incomplete cached discovery (e.g. after an operator restart under node
	// DiskPressure) cannot produce a false PSA fallback on OpenShift. Nil in unit
	// tests, which drive detection purely through a fake RESTMapper.
	Config *rest.Config
}

// +kubebuilder:rbac:groups=csi.alibabacloud.com,resources=alibabacloudcsidrivers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=csi.alibabacloud.com,resources=alibabacloudcsidrivers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=csi.alibabacloud.com,resources=alibabacloudcsidrivers/finalizers,verbs=update
// +kubebuilder:rbac:groups=storage.k8s.io,resources=csidrivers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=storage.k8s.io,resources=storageclasses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=deployments;daemonsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterroles;clusterrolebindings,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=snapshot.storage.k8s.io,resources=volumesnapshotclasses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cdi.kubevirt.io,resources=storageprofiles,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=security.openshift.io,resources=securitycontextconstraints,verbs=use,resourceNames={privileged}
// The operator creates the driver ClusterRole "alibaba-cloud-csi-role" (see
// reconcileRBAC). Kubernetes forbids an account from granting RBAC verbs it does
// not itself hold (privilege-escalation prevention), so the operator's own role
// must be a SUPERSET of every PolicyRule it grants to the driver. The markers
// below mirror that driver role; keep them in sync with it.
// +kubebuilder:rbac:groups="",resources=nodes;namespaces;pods;persistentvolumes;persistentvolumeclaims,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=persistentvolumes,verbs=create;delete
// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=storage.k8s.io,resources=csinodes;volumeattachments,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=storage.k8s.io,resources=volumeattachments/status,verbs=patch
// +kubebuilder:rbac:groups=snapshot.storage.k8s.io,resources=volumesnapshots;volumesnapshotcontents,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=snapshot.storage.k8s.io,resources=volumesnapshotcontents/status;volumesnapshots/status,verbs=get;update;patch

func (r *AlibabaCloudCSIDriverReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var cr csiv1alpha1.AlibabaCloudCSIDriver
	if err := r.Get(ctx, req.NamespacedName, &cr); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	log.Info("Reconciling AlibabaCloudCSIDriver", "name", cr.Name)

	var reconcileErr error

	// 1. Ensure shared RBAC (ServiceAccount, ClusterRole, ClusterRoleBindings).
	if err := r.ensureRBAC(ctx, &cr); err != nil {
		reconcileErr = fmt.Errorf("RBAC: %w", err)
	}

	// 2. Disk driver components.
	diskReady := false
	if reconcileErr == nil && cr.Spec.Disk.Enabled {
		if err := r.ensureDiskDriver(ctx, &cr); err != nil {
			reconcileErr = fmt.Errorf("disk driver: %w", err)
		} else {
			diskReady = true
		}
	}

	// 3. NAS driver components (Phase 2).
	nasReady := false
	if reconcileErr == nil && cr.Spec.NAS.Enabled {
		if err := r.ensureNASDriver(ctx, &cr); err != nil {
			reconcileErr = fmt.Errorf("nas driver: %w", err)
		} else {
			nasReady = true
		}
	}

	// 4. OSS driver (Phase 3 — not yet implemented).
	ossReady := false
	if reconcileErr == nil && cr.Spec.OSS.Enabled {
		log.Info("OSS CSI driver requested but not yet implemented — skipping")
	}

	// 5. Update status.
	allReady := (!cr.Spec.Disk.Enabled || diskReady) && (!cr.Spec.NAS.Enabled || nasReady)
	patch := client.MergeFrom(cr.DeepCopy())
	cr.Status.ObservedGeneration = cr.Generation
	cr.Status.DiskDriverReady = diskReady
	cr.Status.NASDriverReady = nasReady
	cr.Status.OSSDriverReady = ossReady
	cr.Status.Conditions = buildConditions(allReady, reconcileErr)
	if err := r.Status().Patch(ctx, &cr, patch); err != nil {
		log.Error(err, "failed to update status")
	}

	return ctrl.Result{}, reconcileErr
}

// SetupWithManager sets up the controller with the Manager.
//
// We intentionally do NOT Own() the created workloads. The apply helpers write
// them unconditionally every reconcile; with an Owns() watch active (now that the
// objects carry owner references) that self-triggers a reconcile hot loop and the
// racing updates conflict. Owner references still drive garbage collection on CR
// deletion — that does not need a watch. Reconciles are driven by the CR itself;
// resource self-healing on drift would require idempotent (diff-aware) applies.
func (r *AlibabaCloudCSIDriverReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&csiv1alpha1.AlibabaCloudCSIDriver{}).
		Named("alibabacloudcsidriver").
		Complete(r)
}

// ── RBAC ────────────────────────────────────────────────────────────────────────

func (r *AlibabaCloudCSIDriverReconciler) ensureRBAC(ctx context.Context, cr *csiv1alpha1.AlibabaCloudCSIDriver) error {
	if err := r.ensureServiceAccount(ctx, cr); err != nil {
		return err
	}
	if err := r.ensureClusterRole(ctx, cr); err != nil {
		return err
	}
	if err := r.ensureClusterRoleBinding(ctx, cr); err != nil {
		return err
	}
	// Privileged-pod admission for the node DaemonSet is platform-specific:
	// OpenShift uses SecurityContextConstraints; vanilla Kubernetes uses Pod
	// Security Admission. Detect which API the cluster serves and adapt.
	if r.sccAvailable() {
		logf.FromContext(ctx).Info("admission backend: OpenShift SCC (privileged)")
		return r.ensureSCCBinding(ctx, cr)
	}
	logf.FromContext(ctx).Info("admission backend: Pod Security Admission (no SCC API found)")
	return r.ensurePrivilegedNamespacePSA(ctx)
}

func (r *AlibabaCloudCSIDriverReconciler) ensureServiceAccount(ctx context.Context, cr *csiv1alpha1.AlibabaCloudCSIDriver) error {
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceAccountName,
			Namespace: operatorNamespace,
		},
	}
	return r.createOrAdopt(ctx, cr, sa)
}

func (r *AlibabaCloudCSIDriverReconciler) ensureClusterRole(ctx context.Context, cr *csiv1alpha1.AlibabaCloudCSIDriver) error {
	role := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{Name: "alibaba-cloud-csi-role"},
		Rules: []rbacv1.PolicyRule{
			{APIGroups: []string{""}, Resources: []string{"nodes", "namespaces", "pods", "persistentvolumes", "persistentvolumeclaims"}, Verbs: []string{"get", "list", "watch", "update", "patch"}},
			// The node csi-plugin reads the optional kube-system/csi-plugin configmap at
			// startup. Without get it logs `configmaps "csi-plugin" is forbidden` — the
			// fast RBAC denial returns immediately, racing the plugin past credential
			// warmup into an unsigned ECS DescribeDisks (observed 2026-06-14 on zone-c).
			{APIGroups: []string{""}, Resources: []string{"configmaps"}, Verbs: []string{"get", "list", "watch"}},
			// external-provisioner creates/deletes the PV object after CreateVolume —
			// without create/delete the disk is created but "Saving volume" is
			// forbidden and the PVC never binds (observed 2026-06-14 in 13-csi-smoke).
			{APIGroups: []string{""}, Resources: []string{"persistentvolumes"}, Verbs: []string{"create", "delete"}},
			{APIGroups: []string{""}, Resources: []string{"events"}, Verbs: []string{"get", "list", "watch", "create", "update", "patch"}},
			{APIGroups: []string{"storage.k8s.io"}, Resources: []string{"storageclasses", "csinodes", "csidrivers", "volumeattachments"}, Verbs: []string{"get", "list", "watch", "update", "patch"}},
			{APIGroups: []string{"storage.k8s.io"}, Resources: []string{"volumeattachments/status"}, Verbs: []string{"patch"}},
			{APIGroups: []string{"snapshot.storage.k8s.io"}, Resources: []string{"volumesnapshots", "volumesnapshotcontents", "volumesnapshotclasses"}, Verbs: []string{"get", "list", "watch", "create", "update", "patch", "delete"}},
			// The csi-snapshotter sidecar updates VolumeSnapshotContent.status
			// (readyToUse / snapshotHandle) after the driver's CreateSnapshot. Without
			// this subresource the content status is never written, the VolumeSnapshot
			// stays readyToUse=false forever and the restore never happens (observed:
			// 13-csi-smoke [snapshot] FAIL, 2026-06-14).
			{APIGroups: []string{"snapshot.storage.k8s.io"}, Resources: []string{"volumesnapshotcontents/status", "volumesnapshots/status"}, Verbs: []string{"get", "update", "patch"}},
			// Leader election: the provisioner/attacher/resizer/snapshotter run with
			// --leader-election and hold a Lease. Without this the sidecars cannot
			// acquire leadership ("cannot get resource leases"), so e.g. the attacher
			// never attaches the disk and the Pod is stuck ContainerCreating
			// (observed 2026-06-14 in 13-csi-smoke).
			{APIGroups: []string{"coordination.k8s.io"}, Resources: []string{"leases"}, Verbs: []string{"get", "list", "watch", "create", "update", "patch", "delete"}},
		},
	}
	return r.createOrAdopt(ctx, cr, role)
}

func (r *AlibabaCloudCSIDriverReconciler) ensureClusterRoleBinding(ctx context.Context, cr *csiv1alpha1.AlibabaCloudCSIDriver) error {
	crb := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "alibaba-cloud-csi-binding"},
		RoleRef:    rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "ClusterRole", Name: "alibaba-cloud-csi-role"},
		Subjects:   []rbacv1.Subject{{Kind: "ServiceAccount", Name: serviceAccountName, Namespace: operatorNamespace}},
	}
	return r.createOrAdopt(ctx, cr, crb)
}

// ensureSCCBinding grants the CSI ServiceAccount the privileged SCC required
// for the Node DaemonSet to mount /dev and perform disk operations on RHCOS.
func (r *AlibabaCloudCSIDriverReconciler) ensureSCCBinding(ctx context.Context, cr *csiv1alpha1.AlibabaCloudCSIDriver) error {
	crb := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "alibabacloud-csi-privileged"},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     "system:openshift:scc:privileged",
		},
		Subjects: []rbacv1.Subject{{Kind: "ServiceAccount", Name: serviceAccountName, Namespace: operatorNamespace}},
	}
	return r.createOrAdopt(ctx, cr, crb)
}

// sccAvailable reports whether the cluster serves the OpenShift
// SecurityContextConstraints API (i.e. we are on OpenShift). On OpenShift the
// privileged node DaemonSet is admitted via the privileged SCC; elsewhere we
// fall back to Pod Security Admission (a privileged namespace label).
//
// A single cached-RESTMapper miss is NOT trusted as "not OpenShift". The
// manager's RESTMapper is a lazy dynamic mapper that discovers on demand and
// caches; if its first discovery ran while the apiserver was degraded (observed
// 2026-07-15: operator restarted under node DiskPressure), it caches the SCC
// miss and never reliably reloads — so detection stays false for the pod's
// lifetime, the privileged SCC binding is never created, and the node DaemonSet
// can be denied admission → mount failures. To avoid that false negative we
// confirm a mapper miss against a FRESH discovery client before concluding the
// SCC API is absent. Only a fresh discovery that also finds no SCC (a genuine
// non-OpenShift cluster) falls through to PSA.
func (r *AlibabaCloudCSIDriverReconciler) sccAvailable() bool {
	// Fast path: the cached mapper already knows the SCC API. A positive result
	// is authoritative (discovery only ever adds resources, never falsely invents
	// one), so trust it and skip the discovery round-trip.
	gk := schema.GroupKind{Group: "security.openshift.io", Kind: "SecurityContextConstraints"}
	if _, err := r.RESTMapper().RESTMapping(gk); err == nil {
		return true
	}

	// Cached mapper missed. This is either a genuine non-OpenShift cluster or a
	// stale/incomplete cached discovery. Without a rest.Config (unit tests) we
	// can only honour the mapper result.
	if r.Config == nil {
		return false
	}

	// Re-check against a fresh discovery client, bypassing the cached mapper.
	dc, err := discovery.NewDiscoveryClientForConfig(r.Config)
	if err != nil {
		return false
	}
	resList, err := dc.ServerResourcesForGroupVersion("security.openshift.io/v1")
	if err != nil {
		// A genuine non-OpenShift cluster returns a not-found error for the group;
		// any other transient error also safely leaves us on the PSA fallback.
		return false
	}
	for _, res := range resList.APIResources {
		if res.Kind == "SecurityContextConstraints" {
			return true
		}
	}
	return false
}

// ensurePrivilegedNamespacePSA labels the operator namespace so Pod Security
// Admission admits the privileged CSI node DaemonSet on non-OpenShift clusters
// (where there is no SCC). Idempotent; only writes when a label is missing/wrong.
func (r *AlibabaCloudCSIDriverReconciler) ensurePrivilegedNamespacePSA(ctx context.Context) error {
	ns := &corev1.Namespace{}
	if err := r.Get(ctx, types.NamespacedName{Name: operatorNamespace}, ns); err != nil {
		if apierrors.IsNotFound(err) {
			// kube-system is a cluster invariant; if it is somehow absent there is
			// nothing to label (the CSI pods deploy into it and would fail anyway).
			return nil
		}
		return fmt.Errorf("get namespace %s: %w", operatorNamespace, err)
	}
	want := map[string]string{
		"pod-security.kubernetes.io/enforce": "privileged",
		"pod-security.kubernetes.io/audit":   "privileged",
		"pod-security.kubernetes.io/warn":    "privileged",
	}
	if ns.Labels == nil {
		ns.Labels = map[string]string{}
	}
	changed := false
	for k, v := range want {
		if ns.Labels[k] != v {
			ns.Labels[k] = v
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return r.Update(ctx, ns)
}

// ── Disk Driver ──────────────────────────────────────────────────────────────────

func (r *AlibabaCloudCSIDriverReconciler) ensureDiskDriver(ctx context.Context, cr *csiv1alpha1.AlibabaCloudCSIDriver) error {
	imageTag := cr.Spec.ImageTag
	if imageTag == "" {
		imageTag = "v1.35.3"
	}
	pluginImage := fmt.Sprintf("%s:%s", csiImage, imageTag)

	if err := r.ensureDiskCSIDriver(ctx, cr); err != nil {
		return fmt.Errorf("CSIDriver: %w", err)
	}
	if err := r.ensureDiskControllerDeployment(ctx, cr, pluginImage); err != nil {
		return fmt.Errorf("controller Deployment: %w", err)
	}
	if err := r.ensureDiskNodeDaemonSet(ctx, cr, pluginImage); err != nil {
		return fmt.Errorf("node DaemonSet: %w", err)
	}

	// VolumeSnapshotClass — must be created before StorageProfile so we can
	// link spec.snapshotClass when patching.
	if err := r.ensureVolumeSnapshotClass(ctx, cr, diskDriverName, cr.Spec.Disk.Snapshot); err != nil {
		return fmt.Errorf("VolumeSnapshotClass: %w", err)
	}

	// Resolve the VolumeSnapshotClass name that StorageProfile should reference.
	snapClassName := cr.Spec.Disk.Snapshot.ClassName
	if cr.Spec.Disk.Snapshot.Enabled && snapClassName == "" {
		snapClassName = defaultDiskSnapClassName
	}

	for i, sc := range cr.Spec.Disk.StorageClasses {
		isDefault := cr.Spec.Disk.DefaultStorageClass && i == 0
		if err := r.ensureDiskStorageClass(ctx, cr, sc, isDefault); err != nil {
			return fmt.Errorf("StorageClass %s: %w", sc.Name, err)
		}
		// Patch CDI StorageProfile for OKV compatibility. The volumeMode is taken
		// per-StorageClass: a VM OS-disk class uses Block, a general class uses
		// Filesystem. Both are ReadWriteOnce (disk attaches to a single node).
		claimSets := diskClaimPropertySets(sc.VolumeMode)
		if err := r.ensureStorageProfile(ctx, sc.Name, cr.Spec.Disk.StorageProfile, claimSets, snapClassName); err != nil {
			return fmt.Errorf("StorageProfile %s: %w", sc.Name, err)
		}
	}

	return nil
}

// ensureDiskCSIDriver creates the CSIDriver object for the disk plugin.
// seLinuxMount: true is required on RHCOS (SELinux enforcing) so that the kubelet
// applies the correct SELinux label to the mount point before handing it to the driver.
// This matches the behaviour of the AWS EBS CSI driver on ROSA.
func (r *AlibabaCloudCSIDriverReconciler) ensureDiskCSIDriver(ctx context.Context, cr *csiv1alpha1.AlibabaCloudCSIDriver) error {
	attachRequired := true
	podInfoOnMount := true
	seLinuxMount := true
	fsGroupPolicy := storagev1.FileFSGroupPolicy

	obj := &storagev1.CSIDriver{
		ObjectMeta: metav1.ObjectMeta{Name: diskDriverName},
		Spec: storagev1.CSIDriverSpec{
			AttachRequired: &attachRequired,
			PodInfoOnMount: &podInfoOnMount,
			FSGroupPolicy:  &fsGroupPolicy,
			SELinuxMount:   &seLinuxMount,
		},
	}
	return r.createOrAdopt(ctx, cr, obj)
}

func (r *AlibabaCloudCSIDriverReconciler) ensureDiskControllerDeployment(ctx context.Context, cr *csiv1alpha1.AlibabaCloudCSIDriver, pluginImage string) error {
	replicas := cr.Spec.Controller.Replicas
	if replicas == 0 {
		replicas = 2
	}

	nodeSelector := cr.Spec.Controller.NodeSelector
	if nodeSelector == nil {
		nodeSelector = map[string]string{"node-role.kubernetes.io/master": ""}
	}

	tolerations := toCoreTolerations(cr.Spec.Controller.Tolerations)
	if len(tolerations) == 0 {
		tolerations = []corev1.Toleration{
			{Key: "node-role.kubernetes.io/master", Effect: corev1.TaintEffectNoSchedule},
		}
	}

	ramTokenVersion := cr.Spec.Auth.RAMToken
	if ramTokenVersion == "" {
		ramTokenVersion = "v2"
	}

	labels := map[string]string{"app": "csi-disk-controller"}

	// Controller socket lives in an in-pod emptyDir (socket-dir, mounted at /csi),
	// NOT the kubelet plugins hostPath — the controller does not register with
	// kubelet, and the hostPath dir does not exist in the controller pod (binding
	// the old path failed "no such file or directory"). The csi-plugin runs the
	// controller service only (--run-node-service=false); the sidecars connect to
	// the same /csi/csi.sock.
	socketMount := corev1.VolumeMount{Name: "socket-dir", MountPath: "/csi"}
	containers := []corev1.Container{
		{
			Name:  "csi-plugin",
			Image: pluginImage,
			Args:  []string{"--endpoint=$(CSI_ENDPOINT)", "--v=5", "--driver=diskplugin.csi.alibabacloud.com", "--run-node-service=false"},
			Env: append([]corev1.EnvVar{
				{Name: "CSI_ENDPOINT", Value: "unix:///csi/csi.sock"},
				{Name: "MAX_VOLUMES_PERNODE", Value: "15"},
				{Name: "RAM_ROLE_TOKEN", Value: ramTokenVersion},
			}, driverEnv(cr)...),
			VolumeMounts: []corev1.VolumeMount{socketMount},
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("100m"),
					corev1.ResourceMemory: resource.MustParse("128Mi"),
				},
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("500m"),
					corev1.ResourceMemory: resource.MustParse("512Mi"),
				},
			},
		},
		{
			Name:  "csi-provisioner",
			Image: csiProvisionerImage,
			// --leader-election: the controller runs 2 replicas, so without it BOTH
			// provisioners service the same PVC and race on CreateDisk — Alibaba then
			// rejects the duplicate with IdempotentProcessing and the PVC never binds.
			Args:         []string{"--csi-address=$(ADDRESS)", "--v=5", "--feature-gates=Topology=True", "--extra-create-metadata=true", "--leader-election=true"},
			Env:          []corev1.EnvVar{{Name: "ADDRESS", Value: "/csi/csi.sock"}},
			VolumeMounts: []corev1.VolumeMount{socketMount},
		},
		{
			Name:         "csi-attacher",
			Image:        csiAttacherImage,
			Args:         []string{"--v=5", "--csi-address=$(ADDRESS)", "--leader-election=true"},
			Env:          []corev1.EnvVar{{Name: "ADDRESS", Value: "/csi/csi.sock"}},
			VolumeMounts: []corev1.VolumeMount{socketMount},
		},
		{
			Name:         "csi-resizer",
			Image:        csiResizerImage,
			Args:         []string{"--v=5", "--csi-address=$(ADDRESS)", "--leader-election=true"},
			Env:          []corev1.EnvVar{{Name: "ADDRESS", Value: "/csi/csi.sock"}},
			VolumeMounts: []corev1.VolumeMount{socketMount},
		},
		{
			Name:         "csi-snapshotter",
			Image:        csiSnapshotterImage,
			Args:         []string{"--v=5", "--csi-address=$(ADDRESS)", "--leader-election=true"},
			Env:          []corev1.EnvVar{{Name: "ADDRESS", Value: "/csi/csi.sock"}},
			VolumeMounts: []corev1.VolumeMount{socketMount},
		},
	}

	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "csi-disk-controller",
			Namespace: operatorNamespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					ServiceAccountName: serviceAccountName,
					NodeSelector:       nodeSelector,
					Tolerations:        tolerations,
					Affinity:           controllerPodAntiAffinity(labels["app"]),
					Containers:         containers,
					Volumes: []corev1.Volume{
						{Name: "socket-dir", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
					},
				},
			},
		},
	}

	return r.createOrUpdate(ctx, cr, deploy)
}

func (r *AlibabaCloudCSIDriverReconciler) ensureDiskNodeDaemonSet(ctx context.Context, cr *csiv1alpha1.AlibabaCloudCSIDriver, pluginImage string) error {
	ramTokenVersion := cr.Spec.Auth.RAMToken
	if ramTokenVersion == "" {
		ramTokenVersion = "v2"
	}

	hostPathDir := corev1.HostPathDirectory
	hostPathDirOrCreate := corev1.HostPathDirectoryOrCreate
	privileged := true
	labels := map[string]string{"app": "csi-disk-node"}

	ds := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "csi-disk-node",
			Namespace: operatorNamespace,
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					ServiceAccountName: serviceAccountName,
					HostNetwork:        true,
					HostPID:            true,
					Tolerations:        []corev1.Toleration{{Operator: corev1.TolerationOpExists}},
					Containers: []corev1.Container{
						{
							Name:  "csi-plugin",
							Image: pluginImage,
							// Node-service only (the controller Deployment runs the controller
							// service). SERVICE_PORT is distinct per driver because both node
							// DaemonSets run with hostNetwork: without it disk-node and nas-node
							// both bind the same host port (default 11270) and one CrashLoops
							// "address already in use".
							Args: []string{"--endpoint=$(CSI_ENDPOINT)", "--v=5", "--driver=diskplugin.csi.alibabacloud.com", "--run-controller-service=false"},
							SecurityContext: &corev1.SecurityContext{
								Privileged: &privileged,
							},
							Env: append([]corev1.EnvVar{
								{Name: "CSI_ENDPOINT", Value: "unix:///var/lib/kubelet/plugins/diskplugin.csi.alibabacloud.com/csi.sock"},
								{Name: "RAM_ROLE_TOKEN", Value: ramTokenVersion},
								{Name: "SERVICE_PORT", Value: "11260"},
								// NodeGetInfo makes TWO ECS calls during plugin registration, each
								// using the global ECS client built once at startup. When the
								// ecs_ram_role provider hasn't warmed at that moment the client signs
								// nothing → "IllegalTimestamp: Timestamp not supplied" → NodeGetInfo
								// fails → node-driver-registrar never registers → CrashLoopBackOff
								// (reproduced persistently on cn-wulanchabu-c, all infra factors ruled
								// out live 2026-06-14). Both calls are independently gated, so set
								// both envs to make NodeGetInfo issue ZERO ECS calls — registration
								// then never depends on cloud credentials:
								//   MAX_VOLUMES_PERNODE  → skips getVolumeCountFromOpenAPI (DescribeDisks);
								//                          15 matches the controller's value.
								//   DISK_ALLOW_ALL_TYPE  → skips GetAvailableDiskTypes
								//                          (DescribeAvailableResource); the node then
								//                          advertises all disk types instead of
								//                          filtering by per-zone availability.
								{Name: "MAX_VOLUMES_PERNODE", Value: "15"},
								{Name: "DISK_ALLOW_ALL_TYPE", Value: "true"},
								{Name: "KUBE_NODE_NAME", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "spec.nodeName"}}},
							}, driverEnv(cr)...),
							VolumeMounts: []corev1.VolumeMount{
								{Name: "kubelet-dir", MountPath: "/var/lib/kubelet", MountPropagation: mountPropagation(corev1.MountPropagationBidirectional)},
								{Name: "plugin-dir", MountPath: "/var/lib/kubelet/plugins/diskplugin.csi.alibabacloud.com"},
								{Name: "registration-dir", MountPath: "/registration"},
								{Name: "dev", MountPath: "/dev"},
								{Name: "sys", MountPath: "/sys"},
							},
							// Restart the plugin (rebuilding its ECS client with warm
							// credentials) until kubelet registration succeeds. 9810 = disk
							// registrar --http-endpoint.
							StartupProbe: registrarHealthzStartupProbe(9810),
						},
						{
							Name:  "node-driver-registrar",
							Image: nodeRegistrarImage,
							// --http-endpoint exposes /healthz reporting kubelet-registration
							// status; the csi-plugin container's startupProbe watches it (port
							// 9810, distinct from nas 9811 — both DaemonSets are hostNetwork) so
							// the PLUGIN restarts until registration succeeds. See
							// registrarHealthzStartupProbe.
							Args: []string{
								"--v=5",
								"--csi-address=/var/lib/kubelet/plugins/diskplugin.csi.alibabacloud.com/csi.sock",
								"--kubelet-registration-path=/var/lib/kubelet/plugins/diskplugin.csi.alibabacloud.com/csi.sock",
								"--http-endpoint=:9810",
							},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "plugin-dir", MountPath: "/var/lib/kubelet/plugins/diskplugin.csi.alibabacloud.com"},
								{Name: "registration-dir", MountPath: "/registration"},
							},
						},
						{
							Name:  "liveness-probe",
							Image: livenessProbeImage,
							// --health-port distinct per driver: both node DaemonSets run with
							// hostNetwork, so disk-node and nas-node liveness-probes would both
							// bind the default :9808 on the host and one CrashLoops.
							Args: []string{"--csi-address=/var/lib/kubelet/plugins/diskplugin.csi.alibabacloud.com/csi.sock", "--health-port=9808"},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "plugin-dir", MountPath: "/var/lib/kubelet/plugins/diskplugin.csi.alibabacloud.com"},
							},
						},
					},
					Volumes: []corev1.Volume{
						{Name: "kubelet-dir", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/var/lib/kubelet", Type: &hostPathDir}}},
						{Name: "plugin-dir", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/var/lib/kubelet/plugins/diskplugin.csi.alibabacloud.com", Type: &hostPathDirOrCreate}}},
						{Name: "registration-dir", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/var/lib/kubelet/plugins_registry", Type: &hostPathDir}}},
						{Name: "dev", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/dev", Type: &hostPathDir}}},
						{Name: "sys", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/sys", Type: &hostPathDir}}},
					},
				},
			},
		},
	}

	return r.createOrUpdate(ctx, cr, ds)
}

func (r *AlibabaCloudCSIDriverReconciler) ensureDiskStorageClass(ctx context.Context, cr *csiv1alpha1.AlibabaCloudCSIDriver, sc csiv1alpha1.DiskStorageClassSpec, isDefault bool) error {
	reclaimPolicy := corev1.PersistentVolumeReclaimDelete
	if sc.ReclaimPolicy == "Retain" {
		reclaimPolicy = corev1.PersistentVolumeReclaimRetain
	}
	allowExpansion := sc.AllowVolumeExpansion

	annotations := map[string]string{}
	if isDefault {
		annotations["storageclass.kubernetes.io/is-default-class"] = "true"
	}
	if sc.VirtDefault {
		annotations["storageclass.kubevirt.io/is-default-virt-class"] = "true"
	}

	params := map[string]string{
		"type": sc.Type,
	}
	if sc.Encrypted {
		params["encrypted"] = "true"
	}

	obj := &storagev1.StorageClass{
		ObjectMeta: metav1.ObjectMeta{
			Name:        sc.Name,
			Annotations: annotations,
		},
		Provisioner:          diskDriverName,
		ReclaimPolicy:        &reclaimPolicy,
		AllowVolumeExpansion: &allowExpansion,
		Parameters:           params,
		VolumeBindingMode:    storageClassBindingMode(storagev1.VolumeBindingWaitForFirstConsumer),
	}
	return r.createOrAdopt(ctx, cr, obj)
}

// ── NAS Driver ───────────────────────────────────────────────────────────────────

// ensureNASDriver deploys the NAS CSI components, StorageClasses and CDI
// StorageProfiles. It deliberately creates NO VolumeSnapshotClass: the upstream
// NAS CSI controller does not implement CreateSnapshot (CREATE_DELETE_SNAPSHOT is
// not in its ControllerGetCapabilities), so NAS-backed PVCs — including
// OpenShift Virtualization VM disks that need ReadWriteMany for live migration —
// cannot be snapshotted via CSI. Protect NAS volumes with OADP file-system
// backup (kopia) to OSS instead of VolumeSnapshot / VirtualMachineSnapshot.
func (r *AlibabaCloudCSIDriverReconciler) ensureNASDriver(ctx context.Context, cr *csiv1alpha1.AlibabaCloudCSIDriver) error {
	imageTag := cr.Spec.ImageTag
	if imageTag == "" {
		imageTag = "v1.35.3"
	}
	pluginImage := fmt.Sprintf("%s:%s", csiImage, imageTag)

	if err := r.ensureNASCSIDriver(ctx, cr); err != nil {
		return fmt.Errorf("CSIDriver: %w", err)
	}
	if err := r.ensureNASControllerDeployment(ctx, cr, pluginImage); err != nil {
		return fmt.Errorf("controller Deployment: %w", err)
	}
	if err := r.ensureNASNodeDaemonSet(ctx, cr, pluginImage); err != nil {
		return fmt.Errorf("node DaemonSet: %w", err)
	}

	for i, sc := range cr.Spec.NAS.StorageClasses {
		isDefault := i == 0 // first NAS class is default NAS class (not cluster default)
		if err := r.ensureNASStorageClass(ctx, cr, sc, isDefault); err != nil {
			return fmt.Errorf("StorageClass %s: %w", sc.Name, err)
		}
		// Patch CDI StorageProfile for OKV compatibility (NAS = RWX Filesystem mode).
		// NAS snapshots are not yet implemented — pass empty snapshotClass.
		if err := r.ensureStorageProfile(ctx, sc.Name, cr.Spec.NAS.StorageProfile, nasClaimPropertySets(), ""); err != nil {
			return fmt.Errorf("StorageProfile %s: %w", sc.Name, err)
		}
	}

	return nil
}

// ensureNASCSIDriver creates the CSIDriver object for the NAS plugin.
// Key differences from disk:
//   - attachRequired: false  — NAS volumes don't need VolumeAttachment objects
//   - seLinuxMount: false    — NFS mounts don't support SELinux label relabelling
func (r *AlibabaCloudCSIDriverReconciler) ensureNASCSIDriver(ctx context.Context, cr *csiv1alpha1.AlibabaCloudCSIDriver) error {
	attachRequired := false
	podInfoOnMount := true
	seLinuxMount := false
	fsGroupPolicy := storagev1.FileFSGroupPolicy

	obj := &storagev1.CSIDriver{
		ObjectMeta: metav1.ObjectMeta{Name: nasDriverName},
		Spec: storagev1.CSIDriverSpec{
			AttachRequired: &attachRequired,
			PodInfoOnMount: &podInfoOnMount,
			FSGroupPolicy:  &fsGroupPolicy,
			SELinuxMount:   &seLinuxMount,
		},
	}
	return r.createOrAdopt(ctx, cr, obj)
}

func (r *AlibabaCloudCSIDriverReconciler) ensureNASControllerDeployment(ctx context.Context, cr *csiv1alpha1.AlibabaCloudCSIDriver, pluginImage string) error {
	replicas := cr.Spec.Controller.Replicas
	if replicas == 0 {
		replicas = 2
	}

	nodeSelector := cr.Spec.Controller.NodeSelector
	if nodeSelector == nil {
		nodeSelector = map[string]string{"node-role.kubernetes.io/master": ""}
	}

	tolerations := toCoreTolerations(cr.Spec.Controller.Tolerations)
	if len(tolerations) == 0 {
		tolerations = []corev1.Toleration{
			{Key: "node-role.kubernetes.io/master", Effect: corev1.TaintEffectNoSchedule},
		}
	}

	ramTokenVersion := cr.Spec.Auth.RAMToken
	if ramTokenVersion == "" {
		ramTokenVersion = "v2"
	}

	labels := map[string]string{"app": "csi-nas-controller"}

	// NAS controller only needs provisioner — no attacher (attachRequired=false),
	// no resizer (NAS capacity is flexible and serverless). Socket in an in-pod
	// emptyDir (/csi), controller-service only — same fix as the disk controller.
	socketMount := corev1.VolumeMount{Name: "socket-dir", MountPath: "/csi"}
	containers := []corev1.Container{
		{
			Name:  "csi-plugin",
			Image: pluginImage,
			Args:  []string{"--endpoint=$(CSI_ENDPOINT)", "--v=5", "--driver=nasplugin.csi.alibabacloud.com", "--run-node-service=false"},
			Env: append([]corev1.EnvVar{
				{Name: "CSI_ENDPOINT", Value: "unix:///csi/csi.sock"},
				{Name: "RAM_ROLE_TOKEN", Value: ramTokenVersion},
			}, driverEnv(cr)...),
			VolumeMounts: []corev1.VolumeMount{socketMount},
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("100m"),
					corev1.ResourceMemory: resource.MustParse("128Mi"),
				},
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("500m"),
					corev1.ResourceMemory: resource.MustParse("512Mi"),
				},
			},
		},
		{
			Name:  "csi-provisioner",
			Image: csiProvisionerImage,
			// --leader-election: 2 controller replicas — see the disk provisioner note.
			Args:         []string{"--csi-address=$(ADDRESS)", "--v=5", "--extra-create-metadata=true", "--leader-election=true"},
			Env:          []corev1.EnvVar{{Name: "ADDRESS", Value: "/csi/csi.sock"}},
			VolumeMounts: []corev1.VolumeMount{socketMount},
		},
	}

	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "csi-nas-controller",
			Namespace: operatorNamespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					ServiceAccountName: serviceAccountName,
					NodeSelector:       nodeSelector,
					Tolerations:        tolerations,
					Affinity:           controllerPodAntiAffinity(labels["app"]),
					Containers:         containers,
					Volumes: []corev1.Volume{
						{Name: "socket-dir", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
					},
				},
			},
		},
	}

	return r.createOrUpdate(ctx, cr, deploy)
}

func (r *AlibabaCloudCSIDriverReconciler) ensureNASNodeDaemonSet(ctx context.Context, cr *csiv1alpha1.AlibabaCloudCSIDriver, pluginImage string) error {
	ramTokenVersion := cr.Spec.Auth.RAMToken
	if ramTokenVersion == "" {
		ramTokenVersion = "v2"
	}

	hostPathDir := corev1.HostPathDirectory
	hostPathDirOrCreate := corev1.HostPathDirectoryOrCreate
	privileged := true
	labels := map[string]string{"app": "csi-nas-node"}

	ds := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "csi-nas-node",
			Namespace: operatorNamespace,
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					ServiceAccountName: serviceAccountName,
					HostNetwork:        true,
					Tolerations:        []corev1.Toleration{{Operator: corev1.TolerationOpExists}},
					Containers: []corev1.Container{
						{
							Name:  "csi-plugin",
							Image: pluginImage,
							// Node-service only + distinct SERVICE_PORT (11261) so it does not
							// collide with csi-disk-node (11260) on the host network. See the
							// disk node DaemonSet for the rationale.
							Args: []string{"--endpoint=$(CSI_ENDPOINT)", "--v=5", "--driver=nasplugin.csi.alibabacloud.com", "--run-controller-service=false"},
							SecurityContext: &corev1.SecurityContext{
								Privileged: &privileged,
							},
							Env: append([]corev1.EnvVar{
								{Name: "CSI_ENDPOINT", Value: "unix:///var/lib/kubelet/plugins/nasplugin.csi.alibabacloud.com/csi.sock"},
								{Name: "RAM_ROLE_TOKEN", Value: ramTokenVersion},
								{Name: "SERVICE_PORT", Value: "11261"},
								{Name: "KUBE_NODE_NAME", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "spec.nodeName"}}},
							}, driverEnv(cr)...),
							VolumeMounts: []corev1.VolumeMount{
								{Name: "kubelet-dir", MountPath: "/var/lib/kubelet", MountPropagation: mountPropagation(corev1.MountPropagationBidirectional)},
								{Name: "plugin-dir", MountPath: "/var/lib/kubelet/plugins/nasplugin.csi.alibabacloud.com"},
								{Name: "registration-dir", MountPath: "/registration"},
							},
							// 9811 = nas registrar --http-endpoint; restart plugin until
							// kubelet registration succeeds (see registrarHealthzStartupProbe).
							StartupProbe: registrarHealthzStartupProbe(9811),
						},
						{
							Name:  "node-driver-registrar",
							Image: nodeRegistrarImage,
							// --http-endpoint /healthz drives the csi-plugin startupProbe on
							// :9811 (distinct from disk 9810 — both DaemonSets are hostNetwork).
							Args: []string{
								"--v=5",
								"--csi-address=/var/lib/kubelet/plugins/nasplugin.csi.alibabacloud.com/csi.sock",
								"--kubelet-registration-path=/var/lib/kubelet/plugins/nasplugin.csi.alibabacloud.com/csi.sock",
								"--http-endpoint=:9811",
							},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "plugin-dir", MountPath: "/var/lib/kubelet/plugins/nasplugin.csi.alibabacloud.com"},
								{Name: "registration-dir", MountPath: "/registration"},
							},
						},
						{
							Name:  "liveness-probe",
							Image: livenessProbeImage,
							// Distinct --health-port (9809) from csi-disk-node's 9808 — both are
							// hostNetwork, so they would otherwise collide on :9808.
							Args: []string{"--csi-address=/var/lib/kubelet/plugins/nasplugin.csi.alibabacloud.com/csi.sock", "--health-port=9809"},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "plugin-dir", MountPath: "/var/lib/kubelet/plugins/nasplugin.csi.alibabacloud.com"},
							},
						},
					},
					Volumes: []corev1.Volume{
						{Name: "kubelet-dir", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/var/lib/kubelet", Type: &hostPathDir}}},
						{Name: "plugin-dir", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/var/lib/kubelet/plugins/nasplugin.csi.alibabacloud.com", Type: &hostPathDirOrCreate}}},
						{Name: "registration-dir", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/var/lib/kubelet/plugins_registry", Type: &hostPathDir}}},
					},
				},
			},
		},
	}

	return r.createOrUpdate(ctx, cr, ds)
}

// nasStorageClassParameters builds the upstream NAS CSI StorageClass parameters
// for the requested provisioning mode (NASStorageClassSpec.VolumeAs):
//
//	filesystem (default) — the driver creates a NAS filesystem + mount target per
//	  PV; needs fileSystemType + the cluster network (regionId/zoneId/vpcId/vSwitchId).
//	subpath — the driver carves subpaths from a pre-existing NAS filesystem; needs
//	  the server (mount-target) endpoint.
//
// Returns the params and any cluster network fields that are missing for the
// chosen mode (so the caller / a validator can warn that provisioning will fail).
func nasStorageClassParameters(sc csiv1alpha1.NASStorageClassSpec) (params map[string]string, missing []string) {
	volumeAs := sc.VolumeAs
	if volumeAs == "" {
		volumeAs = "filesystem"
	}
	params = map[string]string{"volumeAs": volumeAs}

	switch volumeAs {
	case "subpath":
		mountProtocol := sc.MountProtocol
		if mountProtocol == "" {
			mountProtocol = "nfs"
		}
		params["mountType"] = mountProtocol
		if sc.Server != "" {
			params["server"] = sc.Server
		} else {
			missing = append(missing, "server")
		}
		if sc.StorageType != "" {
			params["storageType"] = sc.StorageType
		}
	default: // filesystem
		fst := sc.FileSystemType
		if fst == "" {
			fst = "standard"
		}
		params["fileSystemType"] = fst
		if sc.StorageType != "" {
			params["storageType"] = sc.StorageType
		}
		for _, f := range []struct {
			key, val string
		}{
			{"regionId", sc.RegionID},
			{"zoneId", sc.ZoneID},
			{"vpcId", sc.VpcID},
			{"vSwitchId", sc.VSwitchID},
		} {
			if f.val != "" {
				params[f.key] = f.val
			} else {
				missing = append(missing, f.key)
			}
		}
	}
	return params, missing
}

func (r *AlibabaCloudCSIDriverReconciler) ensureNASStorageClass(ctx context.Context, cr *csiv1alpha1.AlibabaCloudCSIDriver, sc csiv1alpha1.NASStorageClassSpec, _ bool) error {
	reclaimPolicy := corev1.PersistentVolumeReclaimDelete
	if sc.ReclaimPolicy == "Retain" {
		reclaimPolicy = corev1.PersistentVolumeReclaimRetain
	}
	allowExpansion := sc.AllowVolumeExpansion

	annotations := map[string]string{}
	if sc.VirtDefault {
		annotations["storageclass.kubevirt.io/is-default-virt-class"] = "true"
	}

	params, missing := nasStorageClassParameters(sc)
	if len(missing) > 0 {
		// Don't fail — the SC is still created, but it cannot dynamically provision
		// until the missing fields are supplied (PVCs would stay Pending). Surface it.
		logf.FromContext(ctx).Info("NAS StorageClass is missing fields required for dynamic provisioning",
			"storageClass", sc.Name, "volumeAs", params["volumeAs"], "missing", missing)
	}

	obj := &storagev1.StorageClass{
		ObjectMeta: metav1.ObjectMeta{
			Name:        sc.Name,
			Annotations: annotations,
		},
		Provisioner:          nasDriverName,
		ReclaimPolicy:        &reclaimPolicy,
		AllowVolumeExpansion: &allowExpansion,
		Parameters:           params,
		// Immediate binding is required for RWX NAS volumes — WaitForFirstConsumer
		// does not work well with multi-node access patterns.
		VolumeBindingMode: storageClassBindingMode(storagev1.VolumeBindingImmediate),
	}
	return r.createOrAdopt(ctx, cr, obj)
}

// ── VolumeSnapshotClass ──────────────────────────────────────────────────────────

// ensureVolumeSnapshotClass creates a VolumeSnapshotClass for the given driver.
// The function gracefully skips creation if the snapshot.storage.k8s.io CRDs are
// not installed on the cluster (returns nil instead of an error).
//
// Only the disk driver is wired to call this (see ensureDiskDriver). The NAS CSI
// driver does not implement CreateSnapshot upstream, so no NAS snapshot class is
// ever created — see the defaultDiskSnapClassName const block.
func (r *AlibabaCloudCSIDriverReconciler) ensureVolumeSnapshotClass(ctx context.Context, cr *csiv1alpha1.AlibabaCloudCSIDriver, driverName string, cfg csiv1alpha1.SnapshotConfig) error {
	if !cfg.Enabled {
		return nil
	}

	className := cfg.ClassName
	if className == "" {
		className = defaultDiskSnapClassName
	}

	u := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "snapshot.storage.k8s.io/v1",
			"kind":       "VolumeSnapshotClass",
			"metadata": map[string]interface{}{
				"name": className,
				"annotations": map[string]interface{}{
					"snapshot.storage.kubernetes.io/is-default-class": "true",
				},
			},
			"driver":         driverName,
			"deletionPolicy": "Delete",
		},
	}

	if err := controllerutil.SetControllerReference(cr, u, r.Scheme); err != nil {
		return err
	}

	if err := r.Client.Create(ctx, u); err != nil {
		if apimeta.IsNoMatchError(err) {
			// snapshot.storage.k8s.io CRDs are not installed — skip silently.
			logf.FromContext(ctx).Info("VolumeSnapshot CRDs not found, skipping VolumeSnapshotClass", "class", className)
			return nil
		}
		if apierrors.IsAlreadyExists(err) {
			return nil
		}
		return err
	}
	return nil
}

// ── CDI StorageProfile ───────────────────────────────────────────────────────────

// diskClaimPropertySets returns the CDI claimPropertySets for a disk StorageClass.
// Cloud disks attach to a single node, so the access mode is always ReadWriteOnce;
// the volume mode follows the StorageClass intent:
//
//	"Block"      → RWO + Block (raw block device, best VM OS-disk performance)
//	"Filesystem" → RWO + Filesystem (general container workloads; also the default
//	               when volumeMode is empty)
//
// Matches the CDI built-in table for ebs.csi.aws.com → {{rwo, block}}.
func diskClaimPropertySets(volumeMode string) []interface{} {
	mode := "Filesystem"
	if strings.EqualFold(volumeMode, "Block") {
		mode = "Block"
	}
	return []interface{}{
		map[string]interface{}{
			"accessModes": []interface{}{"ReadWriteOnce"},
			"volumeMode":  mode,
		},
	}
}

// nasClaimPropertySets returns the CDI claimPropertySets for a NAS StorageClass.
// NAS provides ReadWriteMany Filesystem volumes required for live VM migration,
// with a ReadWriteOnce Filesystem fallback for single-attach workloads.
// Matches the CDI built-in table for efs.csi.aws.com → {{rwx, file}, {rwo, file}}.
func nasClaimPropertySets() []interface{} {
	return []interface{}{
		map[string]interface{}{
			"accessModes": []interface{}{"ReadWriteMany"},
			"volumeMode":  "Filesystem",
		},
		map[string]interface{}{
			"accessModes": []interface{}{"ReadWriteOnce"},
			"volumeMode":  "Filesystem",
		},
	}
}

// ensureStorageProfile patches the CDI StorageProfile for the given StorageClass
// to set correct claimPropertySets, cloneStrategy, and snapshotClass for
// OpenShift Virtualization.
//
// claimPropertySets is the desired CDI claimPropertySets, computed by the caller
// via diskClaimPropertySets / nasClaimPropertySets so that per-StorageClass volume
// modes (disk Block vs disk Filesystem vs NAS RWX) are honoured.
//
// snapClassName is the VolumeSnapshotClass name to link via spec.snapshotClass.
// CDI uses this to select the right VolumeSnapshotClass for snapshot-based clones
// instead of guessing by provisioner. Pass "" to leave the field unset.
//
// The function silently returns nil if:
//   - cfg.Patch is false
//   - CDI is not installed (NoMatchError)
//   - The StorageProfile does not exist yet (CDI creates it lazily)
func (r *AlibabaCloudCSIDriverReconciler) ensureStorageProfile(ctx context.Context, name string, cfg csiv1alpha1.StorageProfileConfig, claimPropertySets []interface{}, snapClassName string) error {
	if !cfg.Patch {
		return nil
	}

	sp := &unstructured.Unstructured{}
	sp.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "cdi.kubevirt.io",
		Version: "v1beta1",
		Kind:    "StorageProfile",
	})

	if err := r.Client.Get(ctx, types.NamespacedName{Name: name}, sp); err != nil {
		if apimeta.IsNoMatchError(err) {
			// CDI is not installed — skip.
			return nil
		}
		if apierrors.IsNotFound(err) {
			// StorageProfile not created by CDI yet — will be retried next reconcile.
			return nil
		}
		return err
	}

	// CDI CloneStrategy values (from cdi.kubevirt.io/v1beta1 CDICloneStrategy type):
	//   "csi-clone"  — CloneStrategyCsiClone
	//   "snapshot"   — CloneStrategySnapshot
	//   "copy"       — CloneStrategyHostAssisted (NOT "host-assisted")
	cloneStrategy := cfg.CloneStrategy
	if cloneStrategy == "" {
		cloneStrategy = "csi-clone"
	}

	specPatch := map[string]interface{}{
		"claimPropertySets": claimPropertySets,
		"cloneStrategy":     cloneStrategy,
	}
	// spec.snapshotClass links the VolumeSnapshotClass for snapshot-based cloning.
	// CDI doc: "If not set, a VolumeSnapshotClass is chosen according to the provisioner."
	// Explicitly setting it avoids ambiguity when multiple VolumeSnapshotClasses exist.
	if snapClassName != "" {
		specPatch["snapshotClass"] = snapClassName
	}

	patchData, err := json.Marshal(map[string]interface{}{"spec": specPatch})
	if err != nil {
		return fmt.Errorf("marshal StorageProfile patch: %w", err)
	}

	return r.Client.Patch(ctx, sp, client.RawPatch(types.MergePatchType, patchData))
}

// ── Status helpers ───────────────────────────────────────────────────────────────

func buildConditions(allReady bool, err error) []csiv1alpha1.CSIDriverCondition {
	now := metav1.Now()
	if err != nil {
		return []csiv1alpha1.CSIDriverCondition{
			{Type: csiv1alpha1.ConditionAvailable, Status: "False", Reason: "ReconcileError", Message: err.Error(), LastTransitionTime: now},
			{Type: csiv1alpha1.ConditionDegraded, Status: "True", Reason: "ReconcileError", Message: err.Error(), LastTransitionTime: now},
			{Type: csiv1alpha1.ConditionProgressing, Status: "False", Reason: "Error", LastTransitionTime: now},
		}
	}
	avail := "False"
	if allReady {
		avail = "True"
	}
	return []csiv1alpha1.CSIDriverCondition{
		{Type: csiv1alpha1.ConditionAvailable, Status: avail, Reason: "AsExpected", LastTransitionTime: now},
		{Type: csiv1alpha1.ConditionDegraded, Status: "False", Reason: "AsExpected", LastTransitionTime: now},
		{Type: csiv1alpha1.ConditionProgressing, Status: "False", Reason: "AsExpected", LastTransitionTime: now},
	}
}

// ── Generic helpers ──────────────────────────────────────────────────────────────

// createOrIgnore creates the object if it does not exist; ignores AlreadyExists.
// createOrAdopt creates obj (owned by cr) if it is absent. If it already exists
// it adopts it by setting the controller reference — this covers resources left
// by an operator version that predated owner references, so a later
// `oc delete alibabacloudcsidriver` garbage-collects them instead of orphaning
// them. cr is cluster-scoped, so it may own both cluster-scoped children
// (CSIDriver / StorageClass / ClusterRole[Binding]) and namespaced ones
// (Deployment / DaemonSet / ServiceAccount in kube-system).
func (r *AlibabaCloudCSIDriverReconciler) createOrAdopt(ctx context.Context, cr *csiv1alpha1.AlibabaCloudCSIDriver, obj client.Object) error {
	if err := controllerutil.SetControllerReference(cr, obj, r.Scheme); err != nil {
		return err
	}
	err := r.Create(ctx, obj)
	if err == nil {
		return nil
	}
	if !apierrors.IsAlreadyExists(err) {
		return err
	}
	// Adopt an object left by a pre-owner-reference version so teardown GCs it.
	// Re-Get inside the retry so a concurrent modification is handled cleanly.
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		existing := obj.DeepCopyObject().(client.Object)
		if err := r.Get(ctx, client.ObjectKeyFromObject(obj), existing); err != nil {
			return err
		}
		if metav1.IsControlledBy(existing, cr) {
			return nil
		}
		if err := controllerutil.SetControllerReference(cr, existing, r.Scheme); err != nil {
			return err
		}
		return r.Update(ctx, existing)
	})
}

// createOrUpdate creates the object (owned by cr) if it does not exist, or
// replaces it if it does — writing the owner reference either way, which also
// adopts an existing object left by a pre-owner-reference operator version.
func (r *AlibabaCloudCSIDriverReconciler) createOrUpdate(ctx context.Context, cr *csiv1alpha1.AlibabaCloudCSIDriver, obj client.Object) error {
	if err := controllerutil.SetControllerReference(cr, obj, r.Scheme); err != nil {
		return err
	}
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		existing := obj.DeepCopyObject().(client.Object)
		err := r.Get(ctx, client.ObjectKeyFromObject(obj), existing)
		if err != nil {
			if apierrors.IsNotFound(err) {
				return r.Create(ctx, obj)
			}
			return err
		}
		obj.SetResourceVersion(existing.GetResourceVersion())
		return r.Update(ctx, obj)
	})
}

func toCoreTolerations(in []csiv1alpha1.TolerationSpec) []corev1.Toleration {
	out := make([]corev1.Toleration, 0, len(in))
	for _, t := range in {
		out = append(out, corev1.Toleration{
			Key:      t.Key,
			Operator: corev1.TolerationOperator(t.Operator),
			Value:    t.Value,
			Effect:   corev1.TaintEffect(t.Effect),
		})
	}
	return out
}

// controllerPodAntiAffinity spreads the controller's replicas across distinct
// nodes (hostnames) using a SOFT (preferred) rule. The controller runs 2 replicas
// pinned to control-plane nodes via NodeSelector. On a single-master cluster a hard
// rule would leave the second replica Pending forever, so this is preferred-only:
// with one master both replicas co-locate (acceptable), with multiple masters the
// scheduler places the second replica on a different master for real HA.
func controllerPodAntiAffinity(appLabel string) *corev1.Affinity {
	return &corev1.Affinity{
		PodAntiAffinity: &corev1.PodAntiAffinity{
			PreferredDuringSchedulingIgnoredDuringExecution: []corev1.WeightedPodAffinityTerm{
				{
					Weight: 100,
					PodAffinityTerm: corev1.PodAffinityTerm{
						TopologyKey:   "kubernetes.io/hostname",
						LabelSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": appLabel}},
					},
				},
			},
		},
	}
}

// registrarHealthzStartupProbe makes the node csi-plugin container restart itself
// until the driver is actually registered with kubelet. node-driver-registrar's
// --http-endpoint /healthz reports unhealthy until registration succeeds, and
// registration only succeeds once NodeGetInfo (→ ECS DescribeDisks, a signed call)
// works. On some nodes the csi-plugin builds its ECS client before the ecs_ram_role
// credential provider has warmed up, caches a credential-less client, and issues an
// unsigned DescribeDisks forever ("IllegalTimestamp: Timestamp not supplied") — only
// node-driver-registrar restarts, against the same stuck plugin process, so it never
// recovers. Probing the registrar's healthz from the PLUGIN container restarts the
// plugin instead, which rebuilds the ECS client with now-warm credentials.
// failureThreshold*periodSeconds (=60s) is the per-attempt grace before a restart.
func registrarHealthzStartupProbe(port int32) *corev1.Probe {
	return &corev1.Probe{
		ProbeHandler: corev1.ProbeHandler{
			HTTPGet: &corev1.HTTPGetAction{
				Path: "/healthz",
				Port: intstr.FromInt32(port),
				Host: "127.0.0.1",
			},
		},
		InitialDelaySeconds: 10,
		PeriodSeconds:       10,
		FailureThreshold:    6,
	}
}

func mountPropagation(m corev1.MountPropagationMode) *corev1.MountPropagationMode { return &m }

// driverEnv returns the environment appended to every csi-plugin container:
// spec.extraEnv, plus the ECS_ENDPOINT override when spec.ecsEndpoint is set
// (e.g. ecs-vpc.<region>.aliyuncs.com on air-gapped clusters, where the default
// public endpoint times out and node registration fails on DescribeDisks).
// Nothing configured returns nil, leaving the driver's own defaults.
func driverEnv(cr *csiv1alpha1.AlibabaCloudCSIDriver) []corev1.EnvVar {
	env := extraEnv(cr)
	if cr.Spec.ECSEndpoint != "" {
		env = append(env, corev1.EnvVar{Name: "ECS_ENDPOINT", Value: cr.Spec.ECSEndpoint})
	}
	return env
}

// extraEnv passes spec.extraEnv through to every csi-plugin container.  The
// driver exposes endpoints, scheme and request headers as environment variables
// (NAS_ENDPOINT, ALICLOUD_CLIENT_SCHEME, ALIBABA_CLOUD_HTTP_HEADERS, ...), which
// is what makes a private cloud configurable at all — see ExtraEnv's doc.
func extraEnv(cr *csiv1alpha1.AlibabaCloudCSIDriver) []corev1.EnvVar {
	if len(cr.Spec.ExtraEnv) == 0 {
		return nil
	}
	env := make([]corev1.EnvVar, 0, len(cr.Spec.ExtraEnv))
	for _, e := range cr.Spec.ExtraEnv {
		v := corev1.EnvVar{Name: e.Name}
		if e.SecretKeyRef != nil {
			v.ValueFrom = &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: e.SecretKeyRef.Name},
					Key:                  e.SecretKeyRef.Key,
				},
			}
		} else {
			v.Value = e.Value
		}
		env = append(env, v)
	}
	return env
}

func storageClassBindingMode(m storagev1.VolumeBindingMode) *storagev1.VolumeBindingMode { return &m }
