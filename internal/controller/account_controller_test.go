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
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	cloudflareoperatoriov1 "github.com/containeroo/cloudflare-operator/api/v1"
	"github.com/fluxcd/pkg/runtime/conditions"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestAccountReconciler_reconcileAccount(t *testing.T) {
	t.Run("reconcile account", func(t *testing.T) {
		g := NewWithT(t)

		secret, account := newTestAccountObjects("first-token")

		r := &AccountReconciler{
			Client: fake.NewClientBuilder().
				WithScheme(newTestScheme()).
				WithObjects(secret, account).
				Build(),
		}

		_, err := r.reconcileAccount(context.TODO(), account)
		g.Expect(err).ToNot(HaveOccurred())

		g.Expect(account.Status.Conditions).To(conditions.MatchConditions([]metav1.Condition{
			*conditions.TrueCondition(cloudflareoperatoriov1.ConditionTypeReady, cloudflareoperatoriov1.ConditionReasonReady, "Account is ready"),
		}))
	})

	t.Run("reconcile account error secret not found", func(t *testing.T) {
		g := NewWithT(t)

		account := &cloudflareoperatoriov1.Account{
			ObjectMeta: metav1.ObjectMeta{
				Name: testAccountName,
			},
			Spec: cloudflareoperatoriov1.AccountSpec{
				ApiToken: cloudflareoperatoriov1.AccountSpecApiToken{
					SecretRef: corev1.SecretReference{
						Name:      testSecretName,
						Namespace: testDefaultNamespace,
					},
				},
			},
		}

		r := &AccountReconciler{
			Client: fake.NewClientBuilder().
				WithScheme(newTestScheme()).
				WithObjects(account).
				Build(),
		}

		_, err := r.reconcileAccount(context.TODO(), account)
		g.Expect(err).ToNot(HaveOccurred())

		g.Expect(account.Status.Conditions).To(conditions.MatchConditions([]metav1.Condition{
			*conditions.FalseCondition(cloudflareoperatoriov1.ConditionTypeReady, cloudflareoperatoriov1.ConditionReasonFailed, "secrets \"secret\" not found"),
		}))
	})

	t.Run("reconcile account error key not found in secret", func(t *testing.T) {
		g := NewWithT(t)

		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      testSecretName,
				Namespace: testDefaultNamespace,
			},
			Data: map[string][]byte{
				"invalid": []byte("invalid"),
			},
		}

		account := &cloudflareoperatoriov1.Account{
			ObjectMeta: metav1.ObjectMeta{
				Name: testAccountName,
			},
			Spec: cloudflareoperatoriov1.AccountSpec{
				ApiToken: cloudflareoperatoriov1.AccountSpecApiToken{
					SecretRef: corev1.SecretReference{
						Name:      testSecretName,
						Namespace: testDefaultNamespace,
					},
				},
			},
		}

		r := &AccountReconciler{
			Client: fake.NewClientBuilder().
				WithScheme(newTestScheme()).
				WithObjects(secret, account).
				Build(),
		}

		_, err := r.reconcileAccount(context.TODO(), account)
		g.Expect(err).ToNot(HaveOccurred())

		g.Expect(account.Status.Conditions).To(conditions.MatchConditions([]metav1.Condition{
			*conditions.FalseCondition(cloudflareoperatoriov1.ConditionTypeReady, cloudflareoperatoriov1.ConditionReasonFailed, "secret has no key named \"apiToken\""),
		}))
	})
}

func TestCloudflareAPIForAccountName(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("Authorization") != "Bearer second-token" {
			t.Errorf("unexpected authorization: %q", req.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.Copy(w, cloudflareZoneResponse(req).Body)
	}))
	t.Cleanup(server.Close)
	t.Setenv("CLOUDFLARE_BASE_URL", server.URL)

	t.Run("requires a single account resource", func(t *testing.T) {
		g := NewWithT(t)

		secret, account := newTestAccountObjects("first-token")
		otherSecret := secret.DeepCopy()
		otherSecret.Name = "other-secret"
		otherSecret.Data["apiToken"] = []byte("second-token")

		otherAccount := account.DeepCopy()
		otherAccount.Name = "other-account"
		otherAccount.Spec.ApiToken.SecretRef.Name = otherSecret.Name

		kubeClient := fake.NewClientBuilder().
			WithScheme(newTestScheme()).
			WithObjects(secret, account, otherSecret, otherAccount).
			Build()

		_, err := cloudflareAPIForAccountName(context.TODO(), kubeClient, "")
		g.Expect(err).To(MatchError("multiple Account resources found; specify spec.accountRef.name"))
	})

	t.Run("uses explicit account reference when provided", func(t *testing.T) {
		g := NewWithT(t)

		secret, account := newTestAccountObjects("first-token")
		otherSecret := secret.DeepCopy()
		otherSecret.Name = "other-secret"
		otherSecret.Data["apiToken"] = []byte("second-token")

		otherAccount := account.DeepCopy()
		otherAccount.Name = "other-account"
		otherAccount.Spec.ApiToken.SecretRef.Name = otherSecret.Name

		kubeClient := fake.NewClientBuilder().
			WithScheme(newTestScheme()).
			WithObjects(secret, account, otherSecret, otherAccount).
			Build()

		api, err := cloudflareAPIForAccountName(context.TODO(), kubeClient, otherAccount.Name)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(api).ToNot(BeNil())
		zoneID, err := cloudflareZoneIDByName(t.Context(), api, "example.com")
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(zoneID).To(Equal("zone-id"))
	})
}
