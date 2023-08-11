/*
Copyright 2023 containeroo

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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type AccountSpecApiToken struct {
	// Secret containing the API token (key must be named "apiToken")
	SecretRef corev1.SecretReference `json:"secretRef"`
}

// AccountSpec defines the desired state of Account
type AccountSpec struct {
	// Cloudflare API token
	ApiToken AccountSpecApiToken `json:"apiToken"`
	// Interval to check account status
	// +kubebuilder:default="5m"
	// +optional
	Interval metav1.Duration `json:"interval,omitempty"`
	// List of zone names that should be managed by cloudflare-operator
	// +optional
	ManagedZones []string `json:"managedZones,omitempty"`
}

type AccountStatusZones struct {
	// Name of the zone
	// +optional
	Name string `json:"name,omitempty"`
	// ID of the zone
	// +optional
	ID string `json:"id,omitempty"`
}

// AccountStatus defines the observed state of Account
type AccountStatus struct {
	// Conditions contains the different condition statuses for the Account object.
	// +optional
	Conditions []metav1.Condition `json:"conditions"`
	// Zones contains all the zones of the Account
	// +optional
	Zones []AccountStatusZones `json:"zones,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster

// Account is the Schema for the accounts API
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=`.status.conditions[?(@.type == "Ready")].status`
type Account struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AccountSpec   `json:"spec,omitempty"`
	Status AccountStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AccountList contains a list of Account
type AccountList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Account `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Account{}, &AccountList{})
}
