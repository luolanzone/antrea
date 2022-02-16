/*
Copyright 2021 Antrea Authors.

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
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TunnelEndpointSpec defines the desired state of TunnelEndpoint
type TunnelEndpointSpec struct {
	// Role (leader/member) shows the role of Gateway node if HA is enabled
	Role string `json:"role,omitempty"`
	// ClusterID of the member cluster.
	ClusterID string `json:"clusterID,omitempty"`
	//Hostname of Gateway node
	Hostname string `json:"hostname,omitempty"`
	// Subnets used by the member cluster.
	Subnets []string `json:"subnets,omitempty"`
	// PrivateIP of Gateway node
	PrivateIP string `json:"privateIP,omitempty"`
	// PublicIP of Gateway node
	PublicIP string `json:"publicIP,omitempty"`
}

type TunnelEndpointConditionType string

const (
	TunnelEndpointReady TunnelEndpointConditionType = "Ready"
)

// TunnelEndpointCondition indicates the readiness condition of a TunnelEndpoint.
type TunnelEndpointCondition struct {
	// Type is the type of the condition.
	Type TunnelEndpointConditionType `json:"type,omitempty"`
	// Status of the condition, one of True, False, Unknown.
	Status v1.ConditionStatus `json:"status,omitempty"`

	// +optional
	// Last time the condition transited from one status to another.
	LastTransitionTime metav1.Time `json:"lastTransitionTime,omitempty"`
	// +optional
	// A human readable message indicating details about the transition.
	Message string `json:"message,omitempty"`
	// +optional
	// Unique, one-word, CamelCase reason for the condition's last transition.
	Reason string `json:"reason,omitempty"`
}

// TunnelEndpointStatus defines the observed state of TunnelEndpoint
type TunnelEndpointStatus struct {
	Conditions []TunnelEndpointCondition `json:"conditions,omitempty"`
}

// +genclient
//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// TunnelEndpoint is the Schema for the tunnelendpoints API
type TunnelEndpoint struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   TunnelEndpointSpec   `json:"spec,omitempty"`
	Status TunnelEndpointStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// TunnelEndpointList contains a list of TunnelEndpoint
type TunnelEndpointList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []TunnelEndpoint `json:"items"`
}

func init() {
	SchemeBuilder.Register(&TunnelEndpoint{}, &TunnelEndpointList{})
}
