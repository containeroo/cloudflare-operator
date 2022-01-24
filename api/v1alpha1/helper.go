package v1alpha1

// DNSRecordSettings is a struct that contains the default settings for a DNS record
type DNSRecordSettings struct {
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
}
