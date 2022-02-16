/*
Copyright 2022 Antrea Authors.

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

type TunnelEndpointImportConditionType string

const (
	// TunnelEndpointImportReady indicates TunnelEndpoint is imported successfully.
	TunnelEndpointImportReady TunnelEndpointImportConditionType = "Ready"
	// TunnelEndpointImportTunnelReady means tunnel between clusters is created successfully.
	TunnelEndpointImportTunnelReady TunnelEndpointImportConditionType = "TunnelReady"
)

// TunnelEndpointImportCondition indicates the readiness condition of a TunnelEndpoint.
type TunnelEndpointImportCondition struct {
	// Type is the type of the condition.
	Type TunnelEndpointImportConditionType `json:"type,omitempty"`
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

// TunnelEndpointImportStatus defines the observed state of TunnelEndpointImport
type TunnelEndpointImportStatus struct {
	Conditions []TunnelEndpointImportCondition `json:"conditions,omitempty"`
}

// +genclient
//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// TunnelEndpointImport is the Schema for the tunnelendpointimports API
type TunnelEndpointImport struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   TunnelEndpointSpec         `json:"spec,omitempty"`
	Status TunnelEndpointImportStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// TunnelEndpointImportList contains a list of TunnelEndpointImport
type TunnelEndpointImportList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []TunnelEndpointImport `json:"items"`
}

func init() {
	SchemeBuilder.Register(&TunnelEndpointImport{}, &TunnelEndpointImportList{})
}
