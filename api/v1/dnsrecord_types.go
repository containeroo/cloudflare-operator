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
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type DNSRecordSpecIPRef struct {
	// Name of the IP object
	// +optional
	Name string `json:"name,omitempty"`
}

// DNSRecordSpec defines the desired state of DNSRecord
type DNSRecordSpec struct {
	// DNS record name (e.g. example.com)
	// +kubebuilder:validation:MaxLength=255
	Name string `json:"name"`
	// DNS record content (e.g. 127.0.0.1)
	// +optional
	Content string `json:"content,omitempty"`
	// Reference to an IP object
	// +optional
	IPRef DNSRecordSpecIPRef `json:"ipRef,omitempty"`
	// DNS record type
	// +kubebuilder:default=A
	// +optional
	Type string `json:"type,omitempty"`
	// Whether the record is receiving the performance and security benefits of Cloudflare
	// +kubebuilder:default=true
	// +optional
	Proxied *bool `json:"proxied,omitempty"`
	// Time to live, in seconds, of the DNS record. Must be between 60 and 86400, or 1 for "automatic" (e.g. 3600)
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=86400
	// +kubebuilder:default=1
	// +optional
	TTL int `json:"ttl,omitempty"`
	// Data holds arbitrary key-value pairs used to further configure the DNS record
	// +optional
	Data *apiextensionsv1.JSON `json:"data,omitempty"`
	// Required for MX, SRV and URI records; unused by other record types. Records with lower priorities are preferred.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=65535
	// +optional
	Priority *uint16 `json:"priority,omitempty"`
	// Interval to check DNSRecord
	// +kubebuilder:default="5m"
	// +optional
	Interval metav1.Duration `json:"interval,omitempty"`
}

// DNSRecordStatus defines the observed state of DNSRecord
type DNSRecordStatus struct {
	// Conditions contains the different condition statuses for the DNSRecord object.
	// +optional
	Conditions []metav1.Condition `json:"conditions"`
	// Cloudflare DNS record ID
	// +optional
	RecordID string `json:"recordID,omitempty"`
}

const (
	IPRefIndexKey       string = ".spec.ipRef.name"
	OwnerRefUIDIndexKey string = ".metadata.ownerReferences.uid"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// DNSRecord is the Schema for the dnsrecords API
// +kubebuilder:printcolumn:name="Record Name",type="string",JSONPath=".spec.name"
// +kubebuilder:printcolumn:name="Type",type="string",JSONPath=".spec.type"
// +kubebuilder:printcolumn:name="Content",type="string",JSONPath=".spec.content",priority=1
// +kubebuilder:printcolumn:name="Proxied",type="boolean",JSONPath=".spec.proxied",priority=1
// +kubebuilder:printcolumn:name="TTL",type="integer",JSONPath=".spec.ttl",priority=1
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=`.status.conditions[?(@.type == "Ready")].status`
type DNSRecord struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DNSRecordSpec   `json:"spec,omitempty"`
	Status DNSRecordStatus `json:"status,omitempty"`
}

// GetConditions returns the status conditions of the object.
func (in *DNSRecord) GetConditions() []metav1.Condition {
	return in.Status.Conditions
}

// SetConditions sets the status conditions on the object.
func (in *DNSRecord) SetConditions(conditions []metav1.Condition) {
	in.Status.Conditions = conditions
}

// +kubebuilder:object:root=true

// DNSRecordList contains a list of DNSRecord
type DNSRecordList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DNSRecord `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DNSRecord{}, &DNSRecordList{})
}
