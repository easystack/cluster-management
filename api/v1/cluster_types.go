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

package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// ClusterSpec defines the desired state of Cluster
type ClusterSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file
}

// ResourceUsage describes the resource usage, resolved from the OpenStack flavor.
type ResourceUsage struct {
	// cpu is the number of vCPUs, resolved from the OpenStack flavor.
	// +optional
	CPU int64 `json:"cpu,omitempty"`

	// memory is the memory size in MiB, resolved from the OpenStack flavor.
	// +optional
	Memory int64 `json:"memory,omitempty"`
}

// NodeInfo describes the information of one node (CAPI Machine) of the cluster.
type NodeInfo struct {
	// name is the name of the node (the name of the CAPI Machine).
	Name string `json:"name"`

	// flavor is the OpenStack flavor used by the node, e.g. 4C-8G.
	// +optional
	Flavor string `json:"flavor,omitempty"`

	// resourceUsage is the resource usage of the node, resolved from the OpenStack flavor.
	// +optional
	ResourceUsage ResourceUsage `json:"resourceUsage,omitempty"`
}

// ClusterInfo describes the aggregated information of the cluster.
type ClusterInfo struct {
	// nodes is the total number of nodes.
	// +optional
	Nodes int64 `json:"nodes,omitempty"`

	// resourceUsage is the total resource usage of all nodes of the cluster.
	// +optional
	ResourceUsage ResourceUsage `json:"resourceUsage,omitempty"`
}

// ClusterStatus defines the observed state of Cluster.
type ClusterStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// For Kubernetes API conventions, see:
	// https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#typical-status-properties

	// nodeInfo records the information and resource usage of all nodes of the cluster.
	// +listType=map
	// +listMapKey=name
	// +optional
	NodeInfo []NodeInfo `json:"nodeInfo,omitempty"`

	// clusterInfo records the aggregated information and total resource usage of the cluster.
	// +optional
	ClusterInfo ClusterInfo `json:"clusterInfo,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// Cluster is the Schema for the clusters API
type Cluster struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of Cluster
	// +required
	Spec ClusterSpec `json:"spec"`

	// status defines the observed state of Cluster
	// +optional
	Status ClusterStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// ClusterList contains a list of Cluster
type ClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Cluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &Cluster{}, &ClusterList{})
		return nil
	})
}
