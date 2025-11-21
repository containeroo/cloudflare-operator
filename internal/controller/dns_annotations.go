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

package controller

import (
	"strconv"
	"time"

	cloudflareoperatoriov1 "github.com/containeroo/cloudflare-operator/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// parseDNSAnnotations converts the DNS-related annotations into a DNSRecordSpec.
func parseDNSAnnotations(annotations map[string]string, defaultReconcileInterval time.Duration) cloudflareoperatoriov1.DNSRecordSpec {
	dnsRecordSpec := cloudflareoperatoriov1.DNSRecordSpec{}

	dnsRecordSpec.Content = annotations["cloudflare-operator.io/content"]
	dnsRecordSpec.IPRef.Name = annotations["cloudflare-operator.io/ip-ref"]

	proxied, err := strconv.ParseBool(annotations["cloudflare-operator.io/proxied"])
	if err != nil {
		proxied = true
	}

	dnsRecordSpec.Proxied = &proxied
	ttl, err := strconv.Atoi(annotations["cloudflare-operator.io/ttl"])
	if err != nil {
		ttl = 1
	}
	if *dnsRecordSpec.Proxied && ttl != 1 {
		ttl = 1
	}
	dnsRecordSpec.TTL = ttl

	dnsRecordSpec.Type = annotations["cloudflare-operator.io/type"]
	if dnsRecordSpec.Type == "" {
		dnsRecordSpec.Type = "A"
	}

	intervalDuration, err := time.ParseDuration(annotations["cloudflare-operator.io/interval"])
	if err != nil {
		intervalDuration = defaultReconcileInterval
	}
	dnsRecordSpec.Interval = metav1.Duration{Duration: intervalDuration}

	dnsRecordSpec.Comment = annotations["cloudflare-operator.io/comment"]

	return dnsRecordSpec
}
