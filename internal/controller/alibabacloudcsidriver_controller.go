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
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	csiv1alpha1 "github.com/SammZhu/alibaba-cloud-csi-operator/api/v1alpha1"
)

const (
	diskDriverName      = "diskplugin.csi.alibabacloud.com"
	nasDriverName       = "nasplugin.csi.alibabacloud.com"
	ossDriverName       = "ossplugin.csi.alibabacloud.com"
	serviceAccountName  = "alibaba-cloud-csi-sa"
	operatorNamespace   = "kube-system"
	csiImage            = "registry.cn-hangzhou.aliyuncs.com/acs/csi-plugin"
	csiProvisionerImage = "registry.cn-hangzhou.aliyuncs.com/acs/csi-provisioner:v3.5.0"
	csiAttacherImage    = "registry.cn-hangzhou.aliyuncs.com/acs/csi-attacher:v4.3.0"
	csiResizerImage     = "registry.cn-hangzhou.aliyuncs.com/acs/csi-resizer:v1.8.0"
	nodeRegistrarImage  = "registry.cn-hangzhou.aliyuncs.com/acs/csi-node-driver-registrar:v2.8.0"
	livenessProbeImage  = "registry.cn-hangzhou.aliyuncs.com/acs/livenessprobe:v2.10.0"

	fieldManager = "alibaba-cloud-csi-operator"
)

// AlibabaCloudCSIDriverReconciler reconciles a AlibabaCloudCSIDriver object
type AlibabaCloudCSIDriverReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=csi.alibabacloud.com,resources=alibabacloudcsidrivers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=csi.alibabacloud.com,resources=alibabacloudcsidrivers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=csi.alibabacloud.com,resources=alibabacloudcsidrivers/finalizers,verbs=update
// +kubebuilder:rbac:groups=storage.k8s.io,resources=csidrivers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=storage.k8s.io,resources=storageclasses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=deployments;daemonsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterroles;clusterrolebindings,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *AlibabaCloudCSIDriverReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var cr csiv1alpha1.AlibabaCloudCSIDriver
	if err := r.Get(ctx, req.NamespacedName, &cr); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	log.Info("Reconciling AlibabaCloudCSIDriver", "name", cr.Name)

	// Track whether any reconcile step fails so we update Status.Conditions correctly.
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

	// 3. NAS driver (Phase 2 — skip if not enabled).
	nasReady := false
	if reconcileErr == nil && cr.Spec.NAS.Enabled {
		// Phase 2: NAS reconcile not yet implemented.
		log.Info("NAS CSI driver requested but not yet implemented — skipping")
	}

	// 4. OSS driver (Phase 3 — skip if not enabled).
	ossReady := false
	if reconcileErr == nil && cr.Spec.OSS.Enabled {
		// Phase 3: OSS reconcile not yet implemented.
		log.Info("OSS CSI driver requested but not yet implemented — skipping")
	}

	// 5. Update status.
	patch := client.MergeFrom(cr.DeepCopy())
	cr.Status.ObservedGeneration = cr.Generation
	cr.Status.DiskDriverReady = diskReady
	cr.Status.NASDriverReady = nasReady
	cr.Status.OSSDriverReady = ossReady
	cr.Status.Conditions = buildConditions(diskReady, reconcileErr)
	if err := r.Status().Patch(ctx, &cr, patch); err != nil {
		log.Error(err, "failed to update status")
	}

	return ctrl.Result{}, reconcileErr
}

// SetupWithManager sets up the controller with the Manager.
func (r *AlibabaCloudCSIDriverReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&csiv1alpha1.AlibabaCloudCSIDriver{}).
		Owns(&appsv1.Deployment{}).
		Owns(&appsv1.DaemonSet{}).
		Owns(&storagev1.StorageClass{}).
		Named("alibabacloudcsidriver").
		Complete(r)
}

// ── RBAC ────────────────────────────────────────────────────────────────────────

func (r *AlibabaCloudCSIDriverReconciler) ensureRBAC(ctx context.Context, cr *csiv1alpha1.AlibabaCloudCSIDriver) error {
	if err := r.ensureServiceAccount(ctx); err != nil {
		return err
	}
	if err := r.ensureClusterRole(ctx); err != nil {
		return err
	}
	if err := r.ensureClusterRoleBinding(ctx); err != nil {
		return err
	}
	// SCC privileged binding (OpenShift-specific).
	return r.ensureSCCBinding(ctx)
}

func (r *AlibabaCloudCSIDriverReconciler) ensureServiceAccount(ctx context.Context) error {
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceAccountName,
			Namespace: operatorNamespace,
		},
	}
	return createOrIgnore(ctx, r.Client, sa)
}

func (r *AlibabaCloudCSIDriverReconciler) ensureClusterRole(ctx context.Context) error {
	cr := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{Name: "alibaba-cloud-csi-role"},
		Rules: []rbacv1.PolicyRule{
			{APIGroups: []string{""}, Resources: []string{"nodes", "namespaces", "pods", "persistentvolumes", "persistentvolumeclaims"}, Verbs: []string{"get", "list", "watch", "update", "patch"}},
			{APIGroups: []string{""}, Resources: []string{"events"}, Verbs: []string{"get", "list", "watch", "create", "update", "patch"}},
			{APIGroups: []string{"storage.k8s.io"}, Resources: []string{"storageclasses", "csinodes", "csidrivers", "volumeattachments"}, Verbs: []string{"get", "list", "watch", "update", "patch"}},
			{APIGroups: []string{"storage.k8s.io"}, Resources: []string{"volumeattachments/status"}, Verbs: []string{"patch"}},
			{APIGroups: []string{"snapshot.storage.k8s.io"}, Resources: []string{"volumesnapshots", "volumesnapshotcontents", "volumesnapshotclasses"}, Verbs: []string{"get", "list", "watch", "create", "update", "patch", "delete"}},
		},
	}
	return createOrIgnore(ctx, r.Client, cr)
}

func (r *AlibabaCloudCSIDriverReconciler) ensureClusterRoleBinding(ctx context.Context) error {
	crb := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "alibaba-cloud-csi-binding"},
		RoleRef:    rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "ClusterRole", Name: "alibaba-cloud-csi-role"},
		Subjects:   []rbacv1.Subject{{Kind: "ServiceAccount", Name: serviceAccountName, Namespace: operatorNamespace}},
	}
	return createOrIgnore(ctx, r.Client, crb)
}

// ensureSCCBinding creates the ClusterRoleBinding that grants the CSI ServiceAccount
// the OpenShift privileged SCC. This is required for the Node DaemonSet to mount /dev.
func (r *AlibabaCloudCSIDriverReconciler) ensureSCCBinding(ctx context.Context) error {
	crb := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "alibabacloud-csi-privileged"},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     "system:openshift:scc:privileged",
		},
		Subjects: []rbacv1.Subject{{Kind: "ServiceAccount", Name: serviceAccountName, Namespace: operatorNamespace}},
	}
	return createOrIgnore(ctx, r.Client, crb)
}

// ── Disk Driver ──────────────────────────────────────────────────────────────────

func (r *AlibabaCloudCSIDriverReconciler) ensureDiskDriver(ctx context.Context, cr *csiv1alpha1.AlibabaCloudCSIDriver) error {
	imageTag := cr.Spec.ImageTag
	if imageTag == "" {
		imageTag = "v1.35.3"
	}
	pluginImage := fmt.Sprintf("%s:%s", csiImage, imageTag)

	// CSIDriver object.
	if err := r.ensureCSIDriver(ctx, diskDriverName); err != nil {
		return fmt.Errorf("CSIDriver: %w", err)
	}

	// Controller Deployment.
	if err := r.ensureDiskControllerDeployment(ctx, cr, pluginImage); err != nil {
		return fmt.Errorf("controller Deployment: %w", err)
	}

	// Node DaemonSet.
	if err := r.ensureDiskNodeDaemonSet(ctx, cr, pluginImage); err != nil {
		return fmt.Errorf("node DaemonSet: %w", err)
	}

	// StorageClasses.
	for i, sc := range cr.Spec.Disk.StorageClasses {
		isDefault := cr.Spec.Disk.DefaultStorageClass && i == 0
		if err := r.ensureStorageClass(ctx, sc, isDefault); err != nil {
			return fmt.Errorf("StorageClass %s: %w", sc.Name, err)
		}
	}

	return nil
}

func (r *AlibabaCloudCSIDriverReconciler) ensureCSIDriver(ctx context.Context, driverName string) error {
	attachRequired := true
	podInfoOnMount := true
	fsGroupPolicy := storagev1.FileFSGroupPolicy

	obj := &storagev1.CSIDriver{
		ObjectMeta: metav1.ObjectMeta{Name: driverName},
		Spec: storagev1.CSIDriverSpec{
			AttachRequired: &attachRequired,
			PodInfoOnMount: &podInfoOnMount,
			FSGroupPolicy:  &fsGroupPolicy,
		},
	}
	return createOrIgnore(ctx, r.Client, obj)
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
					Containers: []corev1.Container{
						{
							Name:  "csi-plugin",
							Image: pluginImage,
							Args:  []string{"--endpoint=$(CSI_ENDPOINT)", "--v=5", "--driver=diskplugin.csi.alibabacloud.com"},
							Env: []corev1.EnvVar{
								{Name: "CSI_ENDPOINT", Value: "unix:///var/lib/kubelet/plugins/diskplugin.csi.alibabacloud.com/csi.sock"},
								{Name: "MAX_VOLUMES_PERNODE", Value: "15"},
								{Name: "RAM_ROLE_TOKEN", Value: ramTokenVersion},
							},
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
							Args:  []string{"--csi-address=$(ADDRESS)", "--v=5", "--feature-gates=Topology=True", "--extra-create-metadata=true"},
							Env: []corev1.EnvVar{
								{Name: "ADDRESS", Value: "/var/lib/kubelet/plugins/diskplugin.csi.alibabacloud.com/csi.sock"},
							},
						},
						{
							Name:  "csi-attacher",
							Image: csiAttacherImage,
							Args:  []string{"--v=5", "--csi-address=$(ADDRESS)", "--leader-election=true"},
							Env: []corev1.EnvVar{
								{Name: "ADDRESS", Value: "/var/lib/kubelet/plugins/diskplugin.csi.alibabacloud.com/csi.sock"},
							},
						},
						{
							Name:  "csi-resizer",
							Image: csiResizerImage,
							Args:  []string{"--v=5", "--csi-address=$(ADDRESS)", "--leader-election=true"},
							Env: []corev1.EnvVar{
								{Name: "ADDRESS", Value: "/var/lib/kubelet/plugins/diskplugin.csi.alibabacloud.com/csi.sock"},
							},
						},
					},
					Volumes: []corev1.Volume{
						{Name: "socket-dir", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
					},
				},
			},
		},
	}

	return createOrUpdate(ctx, r.Client, deploy)
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
							Args:  []string{"--endpoint=$(CSI_ENDPOINT)", "--v=5", "--driver=diskplugin.csi.alibabacloud.com"},
							SecurityContext: &corev1.SecurityContext{
								Privileged: &privileged,
							},
							Env: []corev1.EnvVar{
								{Name: "CSI_ENDPOINT", Value: "unix:///var/lib/kubelet/plugins/diskplugin.csi.alibabacloud.com/csi.sock"},
								{Name: "RAM_ROLE_TOKEN", Value: ramTokenVersion},
								{Name: "KUBE_NODE_NAME", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "spec.nodeName"}}},
							},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "kubelet-dir", MountPath: "/var/lib/kubelet", MountPropagation: mountPropagation(corev1.MountPropagationBidirectional)},
								{Name: "plugin-dir", MountPath: "/var/lib/kubelet/plugins/diskplugin.csi.alibabacloud.com"},
								{Name: "registration-dir", MountPath: "/registration"},
								{Name: "dev", MountPath: "/dev"},
								{Name: "sys", MountPath: "/sys"},
							},
						},
						{
							Name:  "node-driver-registrar",
							Image: nodeRegistrarImage,
							Args: []string{
								"--v=5",
								"--csi-address=/var/lib/kubelet/plugins/diskplugin.csi.alibabacloud.com/csi.sock",
								"--kubelet-registration-path=/var/lib/kubelet/plugins/diskplugin.csi.alibabacloud.com/csi.sock",
							},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "plugin-dir", MountPath: "/var/lib/kubelet/plugins/diskplugin.csi.alibabacloud.com"},
								{Name: "registration-dir", MountPath: "/registration"},
							},
						},
						{
							Name:  "liveness-probe",
							Image: livenessProbeImage,
							Args:  []string{"--csi-address=/var/lib/kubelet/plugins/diskplugin.csi.alibabacloud.com/csi.sock"},
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

	return createOrUpdate(ctx, r.Client, ds)
}

func (r *AlibabaCloudCSIDriverReconciler) ensureStorageClass(ctx context.Context, sc csiv1alpha1.DiskStorageClassSpec, isDefault bool) error {
	reclaimPolicy := corev1.PersistentVolumeReclaimDelete
	if sc.ReclaimPolicy == "Retain" {
		reclaimPolicy = corev1.PersistentVolumeReclaimRetain
	}
	allowExpansion := sc.AllowVolumeExpansion

	annotations := map[string]string{}
	if isDefault {
		annotations["storageclass.kubernetes.io/is-default-class"] = "true"
	}

	obj := &storagev1.StorageClass{
		ObjectMeta: metav1.ObjectMeta{
			Name:        sc.Name,
			Annotations: annotations,
		},
		Provisioner:          diskDriverName,
		ReclaimPolicy:        &reclaimPolicy,
		AllowVolumeExpansion: &allowExpansion,
		Parameters: map[string]string{
			"type": sc.Type,
		},
		VolumeBindingMode: storageClassBindingMode(storagev1.VolumeBindingWaitForFirstConsumer),
	}
	return createOrIgnore(ctx, r.Client, obj)
}

// ── Status helpers ───────────────────────────────────────────────────────────────

func buildConditions(diskReady bool, err error) []csiv1alpha1.CSIDriverCondition {
	now := metav1.Now()
	if err != nil {
		return []csiv1alpha1.CSIDriverCondition{
			{Type: csiv1alpha1.ConditionAvailable, Status: "False", Reason: "ReconcileError", Message: err.Error(), LastTransitionTime: now},
			{Type: csiv1alpha1.ConditionDegraded, Status: "True", Reason: "ReconcileError", Message: err.Error(), LastTransitionTime: now},
			{Type: csiv1alpha1.ConditionProgressing, Status: "False", Reason: "Error", LastTransitionTime: now},
		}
	}
	avail := "False"
	if diskReady {
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
func createOrIgnore(ctx context.Context, c client.Client, obj client.Object) error {
	if err := c.Create(ctx, obj); err != nil && !errors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

// createOrUpdate creates the object if it does not exist, or patches it if it does.
func createOrUpdate(ctx context.Context, c client.Client, obj client.Object) error {
	existing := obj.DeepCopyObject().(client.Object)
	err := c.Get(ctx, types.NamespacedName{Name: obj.GetName(), Namespace: obj.GetNamespace()}, existing)
	if err != nil {
		if errors.IsNotFound(err) {
			return c.Create(ctx, obj)
		}
		return err
	}
	obj.SetResourceVersion(existing.GetResourceVersion())
	return c.Update(ctx, obj)
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

func mountPropagation(m corev1.MountPropagationMode) *corev1.MountPropagationMode { return &m }

func storageClassBindingMode(m storagev1.VolumeBindingMode) *storagev1.VolumeBindingMode { return &m }

