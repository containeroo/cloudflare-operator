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
	"os"
	"testing"

	"github.com/fluxcd/pkg/runtime/conditions"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	cloudflareoperatoriov1 "github.com/containeroo/cloudflare-operator/api/v1"
	networkingv1 "k8s.io/api/networking/v1"
)

func NewTestScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	utilruntime.Must(corev1.AddToScheme(s))
	utilruntime.Must(cloudflareoperatoriov1.AddToScheme(s))
	utilruntime.Must(networkingv1.AddToScheme(s))
	return s
}

func TestAccountReconciler_reconcileAccount(t *testing.T) {
	t.Run("reconcile account", func(t *testing.T) {
		g := NewWithT(t)

		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "secret",
				Namespace: "default",
			},
			Data: map[string][]byte{
				"apiToken": []byte(os.Getenv("CF_API_TOKEN")),
			},
		}

		account := &cloudflareoperatoriov1.Account{
			ObjectMeta: metav1.ObjectMeta{
				Name: "account",
			},
			Spec: cloudflareoperatoriov1.AccountSpec{
				ApiToken: cloudflareoperatoriov1.AccountSpecApiToken{
					SecretRef: corev1.SecretReference{
						Name:      "secret",
						Namespace: "default",
					},
				},
			},
		}

		accountManager := NewAccountManager()

		r := &AccountReconciler{
			Client: fake.NewClientBuilder().
				WithScheme(NewTestScheme()).
				WithObjects(secret, account).
				Build(),
			AccountManager: accountManager,
		}

		_, err := r.reconcileAccount(context.TODO(), account)
		g.Expect(err).ToNot(HaveOccurred())

		g.Expect(account.Status.Conditions).To(conditions.MatchConditions([]metav1.Condition{
			*conditions.TrueCondition(cloudflareoperatoriov1.ConditionTypeReady, cloudflareoperatoriov1.ConditionReasonReady, "Account is ready"),
		}))

		api, token, ok := accountManager.GetAccount(account.Name)
		g.Expect(ok).To(BeTrue())
		g.Expect(token).To(Equal(string(secret.Data["apiToken"])))
		g.Expect(api).ToNot(BeNil())
	})

	t.Run("econcile account error secret not found", func(t *testing.T) {
		g := NewWithT(t)

		account := &cloudflareoperatoriov1.Account{
			ObjectMeta: metav1.ObjectMeta{
				Name: "account",
			},
			Spec: cloudflareoperatoriov1.AccountSpec{
				ApiToken: cloudflareoperatoriov1.AccountSpecApiToken{
					SecretRef: corev1.SecretReference{
						Name:      "secret",
						Namespace: "default",
					},
				},
			},
		}

		accountManager := NewAccountManager()

		r := &AccountReconciler{
			Client: fake.NewClientBuilder().
				WithScheme(NewTestScheme()).
				WithObjects(account).
				Build(),
			AccountManager: accountManager,
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
				Name:      "secret",
				Namespace: "default",
			},
			Data: map[string][]byte{
				"invalid": []byte("invalid"),
			},
		}

		account := &cloudflareoperatoriov1.Account{
			ObjectMeta: metav1.ObjectMeta{
				Name: "account",
			},
			Spec: cloudflareoperatoriov1.AccountSpec{
				ApiToken: cloudflareoperatoriov1.AccountSpecApiToken{
					SecretRef: corev1.SecretReference{
						Name:      "secret",
						Namespace: "default",
					},
				},
			},
		}

		accountManager := NewAccountManager()

		r := &AccountReconciler{
			Client: fake.NewClientBuilder().
				WithScheme(NewTestScheme()).
				WithObjects(secret, account).
				Build(),
			AccountManager: accountManager,
		}

		_, err := r.reconcileAccount(context.TODO(), account)
		g.Expect(err).ToNot(HaveOccurred())

		g.Expect(account.Status.Conditions).To(conditions.MatchConditions([]metav1.Condition{
			*conditions.FalseCondition(cloudflareoperatoriov1.ConditionTypeReady, cloudflareoperatoriov1.ConditionReasonFailed, "secret has no key named \"apiToken\""),
		}))
	})
}
