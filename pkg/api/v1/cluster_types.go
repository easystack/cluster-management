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
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type ClusterType string
type ClusterStat string
type NodeRole string
type NodeStat string

const (
	ClusterEKS  ClusterType = "EKS"
	ClusterEHOS ClusterType = "EHOS"
	ClusterEOS  ClusterType = "EOS"
)

const (
	NodeStatReady    NodeStat = "Ready"
	NodeStatNotReady NodeStat = "NotReady"
)

const (
	NodeRoleMaster NodeRole = "master"
	NodeRoleWorker NodeRole = "node"
)

const (
	// all nodes are ready and all components are healthy, we
	// think the cluster is healthy
	ClusterHealthy ClusterStat = "Healthy"

	// if some node is notready or some component like apiserver,
	// controller-manager is unhealthy, we think this cluster
	// is unhealthy
	ClusterWarning ClusterStat = "Warning"

	// if we can not connnect to cluster because some reason
	// we will set cluster cr status to DISCONNECTED
	ClusterDisConnected ClusterStat = "Disconnected"

	// if some of nodes violate the License
	// we will set cluster cr status to Unauthorized
	ClusterUnauthorized ClusterStat = "Unauthorized"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// ClusterSpec defines the desired state of Cluster
type ClusterSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make generate" to regenerate code after modifying this file

	// Host must be a host string, a host:port pair, or a URL to the base of the apiserver.
	Host         string      `json:"host,omitempty"`
	PublicVip    string      `json:"public_vip,omitempty"`
	Version      string      `json:"version,omitempty"`
	Nodes        int         `json:"nodes_count,omitempty"`
	Architecture string      `json:"architecture,omitempty"`
	Type         ClusterType `json:"type,omitempty"`
	ClusterID    string      `json:"clusterid,omitempty"`
	Projects     []string    `json:"projects,omitempty"`
	Eks          EksSpec     `json:"eks,omitempty"`
}

type EksSpec struct {
	EksName           string            `json:"eks_name,omitempty"`
	EksClusterID      string            `json:"eks_clusterid,omitempty"`
	APIAddress        string            `json:"api_address,omitempty"`
	EksStatus         string            `json:"eks_status,omitempty"`
	EksReason         string            `json:"eks_reason,omitempty"`
	EksFaults         map[string]string `json:"eks_faults,omitempty"`
	EksStackID        string            `json:"eks_stackid,omitempty"`
	CreationTimestamp int64             `json:"eks_creation_timestamp,omitempty"`
	EksHealthReasons  map[string]string `json:"eks_health_reasons,omitempty"`

	CVersion string `json:"-"`
	Hadsync  bool   `json:"-"`
	// ClusterNotFoundTimestamp record first time cluster is not found (404)
	ClusterNotFoundTimestamp time.Time `json:"-"`
}

// ClusterStatus defines the observed state of Cluster
type ClusterStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make generate" to regenerate code after modifying this file
	ClusterStatus       ClusterStat         `json:"cluster_status,omitempty"`
	ClusterInfo         ClusterInfo         `json:"clusterInfo,omitempty"`
	Nodes               []*Node             `json:"nodes,omitempty"`
	ClusterStatusReason ClusterStatusReason `json:"cluster_status_reason,omitempty"`

	Conditions []*Condition `json:"conditions,omitempty"`
}

type ClusterStatusReason struct {
	StatusReason string   `json:"status_reason,omitempty"`
	Faults       []string `json:"faults,omitempty"`
}

type Condition struct {
	LastUpdateTime string `json:"lastUpdateTime,omitempty"`
	Type           string `json:"type,omitempty"`
	Reason         string `json:"reason,omitempty"`
}

type ClusterInfo struct {
	FloatingIP string `json:"floatingIP,omitempty"`
}

type Node struct {
	Name       string          `json:"node_name,omitempty"`
	Role       NodeRole        `json:"node_role,omitempty"`
	Status     NodeStat        `json:"node_status,omitempty"`
	Version    string          `json:"version,omitempty"`
	Arch       string          `json:"arch,omitempty"`
	InternalIP string          `json:"internalIP,omitempty"`
	ExternalIP string          `json:"externalIP,omitempty"`
	Capacity   v1.ResourceList `json:"capacity,omitempty"`
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
