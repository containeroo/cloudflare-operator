/*
Copyright 2025 containeroo

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

// ZoneSpec defines the desired state of Zone
type ZoneSpec struct {
	// Name of the zone
	Name string `json:"name"`
	// Prune determines whether DNS records in the zone that are not managed by cloudflare-operator should be automatically removed
	// +kubebuilder:default=false
	// +optional
	Prune bool `json:"prune"`
	// Interval to check zone status
	// +kubebuilder:default="5m"
	// +optional
	Interval metav1.Duration `json:"interval,omitempty"`
}

// ZoneStatus defines the observed state of Zone
type ZoneStatus struct {
	// ID of the zone
	// +optional
	ID string `json:"id,omitempty"`
	// Conditions contains the different condition statuses for the Zone object.
	// +optional
	Conditions []metav1.Condition `json:"conditions"`
}

const (
	ZoneNameIndexKey string = ".spec.name"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster

// Zone is the Schema for the zones API
// +kubebuilder:printcolumn:name="Zone Name",type="string",JSONPath=".spec.name"
// +kubebuilder:printcolumn:name="ID",type="string",JSONPath=".status.id"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=`.status.conditions[?(@.type == "Ready")].status`
type Zone struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ZoneSpec   `json:"spec,omitempty"`
	Status ZoneStatus `json:"status,omitempty"`
}

// GetConditions returns the status conditions of the object.
func (in *Zone) GetConditions() []metav1.Condition {
	return in.Status.Conditions
}

// SetConditions sets the status conditions on the object.
func (in *Zone) SetConditions(conditions []metav1.Condition) {
	in.Status.Conditions = conditions
}

// +kubebuilder:object:root=true

// ZoneList contains a list of Zone
type ZoneList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Zone `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Zone{}, &ZoneList{})
}
