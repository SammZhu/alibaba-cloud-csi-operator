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
