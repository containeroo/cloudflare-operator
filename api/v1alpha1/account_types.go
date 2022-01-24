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
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

type AccountSpecGlobalApiKey struct {
	// Secret name containing the API key (key must be named "apiKey")
	SecretRef v1.SecretReference `json:"secretRef"`
}

// AccountSpec defines the desired state of Account
type AccountSpec struct {
	// Email of the Cloudflare account
	Email string `json:"email"`
	// Global API key of the Cloudflare account
	GlobalApiKey AccountSpecGlobalApiKey `json:"globalApiKey"`
	// Interval to check account status
	//+kubebuilder:default="5m"
	//+optional
	Interval metav1.Duration `json:"interval"`
}

type AccountStatusZones struct {
	Name string `json:"name"`
	ID   string `json:"id"`
}

// AccountStatus defines the observed state of Account
type AccountStatus struct {
	// Phase of the Account
	//+kubebuilder:validation:Enum=Ready;Failed
	Phase string `json:"phase"`
	// Message if the Account authentication failed
	Message string `json:"message,omitempty"`
	// Zones contains all the zones of the Account
	Zones []AccountStatusZones `json:"zones,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:resource:scope=Cluster

// Account is the Schema for the accounts API
//+kubebuilder:printcolumn:name="Email",type="string",JSONPath=".spec.email"
//+kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
type Account struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AccountSpec   `json:"spec,omitempty"`
	Status AccountStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// AccountList contains a list of Account
type AccountList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Account `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Account{}, &AccountList{})
}
