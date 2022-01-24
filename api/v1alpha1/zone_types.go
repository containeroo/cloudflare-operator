/*
Copyright 2022.

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

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// ZoneSpec defines the desired state of Zone
type ZoneSpec struct {
	// Name of the zone
	Name string `json:"name"`
	// ID of the zone
	ID string `json:"id"`
	//+optional
	DefaultSettings DNSRecordSettings `json:"default_settings"`
}

// ZoneStatus defines the observed state of Zone
type ZoneStatus struct {
	//+kubebuilder:validation:Enum=Active;Pending;Failed
	Phase   string `json:"phase"`
	Message string `json:"message,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:resource:scope=Cluster

// Zone is the Schema for the zones API
//+kubebuilder:printcolumn:name="Zone Name",type="string",JSONPath=".spec.name"
//+kubebuilder:printcolumn:name="ID",type="string",JSONPath=".spec.id"
//+kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
type Zone struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ZoneSpec   `json:"spec,omitempty"`
	Status ZoneStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// ZoneList contains a list of Zone
type ZoneList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Zone `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Zone{}, &ZoneList{})
}
