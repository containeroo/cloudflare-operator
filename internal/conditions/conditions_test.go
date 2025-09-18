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
	"errors"
	"testing"

	cloudflareoperatoriov1 "github.com/containeroo/cloudflare-operator/api/v1"
	cloudflareoperatoriov2 "github.com/containeroo/cloudflare-operator/api/v2"
	"github.com/fluxcd/pkg/runtime/conditions"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestPredicate(t *testing.T) {
	t.Run("set true condition", func(t *testing.T) {
		g := NewWithT(t)

		testAccount := &cloudflareoperatoriov1.Account{}

		MarkTrue(testAccount, "test")

		g.Expect(testAccount.Status.Conditions).To(conditions.MatchConditions([]metav1.Condition{
			*conditions.TrueCondition(cloudflareoperatoriov1.ConditionTypeReady, cloudflareoperatoriov1.ConditionReasonReady, "test"),
		}))

		v2Account := &cloudflareoperatoriov2.Account{}

		MarkTrue(v2Account, "test")

		g.Expect(v2Account.Status.Conditions).To(conditions.MatchConditions([]metav1.Condition{
			*conditions.TrueCondition(cloudflareoperatoriov2.ConditionTypeReady, cloudflareoperatoriov2.ConditionReasonReady, "test"),
		}))
	})

	t.Run("set false condition", func(t *testing.T) {
		g := NewWithT(t)

		testAccount := &cloudflareoperatoriov1.Account{}

		MarkFalse(testAccount, errors.New("test"))

		g.Expect(testAccount.Status.Conditions).To(conditions.MatchConditions([]metav1.Condition{
			*conditions.FalseCondition(cloudflareoperatoriov1.ConditionTypeReady, cloudflareoperatoriov1.ConditionReasonFailed, "test"),
		}))

		v2Account := &cloudflareoperatoriov2.Account{}

		MarkFalse(v2Account, errors.New("test"))

		g.Expect(v2Account.Status.Conditions).To(conditions.MatchConditions([]metav1.Condition{
			*conditions.FalseCondition(cloudflareoperatoriov2.ConditionTypeReady, cloudflareoperatoriov2.ConditionReasonFailed, "test"),
		}))
	})

	t.Run("set unknown condition", func(t *testing.T) {
		g := NewWithT(t)

		testAccount := &cloudflareoperatoriov1.Account{}

		MarkUnknown(testAccount, "test")

		g.Expect(testAccount.Status.Conditions).To(conditions.MatchConditions([]metav1.Condition{
			*conditions.UnknownCondition(cloudflareoperatoriov1.ConditionTypeReady, cloudflareoperatoriov1.ConditionReasonNotReady, "test"),
		}))

		v2Account := &cloudflareoperatoriov2.Account{}

		MarkUnknown(v2Account, "test")

		g.Expect(v2Account.Status.Conditions).To(conditions.MatchConditions([]metav1.Condition{
			*conditions.UnknownCondition(cloudflareoperatoriov2.ConditionTypeReady, cloudflareoperatoriov2.ConditionReasonNotReady, "test"),
		}))
	})
}
