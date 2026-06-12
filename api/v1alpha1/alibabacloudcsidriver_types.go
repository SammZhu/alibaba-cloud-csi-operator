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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DiskStorageClassSpec defines a StorageClass to be created for disk volumes.
type DiskStorageClassSpec struct {
	// Name is the StorageClass name, e.g. alicloud-disk-efficiency.
	Name string `json:"name"`
	// Type is the Alibaba Cloud disk type, e.g. cloud_efficiency, cloud_essd.
	Type string `json:"type"`
	// ReclaimPolicy controls what happens to the PV when the PVC is deleted. Defaults to Delete.
	// +kubebuilder:default=Delete
	// +kubebuilder:validation:Enum=Delete;Retain
	ReclaimPolicy string `json:"reclaimPolicy,omitempty"`
	// AllowVolumeExpansion enables online volume expansion. Defaults to true.
	// +kubebuilder:default=true
	AllowVolumeExpansion bool `json:"allowVolumeExpansion,omitempty"`
	// VirtDefault annotates this StorageClass as the default for OpenShift Virtualization (OKV/KubeVirt).
	// Sets the storageclass.kubevirt.io/is-default-virt-class: "true" annotation.
	// +optional
	VirtDefault bool `json:"virtDefault,omitempty"`
	// Encrypted enables server-side disk encryption at rest.
	// +optional
	Encrypted bool `json:"encrypted,omitempty"`
	// VolumeMode is the volume mode advertised to OpenShift Virtualization via the
	// CDI StorageProfile claimPropertySets for this StorageClass:
	//   Block       — raw block device, best VM OS-disk I/O (no filesystem layer), RWO
	//   Filesystem  — general container workloads, RWO (default)
	// This only affects the StorageProfile the operator patches; StorageClass objects
	// themselves carry no volumeMode. A general-purpose disk class should stay
	// Filesystem; a VM OS-disk class (virtDefault) should be Block.
	// +kubebuilder:default=Filesystem
	// +kubebuilder:validation:Enum=Block;Filesystem
	// +optional
	VolumeMode string `json:"volumeMode,omitempty"`
}

// SnapshotConfig configures VolumeSnapshot support for the disk CSI driver.
type SnapshotConfig struct {
	// Enabled controls whether a VolumeSnapshotClass is created. Requires the
	// snapshot.storage.k8s.io CRDs to be installed on the cluster.
	// +kubebuilder:default=false
	Enabled bool `json:"enabled,omitempty"`
	// ClassName is the name of the VolumeSnapshotClass to create.
	// Defaults to "alibaba-cloud-disk-snapclass".
	// +optional
	ClassName string `json:"className,omitempty"`
}

// StorageProfileConfig controls automatic patching of CDI StorageProfiles
// for OpenShift Virtualization compatibility.
type StorageProfileConfig struct {
	// Patch enables automatic patching of the CDI StorageProfile object for
	// this StorageClass. When true the operator sets the recommended
	// claimPropertySets and cloneStrategy so that KubeVirt DataVolumes work
	// correctly without manual intervention. The patch is skipped silently
	// if CDI is not installed.
	// +kubebuilder:default=false
	Patch bool `json:"patch,omitempty"`
	// CloneStrategy specifies the CDI clone strategy. Defaults to "csi-clone".
	// Valid values match CDICloneStrategy in the CDI API (cdi.kubevirt.io/v1beta1):
	//   csi-clone    — zero-copy CSI volume clone (fastest, preferred)
	//   snapshot     — VolumeSnapshot-based copy (requires VolumeSnapshotClass)
	//   copy         — host-assisted data copy through a pod (slowest, always works)
	// +kubebuilder:validation:Enum=csi-clone;snapshot;copy
	// +optional
	CloneStrategy string `json:"cloneStrategy,omitempty"`
}

// DiskSpec configures the Alibaba Cloud Disk (EBS) CSI driver.
type DiskSpec struct {
	// Enabled controls whether the disk CSI driver components are deployed.
	// +kubebuilder:default=true
	Enabled bool `json:"enabled"`
	// DefaultStorageClass marks the first StorageClass in the list as the cluster default.
	// +kubebuilder:default=true
	DefaultStorageClass bool `json:"defaultStorageClass,omitempty"`
	// StorageClasses is the list of StorageClass objects to create.
	// +kubebuilder:validation:MinItems=1
	StorageClasses []DiskStorageClassSpec `json:"storageClasses,omitempty"`
	// Snapshot configures VolumeSnapshot support. Requires snapshot.storage.k8s.io CRDs.
	// +optional
	Snapshot SnapshotConfig `json:"snapshot,omitempty"`
	// StorageProfile configures automatic CDI StorageProfile patching for OKV compatibility.
	// +optional
	StorageProfile StorageProfileConfig `json:"storageProfile,omitempty"`
}

// NASStorageClassSpec defines a StorageClass to be created for NAS volumes.
type NASStorageClassSpec struct {
	// Name is the StorageClass name, e.g. alicloud-nas-standard.
	Name string `json:"name"`
	// VolumeAs selects the NAS dynamic-provisioning mode:
	//   "filesystem" (default) — the driver creates a NAS filesystem + mount target
	//     per PV. Requires the cluster network so the mount target is reachable by the
	//     nodes: set RegionID/ZoneID/VpcID/VSwitchID (and FileSystemType/StorageType).
	//   "subpath" — the driver carves subpaths out of a PRE-EXISTING NAS filesystem;
	//     requires Server (the mount-target endpoint).
	// Leaving this unset and providing no Server makes the upstream driver default to
	// subpath mode with no server, which leaves every PVC Pending — hence the
	// filesystem default here.
	// +kubebuilder:default=filesystem
	// +kubebuilder:validation:Enum=filesystem;subpath
	VolumeAs string `json:"volumeAs,omitempty"`
	// StorageType is the NAS storage type, e.g. Capacity, Performance.
	// +optional
	StorageType string `json:"storageType,omitempty"`
	// FileSystemType is the NAS filesystem type for filesystem mode:
	//   "standard" (general-purpose, default) or "extreme".
	// +kubebuilder:default=standard
	// +kubebuilder:validation:Enum=standard;extreme
	FileSystemType string `json:"fileSystemType,omitempty"`
	// RegionID is the region the NAS filesystem is created in (filesystem mode).
	// +optional
	RegionID string `json:"regionId,omitempty"`
	// ZoneID is the zone the NAS mount target is created in (filesystem mode). Pick a
	// zone where the worker nodes run so the mount target is in-zone.
	// +optional
	ZoneID string `json:"zoneId,omitempty"`
	// VpcID is the VPC the NAS mount target belongs to (filesystem mode).
	// +optional
	VpcID string `json:"vpcId,omitempty"`
	// VSwitchID is the vSwitch the NAS mount target is placed in (filesystem mode).
	// +optional
	VSwitchID string `json:"vSwitchId,omitempty"`
	// Server is the NAS mount-target endpoint for subpath mode
	// (e.g. "0123abcd-xyz.cn-hangzhou.nas.aliyuncs.com:/"). Required when VolumeAs=subpath.
	// +optional
	Server string `json:"server,omitempty"`
	// MountProtocol is the mount protocol, either "nfs" (default) or "cnfs".
	// +kubebuilder:default=nfs
	// +kubebuilder:validation:Enum=nfs;cnfs
	MountProtocol string `json:"mountProtocol,omitempty"`
	// ReclaimPolicy controls what happens to the PV when the PVC is deleted. Defaults to Delete.
	// +kubebuilder:default=Delete
	// +kubebuilder:validation:Enum=Delete;Retain
	ReclaimPolicy string `json:"reclaimPolicy,omitempty"`
	// AllowVolumeExpansion enables online volume expansion. Defaults to true.
	// +kubebuilder:default=true
	AllowVolumeExpansion bool `json:"allowVolumeExpansion,omitempty"`
	// VirtDefault annotates this StorageClass as the default for OpenShift Virtualization (OKV/KubeVirt).
	// NAS provides ReadWriteMany required for live VM migration.
	// +optional
	VirtDefault bool `json:"virtDefault,omitempty"`
}

// NASSpec configures the Alibaba Cloud NAS (file storage) CSI driver.
type NASSpec struct {
	// Enabled controls whether the NAS CSI driver components are deployed.
	// +kubebuilder:default=false
	Enabled bool `json:"enabled"`
	// StorageClasses is the list of NAS StorageClass objects to create.
	// +optional
	StorageClasses []NASStorageClassSpec `json:"storageClasses,omitempty"`
	// StorageProfile configures automatic CDI StorageProfile patching for OKV compatibility.
	// NAS profiles use ReadWriteMany + Filesystem to support live VM migration.
	// +optional
	StorageProfile StorageProfileConfig `json:"storageProfile,omitempty"`
}

// OSSSpec configures the Alibaba Cloud OSS (object storage) CSI driver.
type OSSSpec struct {
	// Enabled controls whether the OSS CSI driver components are deployed.
	// +kubebuilder:default=false
	Enabled bool `json:"enabled"`
}

// AuthSpec configures the authentication method for the CSI driver to call Alibaba Cloud APIs.
type AuthSpec struct {
	// RAMToken specifies the ECS instance metadata token version used for RAM Role auth.
	// Use "v2" (recommended, IMDSv2 with TTL) or "v1".
	// +kubebuilder:default=v2
	// +kubebuilder:validation:Enum=v1;v2
	RAMToken string `json:"ramToken,omitempty"`
}

// ControllerSpec configures the CSI controller Deployment scheduling.
type ControllerSpec struct {
	// Replicas is the number of CSI controller Pod replicas. Defaults to 2 for HA.
	// +kubebuilder:default=2
	// +kubebuilder:validation:Minimum=1
	Replicas int32 `json:"replicas,omitempty"`
	// NodeSelector pins the controller Pods to specific nodes (typically control-plane nodes).
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`
	// Tolerations allow the controller Pods to be scheduled on tainted nodes.
	// +optional
	Tolerations []TolerationSpec `json:"tolerations,omitempty"`
}

// TolerationSpec mirrors a subset of corev1.Toleration for CRD compatibility.
type TolerationSpec struct {
	// Key is the taint key the toleration applies to.
	Key string `json:"key,omitempty"`
	// Operator is the relationship between the key and value. Defaults to Equal.
	// +kubebuilder:validation:Enum=Exists;Equal
	Operator string `json:"operator,omitempty"`
	// Value is the taint value the toleration matches (only for operator Equal).
	Value string `json:"value,omitempty"`
	// Effect is the taint effect to tolerate.
	// +kubebuilder:validation:Enum=NoSchedule;PreferNoSchedule;NoExecute
	Effect string `json:"effect,omitempty"`
}

// AlibabaCloudCSIDriverSpec defines the desired state of AlibabaCloudCSIDriver.
type AlibabaCloudCSIDriverSpec struct {
	// Disk configures the Alibaba Cloud Disk (EBS/cloud disk) CSI driver.
	// +optional
	Disk DiskSpec `json:"disk,omitempty"`

	// NAS configures the Alibaba Cloud NAS file-storage CSI driver.
	// NAS provides ReadWriteMany volumes required for OpenShift Virtualization live migration.
	// +optional
	NAS NASSpec `json:"nas,omitempty"`

	// OSS configures the Alibaba Cloud OSS object-storage CSI driver. Disabled by default (Phase 3).
	// +optional
	OSS OSSSpec `json:"oss,omitempty"`

	// ImageTag is the upstream alibaba-cloud-csi-driver image tag to deploy, e.g. v1.35.3.
	// +kubebuilder:default="v1.35.3"
	ImageTag string `json:"imageTag,omitempty"`

	// Auth configures how the CSI driver authenticates to Alibaba Cloud APIs.
	// +optional
	Auth AuthSpec `json:"auth,omitempty"`

	// Controller configures the CSI controller Deployment (scheduling, replicas).
	// +optional
	Controller ControllerSpec `json:"controller,omitempty"`
}

// ConditionType represents a CSI driver condition.
type ConditionType string

const (
	// ConditionAvailable indicates all managed components are deployed and healthy.
	ConditionAvailable ConditionType = "Available"
	// ConditionProgressing indicates the operator is actively reconciling components.
	ConditionProgressing ConditionType = "Progressing"
	// ConditionDegraded indicates one or more components have failed.
	ConditionDegraded ConditionType = "Degraded"
)

// CSIDriverCondition describes the current state of a CSI driver component.
type CSIDriverCondition struct {
	// Type is the condition type.
	Type ConditionType `json:"type"`
	// Status is True, False, or Unknown.
	// +kubebuilder:validation:Enum=True;False;Unknown
	Status string `json:"status"`
	// Reason is a short CamelCase string describing the reason for the condition.
	// +optional
	Reason string `json:"reason,omitempty"`
	// Message is a human-readable description.
	// +optional
	Message string `json:"message,omitempty"`
	// LastTransitionTime is when the condition last changed.
	// +optional
	LastTransitionTime metav1.Time `json:"lastTransitionTime,omitempty"`
}

// AlibabaCloudCSIDriverStatus defines the observed state of AlibabaCloudCSIDriver.
type AlibabaCloudCSIDriverStatus struct {
	// ObservedGeneration is the generation of the spec that was last reconciled.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions contains the current conditions of the CSI driver deployment.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []CSIDriverCondition `json:"conditions,omitempty"`

	// DiskDriverReady is true when the Disk CSI controller and node DaemonSet are running.
	// +optional
	DiskDriverReady bool `json:"diskDriverReady,omitempty"`

	// NASDriverReady is true when the NAS CSI driver components are running.
	// +optional
	NASDriverReady bool `json:"nasDriverReady,omitempty"`

	// OSSDriverReady is true when the OSS CSI driver components are running.
	// +optional
	OSSDriverReady bool `json:"ossDriverReady,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=alicsid
// +kubebuilder:printcolumn:name="DiskReady",type=boolean,JSONPath=`.status.diskDriverReady`
// +kubebuilder:printcolumn:name="NASReady",type=boolean,JSONPath=`.status.nasDriverReady`
// +kubebuilder:printcolumn:name="Available",type=string,JSONPath=`.status.conditions[?(@.type=='Available')].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// AlibabaCloudCSIDriver is the Schema for the alibabacloudcsidrivers API.
type AlibabaCloudCSIDriver struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AlibabaCloudCSIDriverSpec   `json:"spec,omitempty"`
	Status AlibabaCloudCSIDriverStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AlibabaCloudCSIDriverList contains a list of AlibabaCloudCSIDriver.
type AlibabaCloudCSIDriverList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AlibabaCloudCSIDriver `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AlibabaCloudCSIDriver{}, &AlibabaCloudCSIDriverList{})
}
