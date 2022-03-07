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

package controllers

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	accountFailureCounter = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "cloudflare_operator_account_failure_counter",
			Help: "Cloudflare accounts that failed",
		},
		[]string{"name"},
	)
	dnsRecordFailureCounter = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "cloudflare_operator_dns_record_failure_counter",
			Help: "Cloudflare DNS records that failed",
		},
		[]string{"namespace", "name", "recordName", "recordType"},
	)
	ipFailureCounter = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "cloudflare_operator_ip_failure_counter",
			Help: "IPs that failed",
		},
		[]string{"name", "ipType"},
	)
	zoneFailureCounter = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "cloudflare_operator_zone_failure_counter",
			Help: "Cloudflare zones that failed",
		},
		[]string{"name", "zoneName"},
	)
)

func init() {
	metrics.Registry.MustRegister(accountFailureCounter, dnsRecordFailureCounter, ipFailureCounter, zoneFailureCounter)
}
