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

package v1beta1

import (
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type IPSpecIPSources struct {
	// URL of the IP source (e.g. https://api.hetzner.cloud/v1/servers/12345)
	// +optional
	URL string `json:"url,omitempty"`
	// RequestBody to be sent to the URL
	// +optional
	RequestBody string `json:"requestBody,omitempty"`
	// RequestHeaders to be sent to the URL
	// +optional
	RequestHeaders map[string]string `json:"requestHeaders,omitempty"`
	// RequestHeadersSecretRef is a secret reference to the headers to be sent to the URL (e.g. for authentication)
	// where the key is the header name and the value is the header value
	// +optional
	RequestHeadersSecretRef v1.SecretReference `json:"requestHeadersSecretRef,omitempty"`
	// RequestMethod defines the HTTP method to be used
	// +kubebuilder:validation:Enum=GET;POST;PUT;DELETE
	// +kubebuilder:default=GET
	RequestMethod string `json:"requestMethod,omitempty"`
	// ResponseJSONPath defines the JSON path to the value to be used as IP
	// +optional
	ResponseJSONPath string `json:"responseJSONPath,omitempty"`
	// ResponseTextRegex defines the regular expression to be used to extract the IP from the response
	// +optional
	ResponseTextRegex string `json:"responseTextRegex,omitempty"`
}

// IPSpec defines the desired state of IP
type IPSpec struct {
	// IP address (omit if type is dynamic)
	// +optional
	Address string `json:"address,omitempty"`
	// IP address type (static or dynamic)
	// +kubebuilder:validation:Enum=static;dynamic
	// +kubebuilder:default=static
	// +optional
	Type string `json:"type,omitempty"`
	// Interval at which a dynamic IP should be checked
	// +optional
	Interval *metav1.Duration `json:"interval,omitempty"`
	// IPSources can be configured to get an IP from an external source (e.g. an API or public IP echo service)
	// +optional
	IPSources []IPSpecIPSources `json:"ipSources,omitempty"`
}

// IPStatus defines the observed state of IP
type IPStatus struct {
	// Phase of the IP
	// +kubebuilder:validation:Enum=Ready;Failed
	// +optional
	Phase string `json:"phase,omitempty"`
	// Message if the IP failed to update
	// +optional
	Message string `json:"message,omitempty"`
	// LastObservedIP contains the IP address observed at the last interval (used to determine whether the IP has changed)
	// +optional
	LastObservedIP string `json:"lastObservedIP,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster

// IP is the Schema for the ips API
// +kubebuilder:printcolumn:name="Address",type="string",JSONPath=".spec.address"
// +kubebuilder:printcolumn:name="Type",type="string",JSONPath=".spec.type"
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
type IP struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   IPSpec   `json:"spec,omitempty"`
	Status IPStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// IPList contains a list of IP
type IPList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []IP `json:"items"`
}

func init() {
	SchemeBuilder.Register(&IP{}, &IPList{})
}
