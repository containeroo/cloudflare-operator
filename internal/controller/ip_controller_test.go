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
	"context"
	"net/http"
	"testing"

	"github.com/fluxcd/pkg/runtime/conditions"
	. "github.com/onsi/gomega"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	cloudflareoperatoriov1 "github.com/containeroo/cloudflare-operator/api/v1"
)

func StartIPSource() {
	http.HandleFunc("/plain", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("1.1.1.1"))
	})
	http.HandleFunc("/json", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ip":"1.1.1.1"}`))
	})
	_ = http.ListenAndServe(":8080", nil)
}

func TestIPReconciler_reconcileIP(t *testing.T) {
	g := NewWithT(t)

	ip := &cloudflareoperatoriov1.IP{
		ObjectMeta: metav1.ObjectMeta{
			Name: "ip",
		},
		Spec: cloudflareoperatoriov1.IPSpec{
			Type: "dynamic",
		},
	}

	r := &IPReconciler{
		Client: fake.NewClientBuilder().
			WithScheme(NewTestScheme()).
			WithObjects().
			Build(),
	}

	go StartIPSource()

	t.Run("reconcile dynamic ip plain text", func(t *testing.T) {
		ip.Spec.IPSources = []cloudflareoperatoriov1.IPSpecIPSources{{
			URL: "http://localhost:8080/plain",
		}}

		_ = r.reconcileIP(context.TODO(), ip)

		g.Expect(ip.Status.Conditions).To(conditions.MatchConditions([]metav1.Condition{
			*conditions.TrueCondition(cloudflareoperatoriov1.ConditionTypeReady, cloudflareoperatoriov1.ConditionReasonReady, "IP is ready"),
		}))

		g.Expect(ip.Spec.Address).To(Equal("1.1.1.1"))
	})

	t.Run("reconcile dynamic ip jq filter", func(t *testing.T) {
		ip.Spec.IPSources = []cloudflareoperatoriov1.IPSpecIPSources{{
			URL:              "http://localhost:8080/json",
			ResponseJQFilter: ".ip",
		}}

		_ = r.reconcileIP(context.TODO(), ip)

		g.Expect(ip.Status.Conditions).To(conditions.MatchConditions([]metav1.Condition{
			*conditions.TrueCondition(cloudflareoperatoriov1.ConditionTypeReady, cloudflareoperatoriov1.ConditionReasonReady, "IP is ready"),
		}))

		g.Expect(ip.Spec.Address).To(Equal("1.1.1.1"))
	})

	t.Run("reconcile dynamic ip regex", func(t *testing.T) {
		ip.Spec.IPSources = []cloudflareoperatoriov1.IPSpecIPSources{{
			URL:                 "http://localhost:8080/json",
			PostProcessingRegex: "([0-9]+\\.[0-9]+\\.[0-9]+\\.[0-9]+)",
		}}

		_ = r.reconcileIP(context.TODO(), ip)

		g.Expect(ip.Status.Conditions).To(conditions.MatchConditions([]metav1.Condition{
			*conditions.TrueCondition(cloudflareoperatoriov1.ConditionTypeReady, cloudflareoperatoriov1.ConditionReasonReady, "IP is ready"),
		}))

		g.Expect(ip.Spec.Address).To(Equal("1.1.1.1"))
	})
}
