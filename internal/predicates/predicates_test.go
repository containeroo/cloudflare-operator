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

package predicates

import (
	"testing"

	. "github.com/onsi/gomega"
	"sigs.k8s.io/controller-runtime/pkg/event"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	networkingv1 "k8s.io/api/networking/v1"
)

func TestPredicate(t *testing.T) {
	predicate := DNSFromIngressPredicate{}

	t.Run("createa ingress annotation predicate with no annotations", func(t *testing.T) {
		g := NewWithT(t)

		ingress := &networkingv1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name: "ingress",
			},
		}

		result := predicate.Create(event.CreateEvent{Object: ingress})

		g.Expect(result).To(BeFalse())
	})

	t.Run("create ingress annotation predicate with annotation", func(t *testing.T) {
		g := NewWithT(t)

		ingress := &networkingv1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name: "ingress",
				Annotations: map[string]string{
					"cloudflare-operator.io/content": "test",
				},
			},
		}

		predicate := DNSFromIngressPredicate{}
		result := predicate.Create(event.CreateEvent{Object: ingress})

		g.Expect(result).To(BeTrue())
	})

	t.Run("update ingress annotation predicate with content annotation", func(t *testing.T) {
		g := NewWithT(t)

		oldIngress := &networkingv1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name: "ingress",
				Annotations: map[string]string{
					"cloudflare-operator.io/content": "test",
				},
			},
			Spec: networkingv1.IngressSpec{
				Rules: []networkingv1.IngressRule{{
					Host: "test.containeroo-test.org",
				}},
			},
		}

		newIngress := &networkingv1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name: "ingress",
				Annotations: map[string]string{
					"cloudflare-operator.io/content": "new-test",
				},
			},
			Spec: networkingv1.IngressSpec{
				Rules: []networkingv1.IngressRule{{
					Host: "test.containeroo-test.org",
				}},
			},
		}

		predicate := DNSFromIngressPredicate{}
		result := predicate.Update(event.UpdateEvent{ObjectOld: oldIngress, ObjectNew: newIngress})

		g.Expect(result).To(BeTrue())
	})

	t.Run("update ingress rules predicate", func(t *testing.T) {
		g := NewWithT(t)

		oldIngress := &networkingv1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name: "ingress",
				Annotations: map[string]string{
					"cloudflare-operator.io/content": "test",
				},
			},
			Spec: networkingv1.IngressSpec{
				Rules: []networkingv1.IngressRule{{
					Host: "test.containeroo-test.org",
				}},
			},
		}

		newIngress := &networkingv1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name: "ingress",
				Annotations: map[string]string{
					"cloudflare-operator.io/content": "test",
				},
			},
			Spec: networkingv1.IngressSpec{
				Rules: []networkingv1.IngressRule{{
					Host: "test-new.containeroo-test.org",
				}},
			},
		}

		predicate := DNSFromIngressPredicate{}
		result := predicate.Update(event.UpdateEvent{ObjectOld: oldIngress, ObjectNew: newIngress})

		g.Expect(result).To(BeTrue())
	})
}
