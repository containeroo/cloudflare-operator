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

	cloudflareoperatoriov1 "github.com/containeroo/cloudflare-operator/api/v1"
	networkingv1 "k8s.io/api/networking/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func TestPredicate(t *testing.T) {
	predicate := DNSFromIngressPredicate{}

	t.Run("create ingress annotation predicate with no annotations", func(t *testing.T) {
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

	t.Run("create httproute annotation predicate with annotation", func(t *testing.T) {
		g := NewWithT(t)

		httpRoute := &gatewayv1.HTTPRoute{
			ObjectMeta: metav1.ObjectMeta{
				Name: "httproute",
				Annotations: map[string]string{
					"cloudflare-operator.io/content": "test",
				},
			},
		}

		predicate := DNSFromHTTPRoutePredicate{}
		result := predicate.Create(event.CreateEvent{Object: httpRoute})

		g.Expect(result).To(BeTrue())
	})

	t.Run("update httproute hostname predicate", func(t *testing.T) {
		g := NewWithT(t)

		oldHTTPRoute := &gatewayv1.HTTPRoute{
			ObjectMeta: metav1.ObjectMeta{
				Name: "httproute",
				Annotations: map[string]string{
					"cloudflare-operator.io/content": "test",
				},
			},
			Spec: gatewayv1.HTTPRouteSpec{
				Hostnames: []gatewayv1.Hostname{"test.containeroo-test.org"},
			},
		}

		newHTTPRoute := &gatewayv1.HTTPRoute{
			ObjectMeta: metav1.ObjectMeta{
				Name: "httproute",
				Annotations: map[string]string{
					"cloudflare-operator.io/content": "test",
				},
			},
			Spec: gatewayv1.HTTPRouteSpec{
				Hostnames: []gatewayv1.Hostname{"test-new.containeroo-test.org"},
			},
		}

		predicate := DNSFromHTTPRoutePredicate{}
		result := predicate.Update(event.UpdateEvent{ObjectOld: oldHTTPRoute, ObjectNew: newHTTPRoute})

		g.Expect(result).To(BeTrue())
	})

	t.Run("update ip dnsrecord predicate", func(t *testing.T) {
		g := NewWithT(t)

		oldIP := &cloudflareoperatoriov1.IP{
			Spec: cloudflareoperatoriov1.IPSpec{
				Address: "1.1.1.1",
			},
		}

		newIP := &cloudflareoperatoriov1.IP{
			Spec: cloudflareoperatoriov1.IPSpec{
				Address: "2.2.2.2",
			},
		}

		predicate := IPAddressChangedPredicate{}
		result := predicate.Update(event.UpdateEvent{ObjectOld: oldIP, ObjectNew: newIP})

		g.Expect(result).To(BeTrue())
	})
}
