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

package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	k8smetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	AccountFailureCounter = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "cloudflare_operator_account_status",
			Help: "Cloudflare account status",
		},
		[]string{"name"},
	)
	DnsRecordFailureCounter = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "cloudflare_operator_dns_record_status",
			Help: "Cloudflare DNS records status",
		},
		[]string{"namespace", "name", "record_name"},
	)
	IpFailureCounter = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "cloudflare_operator_ip_status",
			Help: "IPs status",
		},
		[]string{"name", "ip_type"},
	)
	ZoneFailureCounter = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "cloudflare_operator_zone_status",
			Help: "Cloudflare zones status",
		},
		[]string{"name", "zone_name"},
	)
)

func init() {
	k8smetrics.Registry.MustRegister(AccountFailureCounter, DnsRecordFailureCounter, IpFailureCounter, ZoneFailureCounter)
}
