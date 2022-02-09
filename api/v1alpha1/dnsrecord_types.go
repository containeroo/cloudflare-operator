/*
Copyright 2022 containeroo

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

type DNSRecordSpecIPRef struct {
	// Name of the IP object
	Name string `json:"name"`
}

// DNSRecordSpec defines the desired state of DNSRecord
type DNSRecordSpec struct {
	// Name of the DNS record (e.g. app.example.com)
	Name string `json:"name"`
	// Content of the DNS record (e.g. 144.231.20.1)
	//+optional
	Content string `json:"content"`
	// Reference to an IP object
	//+optional
	IPRef DNSRecordSpecIPRef `json:"ipRef,omitempty"`
	// Type of DNS record (A, CNAME)
	//+kubebuilder:validation:Enum=A;CNAME
	//+kubebuilder:default=A
	//+optional
	Type string `json:"type"`
	// Proxied indicates whether the DNS record should be proxied
	//+kubebuilder:default=true
	//+optional
	Proxied *bool `json:"proxied"`
	// TTL of the DNS record (e.g. 300, 1 for automatic)
	//+kubebuilder:validation:Minimum=1
	//+kubebuilder:validation:Maximum=86400
	//+kubebuilder:default=1
	//+optional
	TTL int `json:"ttl"`
	// Interval to check DNSRecord
	//+kubebuilder:default="5m"
	//+optional
	Interval metav1.Duration `json:"interval"`
}

// DNSRecordStatus defines the observed state of DNSRecord
type DNSRecordStatus struct {
	// Phase of the DNS record
	//+kubebuilder:validation:Enum=Created;Pending;Failed
	Phase string `json:"phase"`
	// Message if the DNS record failed
	Message string `json:"message,omitempty"`
	// Cloudflare DNS record ID
	RecordID string `json:"recordID"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// DNSRecord is the Schema for the dnsrecords API
//+kubebuilder:printcolumn:name="Record Name",type="string",JSONPath=".spec.name"
//+kubebuilder:printcolumn:name="Type",type="string",JSONPath=".spec.type"
//+kubebuilder:printcolumn:name="Content",type="string",JSONPath=".spec.content"
//+kubebuilder:printcolumn:name="Proxied",type="boolean",JSONPath=".spec.proxied"
//+kubebuilder:printcolumn:name="TTL",type="integer",JSONPath=".spec.ttl"
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
