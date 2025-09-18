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

package conditions

import (
	cloudflareoperatoriov1 "github.com/containeroo/cloudflare-operator/api/v1"
	cloudflareoperatoriov2 "github.com/containeroo/cloudflare-operator/api/v2"
	"github.com/containeroo/cloudflare-operator/internal/metrics"
	"github.com/fluxcd/pkg/runtime/conditions"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SetCondition updates the Kubernetes condition status dynamically
func SetCondition(to conditions.Setter, status metav1.ConditionStatus, reason, msg string) {
	condType, _, _, _ := conditionConstants(to)
	conditions.Set(to, &metav1.Condition{
		Type:    condType,
		Status:  status,
		Reason:  reason,
		Message: msg,
	})

	updateMetrics(to, status)
}

// updateMetrics handles updating the failure counters for each type
func updateMetrics(to conditions.Setter, status metav1.ConditionStatus) {
	value := 0.0
	if status == metav1.ConditionFalse {
		value = 1.0
	}

	switch o := to.(type) {
	case *cloudflareoperatoriov1.Account:
		metrics.AccountFailureCounter.WithLabelValues(o.Namespace, o.Name).Set(value)

	case *cloudflareoperatoriov2.Account:
		metrics.AccountFailureCounter.WithLabelValues(o.Namespace, o.Name).Set(value)

	case *cloudflareoperatoriov1.Zone:
		metrics.ZoneFailureCounter.WithLabelValues(o.Name, o.Spec.Name).Set(value)

	case *cloudflareoperatoriov1.IP:
		metrics.IpFailureCounter.WithLabelValues(o.Name, o.Spec.Type).Set(value)

	case *cloudflareoperatoriov1.DNSRecord:
		metrics.DnsRecordFailureCounter.WithLabelValues(o.Namespace, o.Name, o.Spec.Name).Set(value)
	}
}

// Convenience wrappers
func MarkFalse(to conditions.Setter, err error) {
	_, _, _, reasonFailed := conditionConstants(to)
	SetCondition(to, metav1.ConditionFalse, reasonFailed, err.Error())
}

func MarkTrue(to conditions.Setter, msg string) {
	_, reasonReady, _, _ := conditionConstants(to)
	SetCondition(to, metav1.ConditionTrue, reasonReady, msg)
}

func MarkUnknown(to conditions.Setter, msg string) {
	_, _, reasonNotReady, _ := conditionConstants(to)
	SetCondition(to, metav1.ConditionUnknown, reasonNotReady, msg)
}

func conditionConstants(to conditions.Setter) (condType, reasonReady, reasonNotReady, reasonFailed string) {
	switch to.(type) {
	case *cloudflareoperatoriov2.Account:
		return cloudflareoperatoriov2.ConditionTypeReady,
			cloudflareoperatoriov2.ConditionReasonReady,
			cloudflareoperatoriov2.ConditionReasonNotReady,
			cloudflareoperatoriov2.ConditionReasonFailed
	default:
		return cloudflareoperatoriov1.ConditionTypeReady,
			cloudflareoperatoriov1.ConditionReasonReady,
			cloudflareoperatoriov1.ConditionReasonNotReady,
			cloudflareoperatoriov1.ConditionReasonFailed
	}
}
