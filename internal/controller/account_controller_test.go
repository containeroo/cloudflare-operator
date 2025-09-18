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
	"os"
	"testing"
	"time"

	"github.com/fluxcd/pkg/runtime/conditions"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/cloudflare/cloudflare-go"
	cloudflareoperatoriov1 "github.com/containeroo/cloudflare-operator/api/v1"
	cloudflareoperatoriov2 "github.com/containeroo/cloudflare-operator/api/v2"
	networkingv1 "k8s.io/api/networking/v1"
)

func NewTestScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	utilruntime.Must(corev1.AddToScheme(s))
	utilruntime.Must(cloudflareoperatoriov1.AddToScheme(s))
	utilruntime.Must(cloudflareoperatoriov2.AddToScheme(s))
	utilruntime.Must(networkingv1.AddToScheme(s))
	return s
}

var cloudflareAPI cloudflare.API

func TestAccountReconciler_reconcileAccount(t *testing.T) {
	runAccountReconcilerTests[*cloudflareoperatoriov1.Account](t, "v1", func() *cloudflareoperatoriov1.Account {
		return &cloudflareoperatoriov1.Account{}
	})
	runAccountReconcilerTests[*cloudflareoperatoriov2.Account](t, "v2", func() *cloudflareoperatoriov2.Account {
		return &cloudflareoperatoriov2.Account{}
	})
}

func runAccountReconcilerTests[T AccountObject](t *testing.T, version string, newAccount func() T) {
	t.Helper()

	t.Run(fmt.Sprintf("%s reconcile account", version), func(t *testing.T) {
		g := NewWithT(t)
		cloudflareAPI = cloudflare.API{}

		account := newAccount()
		secret := configureAccountAndSecret(account, map[string][]byte{
			"apiToken": []byte(os.Getenv("CF_API_TOKEN")),
		})

		r := &AccountReconciler[T]{
			Client: fake.NewClientBuilder().
				WithScheme(NewTestScheme()).
				WithObjects(secret, account).
				Build(),
			CloudflareAPI: &cloudflareAPI,
			RetryInterval: time.Second,
		}

		_, err := r.reconcileAccount(context.TODO(), account)
		g.Expect(err).ToNot(HaveOccurred())

		condType, reasonReady, _, _ := accountConditionConstantsFromAccount(account)
		g.Expect(account.GetConditions()).To(conditions.MatchConditions([]metav1.Condition{
			*conditions.TrueCondition(condType, reasonReady, "Account is ready"),
		}))

		g.Expect(cloudflareAPI.APIToken).To(Equal(string(secret.Data["apiToken"])))
	})

	t.Run(fmt.Sprintf("%s reconcile account error secret not found", version), func(t *testing.T) {
		g := NewWithT(t)
		cloudflareAPI = cloudflare.API{}

		account := newAccount()
		_ = configureAccountAndSecret(account, map[string][]byte{
			"apiToken": []byte(os.Getenv("CF_API_TOKEN")),
		})

		r := &AccountReconciler[T]{
			Client: fake.NewClientBuilder().
				WithScheme(NewTestScheme()).
				WithObjects(account).
				Build(),
			CloudflareAPI: &cloudflareAPI,
			RetryInterval: time.Second,
		}

		_, err := r.reconcileAccount(context.TODO(), account)
		g.Expect(err).ToNot(HaveOccurred())

		condType, _, _, reasonFailed := accountConditionConstantsFromAccount(account)
		g.Expect(account.GetConditions()).To(HaveLen(1))
		condition := account.GetConditions()[0]
		g.Expect(condition.Type).To(Equal(condType))
		g.Expect(condition.Status).To(Equal(metav1.ConditionFalse))
		g.Expect(condition.Reason).To(Equal(reasonFailed))
		g.Expect(condition.Message).To(ContainSubstring("secret"))
	})

	t.Run(fmt.Sprintf("%s reconcile account error key not found in secret", version), func(t *testing.T) {
		g := NewWithT(t)
		cloudflareAPI = cloudflare.API{}

		account := newAccount()
		secret := configureAccountAndSecret(account, map[string][]byte{
			"invalid": []byte("invalid"),
		})

		r := &AccountReconciler[T]{
			Client: fake.NewClientBuilder().
				WithScheme(NewTestScheme()).
				WithObjects(secret, account).
				Build(),
			CloudflareAPI: &cloudflareAPI,
			RetryInterval: time.Second,
		}

		_, err := r.reconcileAccount(context.TODO(), account)
		g.Expect(err).ToNot(HaveOccurred())

		condType, _, _, reasonFailed := accountConditionConstantsFromAccount(account)
		g.Expect(account.GetConditions()).To(HaveLen(1))
		condition := account.GetConditions()[0]
		g.Expect(condition.Type).To(Equal(condType))
		g.Expect(condition.Status).To(Equal(metav1.ConditionFalse))
		g.Expect(condition.Reason).To(Equal(reasonFailed))
		g.Expect(condition.Message).To(Equal("secret has no key named \"apiToken\""))
	})
}

func configureAccountAndSecret[T AccountObject](account T, data map[string][]byte) *corev1.Secret {
	secretData := data
	if secretData == nil {
		secretData = map[string][]byte{}
	}

	switch a := any(account).(type) {
	case *cloudflareoperatoriov1.Account:
		a.ObjectMeta = metav1.ObjectMeta{
			Name:      "account",
			Namespace: "default",
		}
		a.Spec = cloudflareoperatoriov1.AccountSpec{
			ApiToken: cloudflareoperatoriov1.AccountSpecApiToken{
				SecretRef: corev1.SecretReference{
					Name:      "secret",
					Namespace: "default",
				},
			},
		}
	case *cloudflareoperatoriov2.Account:
		a.ObjectMeta = metav1.ObjectMeta{
			Name:      "account",
			Namespace: "default",
		}
		a.Spec = cloudflareoperatoriov2.AccountSpec{
			ApiToken: cloudflareoperatoriov2.AccountSpecApiToken{
				SecretRef: corev1.SecretReference{
					Name:      "secret",
					Namespace: "default",
				},
			},
		}
	default:
		panic("unsupported account type")
	}

	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "secret",
			Namespace: "default",
		},
		Data: secretData,
	}
}

func accountConditionConstantsFromAccount[T AccountObject](account T) (condType, reasonReady, reasonNotReady, reasonFailed string) {
	switch any(account).(type) {
	case *cloudflareoperatoriov2.Account:
		return cloudflareoperatoriov2.ConditionTypeReady,
			cloudflareoperatoriov2.ConditionReasonReady,
			cloudflareoperatoriov2.ConditionReasonNotReady,
			cloudflareoperatoriov2.ConditionReasonFailed
	case *cloudflareoperatoriov1.Account:
		return cloudflareoperatoriov1.ConditionTypeReady,
			cloudflareoperatoriov1.ConditionReasonReady,
			cloudflareoperatoriov1.ConditionReasonNotReady,
			cloudflareoperatoriov1.ConditionReasonFailed
	default:
		panic("unsupported account type")
	}
}
