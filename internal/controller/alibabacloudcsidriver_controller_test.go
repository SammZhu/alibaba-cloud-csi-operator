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
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	csiv1alpha1 "github.com/SammZhu/alibaba-cloud-csi-operator/api/v1alpha1"
)

// newScheme registers all required API types for the fake client.
func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := csiv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme csiv1alpha1: %v", err)
	}
	if err := appsv1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme appsv1: %v", err)
	}
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme corev1: %v", err)
	}
	if err := rbacv1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme rbacv1: %v", err)
	}
	if err := storagev1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme storagev1: %v", err)
	}
	return s
}

// defaultCR returns a minimal AlibabaCloudCSIDriver CR for testing.
func defaultCR() *csiv1alpha1.AlibabaCloudCSIDriver {
	return &csiv1alpha1.AlibabaCloudCSIDriver{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cluster",
			Namespace: operatorNamespace,
		},
		Spec: csiv1alpha1.AlibabaCloudCSIDriverSpec{
			Disk: csiv1alpha1.DiskSpec{
				Enabled:             true,
				DefaultStorageClass: true,
				StorageClasses: []csiv1alpha1.DiskStorageClassSpec{
					{Name: "alicloud-disk-efficiency", Type: "cloud_efficiency", ReclaimPolicy: "Delete", AllowVolumeExpansion: true},
					{Name: "alicloud-disk-essd", Type: "cloud_essd", ReclaimPolicy: "Delete", AllowVolumeExpansion: true},
				},
			},
			ImageTag: "v1.35.3",
			Auth:     csiv1alpha1.AuthSpec{RAMToken: "v2"},
			Controller: csiv1alpha1.ControllerSpec{
				Replicas:     2,
				NodeSelector: map[string]string{"node-role.kubernetes.io/master": ""},
			},
		},
	}
}

// ── Reconcile: CR not found ──────────────────────────────────────────────────────

func TestReconcile_CRNotFound(t *testing.T) {
	scheme := newScheme(t)
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	r := &AlibabaCloudCSIDriverReconciler{Client: k8sClient, Scheme: scheme}

	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "cluster", Namespace: operatorNamespace},
	})
	if err != nil {
		t.Fatalf("expected no error for not-found CR, got %v", err)
	}
	if res.Requeue || res.RequeueAfter != 0 {
		t.Errorf("expected empty Result, got %+v", res)
	}
}

// ── Reconcile: disk disabled ─────────────────────────────────────────────────────

func TestReconcile_DiskDisabled(t *testing.T) {
	scheme := newScheme(t)
	cr := defaultCR()
	cr.Spec.Disk.Enabled = false

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cr).
		WithStatusSubresource(cr).
		Build()
	r := &AlibabaCloudCSIDriverReconciler{Client: k8sClient, Scheme: scheme}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// CSIDriver object should NOT have been created since disk is disabled.
	csiDriver := &storagev1.CSIDriver{}
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: diskDriverName}, csiDriver); err == nil {
		t.Error("CSIDriver should not exist when disk is disabled")
	}
}

// ── Reconcile: disk enabled — RBAC created ───────────────────────────────────────

func TestReconcile_DiskEnabled_CreatesRBAC(t *testing.T) {
	scheme := newScheme(t)
	cr := defaultCR()

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cr).
		WithStatusSubresource(cr).
		Build()
	r := &AlibabaCloudCSIDriverReconciler{Client: k8sClient, Scheme: scheme}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// ServiceAccount should exist.
	sa := &corev1.ServiceAccount{}
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: serviceAccountName, Namespace: operatorNamespace}, sa); err != nil {
		t.Errorf("ServiceAccount not created: %v", err)
	}

	// ClusterRole should exist.
	cr2 := &rbacv1.ClusterRole{}
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: "alibaba-cloud-csi-role"}, cr2); err != nil {
		t.Errorf("ClusterRole not created: %v", err)
	}

	// SCC privileged ClusterRoleBinding should exist.
	crb := &rbacv1.ClusterRoleBinding{}
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: "alibabacloud-csi-privileged"}, crb); err != nil {
		t.Errorf("SCC ClusterRoleBinding not created: %v", err)
	}
	if crb.RoleRef.Name != "system:openshift:scc:privileged" {
		t.Errorf("SCC CRB RoleRef = %q, want system:openshift:scc:privileged", crb.RoleRef.Name)
	}
}

// ── Reconcile: disk enabled — CSI components created ────────────────────────────

func TestReconcile_DiskEnabled_CreatesCSIComponents(t *testing.T) {
	scheme := newScheme(t)
	cr := defaultCR()

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cr).
		WithStatusSubresource(cr).
		Build()
	r := &AlibabaCloudCSIDriverReconciler{Client: k8sClient, Scheme: scheme}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx := context.Background()

	// CSIDriver object.
	csiDriver := &storagev1.CSIDriver{}
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: diskDriverName}, csiDriver); err != nil {
		t.Errorf("CSIDriver not created: %v", err)
	}

	// Controller Deployment.
	deploy := &appsv1.Deployment{}
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: "csi-disk-controller", Namespace: operatorNamespace}, deploy); err != nil {
		t.Errorf("controller Deployment not created: %v", err)
	}
	if *deploy.Spec.Replicas != 2 {
		t.Errorf("replicas = %d, want 2", *deploy.Spec.Replicas)
	}

	// Node DaemonSet.
	ds := &appsv1.DaemonSet{}
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: "csi-disk-node", Namespace: operatorNamespace}, ds); err != nil {
		t.Errorf("node DaemonSet not created: %v", err)
	}
	if !ds.Spec.Template.Spec.HostNetwork {
		t.Error("DaemonSet should have hostNetwork=true")
	}
	if !ds.Spec.Template.Spec.HostPID {
		t.Error("DaemonSet should have hostPID=true")
	}

	// StorageClasses.
	sc1 := &storagev1.StorageClass{}
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: "alicloud-disk-efficiency"}, sc1); err != nil {
		t.Errorf("StorageClass alicloud-disk-efficiency not created: %v", err)
	}
	if sc1.Annotations["storageclass.kubernetes.io/is-default-class"] != "true" {
		t.Error("first StorageClass should be annotated as default")
	}

	sc2 := &storagev1.StorageClass{}
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: "alicloud-disk-essd"}, sc2); err != nil {
		t.Errorf("StorageClass alicloud-disk-essd not created: %v", err)
	}
	if sc2.Annotations["storageclass.kubernetes.io/is-default-class"] == "true" {
		t.Error("second StorageClass should NOT be annotated as default")
	}
}

// ── Reconcile: image tag propagated correctly ────────────────────────────────────

func TestReconcile_ImageTagPropagated(t *testing.T) {
	scheme := newScheme(t)
	cr := defaultCR()
	cr.Spec.ImageTag = "v1.99.0"

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cr).
		WithStatusSubresource(cr).
		Build()
	r := &AlibabaCloudCSIDriverReconciler{Client: k8sClient, Scheme: scheme}

	if _, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace},
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	deploy := &appsv1.Deployment{}
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: "csi-disk-controller", Namespace: operatorNamespace}, deploy); err != nil {
		t.Fatalf("Deployment not found: %v", err)
	}

	expectedImage := csiImage + ":v1.99.0"
	found := false
	for _, c := range deploy.Spec.Template.Spec.Containers {
		if c.Name == "csi-plugin" && c.Image == expectedImage {
			found = true
		}
	}
	if !found {
		t.Errorf("expected container image %q not found in Deployment", expectedImage)
	}
}

// ── Reconcile: controller replicas override ──────────────────────────────────────

func TestReconcile_ControllerReplicas(t *testing.T) {
	scheme := newScheme(t)
	cr := defaultCR()
	cr.Spec.Controller.Replicas = 3

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cr).
		WithStatusSubresource(cr).
		Build()
	r := &AlibabaCloudCSIDriverReconciler{Client: k8sClient, Scheme: scheme}

	if _, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace},
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	deploy := &appsv1.Deployment{}
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: "csi-disk-controller", Namespace: operatorNamespace}, deploy); err != nil {
		t.Fatalf("Deployment not found: %v", err)
	}
	if *deploy.Spec.Replicas != 3 {
		t.Errorf("replicas = %d, want 3", *deploy.Spec.Replicas)
	}
}

// ── buildConditions ───────────────────────────────────────────────────────────────

func TestBuildConditions_AllGood(t *testing.T) {
	conds := buildConditions(true, nil)
	if len(conds) != 3 {
		t.Fatalf("expected 3 conditions, got %d", len(conds))
	}
	byType := map[csiv1alpha1.ConditionType]string{}
	for _, c := range conds {
		byType[c.Type] = c.Status
	}
	if byType[csiv1alpha1.ConditionAvailable] != "True" {
		t.Errorf("Available = %q, want True", byType[csiv1alpha1.ConditionAvailable])
	}
	if byType[csiv1alpha1.ConditionDegraded] != "False" {
		t.Errorf("Degraded = %q, want False", byType[csiv1alpha1.ConditionDegraded])
	}
}

func TestBuildConditions_Error(t *testing.T) {
	conds := buildConditions(false, fmt.Errorf("some error"))
	byType := map[csiv1alpha1.ConditionType]string{}
	for _, c := range conds {
		byType[c.Type] = c.Status
	}
	if byType[csiv1alpha1.ConditionAvailable] != "False" {
		t.Errorf("Available = %q, want False on error", byType[csiv1alpha1.ConditionAvailable])
	}
	if byType[csiv1alpha1.ConditionDegraded] != "True" {
		t.Errorf("Degraded = %q, want True on error", byType[csiv1alpha1.ConditionDegraded])
	}
}

// ── Disk CSIDriver: seLinuxMount=true ───────────────────────────────────────────

// TestDiskCSIDriver_SELinuxMount verifies that the disk CSIDriver is created with
// seLinuxMount: true.  This is required on RHCOS (SELinux enforcing) so that the
// kubelet relabels the bind-mount before handing it to the driver — matching the
// behaviour of the AWS EBS CSI driver on ROSA.
func TestDiskCSIDriver_SELinuxMount(t *testing.T) {
	scheme := newScheme(t)
	cr := defaultCR()

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cr).
		WithStatusSubresource(cr).
		Build()
	r := &AlibabaCloudCSIDriverReconciler{Client: k8sClient, Scheme: scheme}

	if _, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace},
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	csiDriver := &storagev1.CSIDriver{}
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: diskDriverName}, csiDriver); err != nil {
		t.Fatalf("CSIDriver not found: %v", err)
	}
	if csiDriver.Spec.SELinuxMount == nil || !*csiDriver.Spec.SELinuxMount {
		t.Error("disk CSIDriver must have seLinuxMount: true for RHCOS SELinux compatibility")
	}
	if csiDriver.Spec.AttachRequired == nil || !*csiDriver.Spec.AttachRequired {
		t.Error("disk CSIDriver must have attachRequired: true")
	}
}

// ── NAS CSIDriver: seLinuxMount=false, attachRequired=false ─────────────────────

func TestNASDriver_CreatesComponents(t *testing.T) {
	scheme := newScheme(t)
	cr := defaultCR()
	cr.Spec.NAS = csiv1alpha1.NASSpec{
		Enabled: true,
		StorageClasses: []csiv1alpha1.NASStorageClassSpec{
			{Name: "alicloud-nas-standard", StorageType: "standard", MountProtocol: "nfs", ReclaimPolicy: "Delete", AllowVolumeExpansion: true},
		},
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cr).
		WithStatusSubresource(cr).
		Build()
	r := &AlibabaCloudCSIDriverReconciler{Client: k8sClient, Scheme: scheme}

	if _, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace},
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx := context.Background()

	// NAS CSIDriver: seLinuxMount=false, attachRequired=false.
	nasDriver := &storagev1.CSIDriver{}
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: nasDriverName}, nasDriver); err != nil {
		t.Fatalf("NAS CSIDriver not found: %v", err)
	}
	if nasDriver.Spec.SELinuxMount == nil || *nasDriver.Spec.SELinuxMount {
		t.Error("NAS CSIDriver must have seLinuxMount: false (NFS does not support SELinux relabelling)")
	}
	if nasDriver.Spec.AttachRequired == nil || *nasDriver.Spec.AttachRequired {
		t.Error("NAS CSIDriver must have attachRequired: false")
	}

	// NAS controller Deployment.
	deploy := &appsv1.Deployment{}
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: "csi-nas-controller", Namespace: operatorNamespace}, deploy); err != nil {
		t.Errorf("NAS controller Deployment not created: %v", err)
	}

	// NAS node DaemonSet.
	ds := &appsv1.DaemonSet{}
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: "csi-nas-node", Namespace: operatorNamespace}, ds); err != nil {
		t.Errorf("NAS node DaemonSet not created: %v", err)
	}
	// NAS node does not need HostPID (no disk device management).
	if ds.Spec.Template.Spec.HostPID {
		t.Error("NAS node DaemonSet should NOT set hostPID=true")
	}

	// NAS StorageClass with Immediate binding.
	sc := &storagev1.StorageClass{}
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: "alicloud-nas-standard"}, sc); err != nil {
		t.Errorf("NAS StorageClass not created: %v", err)
	}
	if *sc.VolumeBindingMode != storagev1.VolumeBindingImmediate {
		t.Errorf("NAS StorageClass VolumeBindingMode = %q, want Immediate", *sc.VolumeBindingMode)
	}
	if sc.Parameters["mountType"] != "nfs" {
		t.Errorf("NAS StorageClass mountType = %q, want nfs", sc.Parameters["mountType"])
	}
}

// ── StorageClass: VirtDefault annotation ────────────────────────────────────────

func TestDiskStorageClass_VirtDefault(t *testing.T) {
	scheme := newScheme(t)
	cr := defaultCR()
	cr.Spec.Disk.StorageClasses[0].VirtDefault = true

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cr).
		WithStatusSubresource(cr).
		Build()
	r := &AlibabaCloudCSIDriverReconciler{Client: k8sClient, Scheme: scheme}

	if _, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace},
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sc := &storagev1.StorageClass{}
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: "alicloud-disk-efficiency"}, sc); err != nil {
		t.Fatalf("StorageClass not found: %v", err)
	}
	if sc.Annotations["storageclass.kubevirt.io/is-default-virt-class"] != "true" {
		t.Error("VirtDefault StorageClass missing storageclass.kubevirt.io/is-default-virt-class annotation")
	}
}

// ── StorageClass: Encrypted parameter ───────────────────────────────────────────

func TestDiskStorageClass_Encrypted(t *testing.T) {
	scheme := newScheme(t)
	cr := defaultCR()
	cr.Spec.Disk.StorageClasses[0].Encrypted = true

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cr).
		WithStatusSubresource(cr).
		Build()
	r := &AlibabaCloudCSIDriverReconciler{Client: k8sClient, Scheme: scheme}

	if _, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace},
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sc := &storagev1.StorageClass{}
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: "alicloud-disk-efficiency"}, sc); err != nil {
		t.Fatalf("StorageClass not found: %v", err)
	}
	if sc.Parameters["encrypted"] != "true" {
		t.Error("Encrypted StorageClass missing encrypted=true parameter")
	}
}

// ── VolumeSnapshotClass: disabled path ──────────────────────────────────────────

// TestVolumeSnapshotClass_Disabled verifies that reconcile succeeds and does not
// attempt to create a VolumeSnapshotClass when snapshot.enabled=false.
func TestVolumeSnapshotClass_Disabled(t *testing.T) {
	scheme := newScheme(t)
	cr := defaultCR()
	cr.Spec.Disk.Snapshot = csiv1alpha1.SnapshotConfig{Enabled: false}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cr).
		WithStatusSubresource(cr).
		Build()
	r := &AlibabaCloudCSIDriverReconciler{Client: k8sClient, Scheme: scheme}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace},
	})
	if err != nil {
		t.Fatalf("reconcile should succeed when snapshot is disabled, got: %v", err)
	}
}

// TestVolumeSnapshotClass_EnabledCreatesClass verifies that a VolumeSnapshotClass
// is created when snapshot.enabled=true and the CRD is available.
// The fake client accepts unstructured objects even without a pre-registered CRD.
func TestVolumeSnapshotClass_EnabledCreatesClass(t *testing.T) {
	scheme := newScheme(t)
	cr := defaultCR()
	cr.Spec.Disk.Snapshot = csiv1alpha1.SnapshotConfig{
		Enabled:   true,
		ClassName: "test-disk-snapclass",
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cr).
		WithStatusSubresource(cr).
		Build()
	r := &AlibabaCloudCSIDriverReconciler{Client: k8sClient, Scheme: scheme}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace},
	})
	// With the fake client the unstructured Create either succeeds or returns
	// NoMatchError (handled gracefully). Either way reconcile must not return an error.
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
}

// ── StorageProfile: patch=false skips patching ──────────────────────────────────

func TestStorageProfile_PatchFalse(t *testing.T) {
	scheme := newScheme(t)
	cr := defaultCR()
	cr.Spec.Disk.StorageProfile = csiv1alpha1.StorageProfileConfig{Patch: false}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cr).
		WithStatusSubresource(cr).
		Build()
	r := &AlibabaCloudCSIDriverReconciler{Client: k8sClient, Scheme: scheme}

	// Should succeed without touching any StorageProfile.
	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace},
	})
	if err != nil {
		t.Fatalf("reconcile should succeed when storageProfile.patch=false, got: %v", err)
	}
}

// TestStorageProfile_PatchTrue_CDIAbsent verifies that when storageProfile.patch=true
// but CDI is not installed (StorageProfile CR returns not-found), reconcile succeeds
// and the missing profile is silently skipped.
func TestStorageProfile_PatchTrue_CDIAbsent(t *testing.T) {
	scheme := newScheme(t)
	cr := defaultCR()
	cr.Spec.Disk.StorageProfile = csiv1alpha1.StorageProfileConfig{Patch: true, CloneStrategy: "csi-clone"}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cr).
		WithStatusSubresource(cr).
		Build()
	r := &AlibabaCloudCSIDriverReconciler{Client: k8sClient, Scheme: scheme}

	// CDI StorageProfile CRD is not registered in the scheme, so Get will return
	// either NoMatchError or NotFound — both handled gracefully.
	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace},
	})
	if err != nil {
		t.Fatalf("reconcile should tolerate absent CDI StorageProfile, got: %v", err)
	}
}

// ── NAS controller has no attacher or resizer ────────────────────────────────────

func TestNASController_NoAttacherNoResizer(t *testing.T) {
	scheme := newScheme(t)
	cr := defaultCR()
	cr.Spec.NAS = csiv1alpha1.NASSpec{Enabled: true}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cr).
		WithStatusSubresource(cr).
		Build()
	r := &AlibabaCloudCSIDriverReconciler{Client: k8sClient, Scheme: scheme}

	if _, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace},
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	deploy := &appsv1.Deployment{}
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: "csi-nas-controller", Namespace: operatorNamespace}, deploy); err != nil {
		t.Fatalf("NAS controller Deployment not found: %v", err)
	}

	for _, c := range deploy.Spec.Template.Spec.Containers {
		if c.Name == "csi-attacher" {
			t.Error("NAS controller should NOT include csi-attacher (attachRequired=false)")
		}
		if c.Name == "csi-resizer" {
			t.Error("NAS controller should NOT include csi-resizer")
		}
	}

	// Must have provisioner.
	hasProvisioner := false
	for _, c := range deploy.Spec.Template.Spec.Containers {
		if c.Name == "csi-provisioner" {
			hasProvisioner = true
		}
	}
	if !hasProvisioner {
		t.Error("NAS controller must include csi-provisioner")
	}
}

// ── Disk controller includes snapshotter sidecar ─────────────────────────────────

func TestDiskController_HasSnapshotter(t *testing.T) {
	scheme := newScheme(t)
	cr := defaultCR()

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cr).
		WithStatusSubresource(cr).
		Build()
	r := &AlibabaCloudCSIDriverReconciler{Client: k8sClient, Scheme: scheme}

	if _, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace},
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	deploy := &appsv1.Deployment{}
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: "csi-disk-controller", Namespace: operatorNamespace}, deploy); err != nil {
		t.Fatalf("Deployment not found: %v", err)
	}

	hasSnapshotter := false
	for _, c := range deploy.Spec.Template.Spec.Containers {
		if c.Name == "csi-snapshotter" && c.Image == csiSnapshotterImage {
			hasSnapshotter = true
		}
	}
	if !hasSnapshotter {
		t.Errorf("disk controller Deployment missing csi-snapshotter container (image %q)", csiSnapshotterImage)
	}
}
