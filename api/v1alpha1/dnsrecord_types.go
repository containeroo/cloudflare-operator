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

// DNSRecordSpec defines the desired state of DNSRecord
type DNSRecordSpec struct {
	// Name of the DNS record (e.g. app.example.com)
	Name string `json:"name"`
	// Content of the DNS record (e.g. 144.231.20.1)
	//+optional
	Content string `json:"content"`
	//+optional
	IpRef v1.ObjectReference `json:"ipRef,omitempty"`
	//+optional
	DNSRecordSettings `json:",inline"`
	// Type of DNS record (A, CNAME)
	//+kubebuilder:validation:Enum=A;CNAME
	//+kubebuilder:default=A
	//+optional
	Type string `json:"type"`
}

// DNSRecordStatus defines the observed state of DNSRecord
type DNSRecordStatus struct {
	// Phase of the DNS record
	//+kubebuilder:validation:Enum=Created;Failed
	Phase string `json:"phase"`
	// Message if the DNS record failed
	Message string `json:"message,omitempty"`
	// Cloudflare DNS record ID
	RecordID string `json:"recordId"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// DNSRecord is the Schema for the dnsrecords API
//+kubebuilder:printcolumn:name="Record Name",type="string",JSONPath=".spec.name"
//+kubebuilder:printcolumn:name="Type",type="string",JSONPath=".spec.type"
//+kubebuilder:printcolumn:name="Content",type="string",JSONPath=".spec.content"
//+kubebuilder:printcolumn:name="Status",type="string",JSONPath=".status.phase"
type DNSRecord struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DNSRecordSpec   `json:"spec,omitempty"`
	Status DNSRecordStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// DNSRecordList contains a list of DNSRecord
type DNSRecordList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DNSRecord `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DNSRecord{}, &DNSRecordList{})
}
