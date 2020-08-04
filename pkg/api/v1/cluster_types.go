/*
Copyright 2020 EasyStack Container Team.

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
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// ClusterSpec defines the desired state of Cluster
type ClusterSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Host must be a host string, a host:port pair, or a URL to the base of the apiserver.
	Host         string   `json:"host,omitempty"`
	Version      string   `json:"version,omitempty"`
	Nodes        int      `json:"nodes_count,omitempty"`
	Architecture string   `json:"architecture,omitempty"`
	Type         string   `json:"type,omitempty"`
	ClusterID    string   `json:"clusterid,omitempty"`
	Projects     []string `json:"projects,omitempty"`
	Eks          EksSpec  `json:"eks,omitempty"`
}

type EksSpec struct {
	EksStatus    string `json:"eks_status,omitempty"`
	EksClusterID string `json:"eks_clusterid,omitempty"`
	EksName      string `json:"eks_name,omitempty"`
	APIAddress   string `json:"api_address,omitempty"`
}

// ClusterStatus defines the observed state of Cluster
type ClusterStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	ClusterStatus       string   `json:"cluster_status,omitempty"`
	Nodes               []Node   `json:"nodes,omitempty"`
	ClusterStatusReason []string `json:"cluster_status_reason,omitempty"`

	// when controller restart,we only set the cluster which .status.needreconcile
	// is true to cache, others need to reenter controller cycle to make sure some
	// action in cluster is finished
	HasReconciledOnce bool `json:"has_reconciled_once,omitempty"`
}

type Node struct {
	Name      string          `json:"node_name,omitempty"`
	Role      string          `json:"node_role,omitempty"`
	Status    string          `json:"node_status,omitempty"`
	Component ComponentStatus `json:"component_status,omitempty"`
}

type ComponentStatus struct {
	ApiserverStatus         string `json:"apiserver_status,omitempty"`
	ControllerManagerStatus string `json:"controller_manager_status,omitempty"`
	SchedulerStatus         string `json:"scheduler_status,omitempty"`
	EtcdStatus              string `json:"etcd_status,omitempty"`
}

// +kubebuilder:object:root=true

// Cluster is the Schema for the clusters API
type Cluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ClusterSpec   `json:"spec,omitempty"`
	Status ClusterStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ClusterList contains a list of Cluster
type ClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Cluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Cluster{}, &ClusterList{})
}
