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

// EosClusterSpec defines the desired state of EosCluster
type EosClusterSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Host must be a host string, a host:port pair, or a URL to the base of the apiserver.
	Host         string   `json:"host,omitempty"`
	Version      string   `json:"version,omitempty"`
	Nodes        int      `json:"nodes,omitempty"`
	Architecture string   `json:"architecture,omitempty"`
	Status       string   `json:"status,omitempty"`
	Type         string   `json:"type,omitempty"`
	Projects     []string `json:"projects"`
}

// EosClusterStatus defines the observed state of EosCluster
type EosClusterStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
}

// +kubebuilder:object:root=true

// EosCluster is the Schema for the eosclusters API
type EosCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   EosClusterSpec   `json:"spec,omitempty"`
	Status EosClusterStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// EosClusterList contains a list of EosCluster
type EosClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []EosCluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(&EosCluster{}, &EosClusterList{})
}
