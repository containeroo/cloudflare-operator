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
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fluxcd/pkg/runtime/conditions"
	. "github.com/onsi/gomega"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	cloudflareoperatoriov1 "github.com/containeroo/cloudflare-operator/api/v1"
	corev1 "k8s.io/api/core/v1"
)

func TestIPReconciler_reconcileIP(t *testing.T) {
	var requestHeader, requestAuthHeader string

	mux := http.NewServeMux()
	mux.HandleFunc("/plain", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(testIPv4Address))
	})
	mux.HandleFunc("/invalid", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("invalid"))
	})
	mux.HandleFunc("/json", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{"ip":"%s"}`, testIPv4Address)
	})
	mux.HandleFunc("/header", func(w http.ResponseWriter, r *http.Request) {
		requestHeader = r.Header.Get("X-Test")
		requestAuthHeader = r.Header.Get("X-Auth-Test")
		_, _ = w.Write([]byte(testIPv4Address))
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testSecretName,
			Namespace: testDefaultNamespace,
		},
		Data: map[string][]byte{
			"X-Auth-Test": []byte("auth-test"),
		},
	}

	r := &IPReconciler{
		Client: fake.NewClientBuilder().
			WithScheme(newTestScheme()).
			WithObjects(secret).
			Build(),
	}

	t.Run("reconcile dynamic ip plain text", func(t *testing.T) {
		g := NewWithT(t)
		ip := &cloudflareoperatoriov1.IP{Spec: cloudflareoperatoriov1.IPSpec{Type: testIPTypeDynamic}}
		ip.Spec.IPSources = []cloudflareoperatoriov1.IPSpecIPSources{{
			URL: server.URL + "/plain",
		}}

		_ = r.reconcileIP(context.TODO(), ip)

		g.Expect(ip.Status.Conditions).To(conditions.MatchConditions([]metav1.Condition{
			*conditions.TrueCondition(cloudflareoperatoriov1.ConditionTypeReady, cloudflareoperatoriov1.ConditionReasonReady, "IP is ready"),
		}))

		g.Expect(ip.Status.Address).To(Equal(testIPv4Address))
		g.Expect(ip.Spec.Address).To(BeEmpty())
		g.Expect(ip.Spec.Interval).To(BeNil())
	})

	t.Run("reconcile dynamic ip plain text error invalid ip", func(t *testing.T) {
		g := NewWithT(t)
		ip := &cloudflareoperatoriov1.IP{Spec: cloudflareoperatoriov1.IPSpec{Type: testIPTypeDynamic}}
		ip.Spec.IPSources = []cloudflareoperatoriov1.IPSpecIPSources{{
			URL: server.URL + "/invalid",
		}}

		_ = r.reconcileIP(context.TODO(), ip)

		g.Expect(ip.Status.Conditions).To(conditions.MatchConditions([]metav1.Condition{
			*conditions.FalseCondition(cloudflareoperatoriov1.ConditionTypeReady, cloudflareoperatoriov1.ConditionReasonFailed, "ip from source %s/invalid is invalid: invalid", server.URL),
		}))
	})

	t.Run("reconcile dynamic ip error invalid source URL", func(t *testing.T) {
		g := NewWithT(t)
		ip := &cloudflareoperatoriov1.IP{Spec: cloudflareoperatoriov1.IPSpec{Type: testIPTypeDynamic}}
		ip.Spec.IPSources = []cloudflareoperatoriov1.IPSpecIPSources{{
			URL: "/plain",
		}}

		_ = r.reconcileIP(context.TODO(), ip)

		g.Expect(ip.Status.Conditions).To(conditions.MatchConditions([]metav1.Condition{
			*conditions.FalseCondition(cloudflareoperatoriov1.ConditionTypeReady, cloudflareoperatoriov1.ConditionReasonFailed, `IP source URL "/plain" must be an absolute http or https URL`),
		}))
	})

	t.Run("reconcile dynamic ip jq filter", func(t *testing.T) {
		g := NewWithT(t)
		ip := &cloudflareoperatoriov1.IP{Spec: cloudflareoperatoriov1.IPSpec{Type: testIPTypeDynamic}}
		ip.Spec.IPSources = []cloudflareoperatoriov1.IPSpecIPSources{{
			URL:              server.URL + "/json",
			ResponseJQFilter: ".ip",
		}}

		_ = r.reconcileIP(context.TODO(), ip)

		g.Expect(ip.Status.Conditions).To(conditions.MatchConditions([]metav1.Condition{
			*conditions.TrueCondition(cloudflareoperatoriov1.ConditionTypeReady, cloudflareoperatoriov1.ConditionReasonReady, "IP is ready"),
		}))

		g.Expect(ip.Status.Address).To(Equal(testIPv4Address))
		g.Expect(ip.Spec.Address).To(BeEmpty())
	})

	t.Run("reconcile dynamic ip regex", func(t *testing.T) {
		g := NewWithT(t)
		ip := &cloudflareoperatoriov1.IP{Spec: cloudflareoperatoriov1.IPSpec{Type: testIPTypeDynamic}}
		ip.Spec.IPSources = []cloudflareoperatoriov1.IPSpecIPSources{{
			URL:                 server.URL + "/json",
			PostProcessingRegex: "([0-9]+\\.[0-9]+\\.[0-9]+\\.[0-9]+)",
		}}

		_ = r.reconcileIP(context.TODO(), ip)

		g.Expect(ip.Status.Conditions).To(conditions.MatchConditions([]metav1.Condition{
			*conditions.TrueCondition(cloudflareoperatoriov1.ConditionTypeReady, cloudflareoperatoriov1.ConditionReasonReady, "IP is ready"),
		}))

		g.Expect(ip.Status.Address).To(Equal(testIPv4Address))
		g.Expect(ip.Spec.Address).To(BeEmpty())
	})

	t.Run("reconcile dynamic ip with header", func(t *testing.T) {
		g := NewWithT(t)
		ip := &cloudflareoperatoriov1.IP{Spec: cloudflareoperatoriov1.IPSpec{Type: testIPTypeDynamic}}
		ip.Spec.IPSources = []cloudflareoperatoriov1.IPSpecIPSources{{
			URL: server.URL + "/header",
			RequestHeaders: &apiextensionsv1.JSON{
				Raw: []byte(`{"X-Test":"test"}`),
			},
		}}

		_ = r.reconcileIP(context.TODO(), ip)

		g.Expect(ip.Status.Conditions).To(conditions.MatchConditions([]metav1.Condition{
			*conditions.TrueCondition(cloudflareoperatoriov1.ConditionTypeReady, cloudflareoperatoriov1.ConditionReasonReady, "IP is ready"),
		}))

		g.Expect(ip.Status.Address).To(Equal(testIPv4Address))
		g.Expect(ip.Spec.Address).To(BeEmpty())
		g.Expect(requestHeader).To(Equal("test"))
	})

	t.Run("reconcile dynamic ip with header from secret", func(t *testing.T) {
		g := NewWithT(t)
		ip := &cloudflareoperatoriov1.IP{Spec: cloudflareoperatoriov1.IPSpec{Type: testIPTypeDynamic}}
		ip.Spec.IPSources = []cloudflareoperatoriov1.IPSpecIPSources{{
			URL: server.URL + "/header",
			RequestHeadersSecretRef: corev1.SecretReference{
				Name:      testSecretName,
				Namespace: testDefaultNamespace,
			},
		}}

		_ = r.reconcileIP(context.TODO(), ip)

		g.Expect(ip.Status.Conditions).To(conditions.MatchConditions([]metav1.Condition{
			*conditions.TrueCondition(cloudflareoperatoriov1.ConditionTypeReady, cloudflareoperatoriov1.ConditionReasonReady, "IP is ready"),
		}))

		g.Expect(ip.Status.Address).To(Equal(testIPv4Address))
		g.Expect(ip.Spec.Address).To(BeEmpty())
		g.Expect(requestAuthHeader).To(Equal("auth-test"))
	})

	t.Run("reconcile static ip", func(t *testing.T) {
		g := NewWithT(t)
		ip := &cloudflareoperatoriov1.IP{Spec: cloudflareoperatoriov1.IPSpec{Type: testIPTypeDynamic}}
		ip.Spec.Type = testIPTypeStatic
		ip.Spec.Address = testIPv4Address

		_ = r.reconcileIP(context.TODO(), ip)

		g.Expect(ip.Status.Conditions).To(conditions.MatchConditions([]metav1.Condition{
			*conditions.TrueCondition(cloudflareoperatoriov1.ConditionTypeReady, cloudflareoperatoriov1.ConditionTypeReady, "IP is ready"),
		}))
		g.Expect(ip.Status.Address).To(Equal(testIPv4Address))
		g.Expect(ip.Spec.Address).To(Equal(testIPv4Address))
	})

	t.Run("reconcile static ip error no address", func(t *testing.T) {
		g := NewWithT(t)
		ip := &cloudflareoperatoriov1.IP{Spec: cloudflareoperatoriov1.IPSpec{Type: testIPTypeDynamic}}
		ip.Spec.Type = testIPTypeStatic
		ip.Spec.Address = ""

		_ = r.reconcileIP(context.TODO(), ip)

		g.Expect(ip.Status.Conditions).To(conditions.MatchConditions([]metav1.Condition{
			*conditions.FalseCondition(cloudflareoperatoriov1.ConditionTypeReady, cloudflareoperatoriov1.ConditionReasonFailed, "address is required for static IPs"),
		}))
	})

	t.Run("reconcile static ip error invalid address", func(t *testing.T) {
		g := NewWithT(t)
		ip := &cloudflareoperatoriov1.IP{Spec: cloudflareoperatoriov1.IPSpec{Type: testIPTypeDynamic}}
		ip.Spec.Type = testIPTypeStatic
		ip.Spec.Address = "invalid"

		_ = r.reconcileIP(context.TODO(), ip)

		g.Expect(ip.Status.Conditions).To(conditions.MatchConditions([]metav1.Condition{
			*conditions.FalseCondition(cloudflareoperatoriov1.ConditionTypeReady, cloudflareoperatoriov1.ConditionReasonFailed, "IP address \"invalid\" is not valid"),
		}))
	})
}
