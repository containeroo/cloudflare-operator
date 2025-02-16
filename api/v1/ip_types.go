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
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type IPSpecIPSources struct {
	// URL of the IP source (e.g. https://checkip.amazonaws.com)
	URL string `json:"url,omitempty"`
	// RequestBody to be sent to the URL
	// +optional
	RequestBody string `json:"requestBody,omitempty"`
	// RequestHeaders to be sent to the URL
	// +optional
	RequestHeaders *apiextensionsv1.JSON `json:"requestHeaders,omitempty"`
	// RequestHeadersSecretRef is a secret reference to the headers to be sent to the URL (e.g. for authentication)
	// where the key is the header name and the value is the header value
	// +optional
	RequestHeadersSecretRef corev1.SecretReference `json:"requestHeadersSecretRef,omitempty"`
	// RequestMethod defines the HTTP method to be used
	// +kubebuilder:validation:Enum=GET;POST;PUT;DELETE
	// +optional
	RequestMethod string `json:"requestMethod,omitempty"`
	// ResponseJQFilter applies a JQ filter to the response to extract the IP
	// +optional
	ResponseJQFilter string `json:"responseJQFilter,omitempty"`
	// PostProcessingRegex defines the regular expression to be used to extract the IP from the response or a JQ filter result
	// +optional
	PostProcessingRegex string `json:"postProcessingRegex,omitempty"`
	// InsecureSkipVerify defines whether to skip TLS certificate verification
	// +optional
	InsecureSkipVerify bool `json:"insecureSkipVerify,omitempty"`
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
	// Conditions contains the different condition statuses for the IP object.
	// +optional
	Conditions []metav1.Condition `json:"conditions"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster

// IP is the Schema for the ips API
// +kubebuilder:printcolumn:name="Address",type="string",JSONPath=".spec.address"
// +kubebuilder:printcolumn:name="Type",type="string",JSONPath=".spec.type"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=`.status.conditions[?(@.type == "Ready")].status`
type IP struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   IPSpec   `json:"spec,omitempty"`
	Status IPStatus `json:"status,omitempty"`
}

// GetConditions returns the status conditions of the object.
func (in *IP) GetConditions() []metav1.Condition {
	return in.Status.Conditions
}

// SetConditions sets the status conditions on the object.
func (in *IP) SetConditions(conditions []metav1.Condition) {
	in.Status.Conditions = conditions
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
