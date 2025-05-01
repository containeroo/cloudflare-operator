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
	"testing"
	"time"

	cloudflareoperatoriov1 "github.com/containeroo/cloudflare-operator/api/v1"
	. "github.com/onsi/gomega"

	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestIngressReconciler_reconcileIngress(t *testing.T) {
	ingress := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ingress",
			Namespace: "default",
			Annotations: map[string]string{
				"cloudflare-operator.io/content": "1.1.1.1",
			},
		},
	}

	r := &IngressReconciler{
		Client: fake.NewClientBuilder().
			WithScheme(NewTestScheme()).
			WithObjects(ingress).
			WithIndex(&cloudflareoperatoriov1.DNSRecord{}, cloudflareoperatoriov1.OwnerRefUIDIndexKey, func(obj client.Object) []string {
				dnsRecord, ok := obj.(*cloudflareoperatoriov1.DNSRecord)
				if !ok {
					return nil
				}
				if len(dnsRecord.OwnerReferences) == 0 {
					return nil
				}
				return []string{string(dnsRecord.OwnerReferences[0].UID)}
			}).
			Build(),
		Scheme: NewTestScheme(),
	}

	t.Run("reconcile ingress", func(t *testing.T) {
		g := NewWithT(t)
		ingress.Spec = networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{{
				Host: "ingtest.containeroo-test.org",
			}},
		}
		_, err := r.reconcileIngress(context.TODO(), ingress)
		g.Expect(err).NotTo(HaveOccurred())

		dnsRecord := &cloudflareoperatoriov1.DNSRecord{}
		err = r.Get(context.TODO(), client.ObjectKey{Namespace: "default", Name: "ingtest-containeroo-test-org"}, dnsRecord)
		g.Expect(err).NotTo(HaveOccurred())

		g.Expect(dnsRecord.Spec).To(HaveField("Name", Equal("ingtest.containeroo-test.org")))
		g.Expect(dnsRecord.Spec).To(HaveField("Content", Equal("1.1.1.1")))
	})

	t.Run("change dnsrecord spec when annotations change", func(t *testing.T) {
		g := NewWithT(t)
		ingress.Annotations = map[string]string{
			"cloudflare-operator.io/content": "2.2.2.2",
		}

		_, err := r.reconcileIngress(context.TODO(), ingress)
		g.Expect(err).NotTo(HaveOccurred())

		dnsRecord := &cloudflareoperatoriov1.DNSRecord{}
		err = r.Get(context.TODO(), client.ObjectKey{Namespace: "default", Name: "ingtest-containeroo-test-org"}, dnsRecord)
		g.Expect(err).NotTo(HaveOccurred())

		g.Expect(dnsRecord.Spec).To(HaveField("Name", Equal("ingtest.containeroo-test.org")))
		g.Expect(dnsRecord.Spec).To(HaveField("Content", Equal("2.2.2.2")))
	})

	t.Run("reconcile ingress wildcard", func(t *testing.T) {
		g := NewWithT(t)
		ingress.Spec = networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{{
				Host: "*.containeroo-test.org",
			}},
		}
		_, err := r.reconcileIngress(context.TODO(), ingress)
		g.Expect(err).NotTo(HaveOccurred())

		dnsRecord := &cloudflareoperatoriov1.DNSRecord{}
		err = r.Get(context.TODO(), client.ObjectKey{Namespace: "default", Name: "wildcard-containeroo-test-org"}, dnsRecord)
		g.Expect(err).NotTo(HaveOccurred())

		g.Expect(dnsRecord.Spec).To(HaveField("Name", Equal("*.containeroo-test.org")))
		g.Expect(dnsRecord.Spec).To(HaveField("Content", Equal("2.2.2.2")))
	})

	t.Run("remove dnsrecord when annotations are absent", func(t *testing.T) {
		g := NewWithT(t)
		ingress.Annotations = map[string]string{}

		_, err := r.reconcileIngress(context.TODO(), ingress)
		g.Expect(err).NotTo(HaveOccurred())

		dnsRecord := &cloudflareoperatoriov1.DNSRecord{}
		err = r.Get(context.TODO(), client.ObjectKey{Namespace: "default", Name: "wildcard-containeroo-test-org"}, dnsRecord)
		g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
	})

	t.Run("ingress annotation parsing", func(t *testing.T) {
		g := NewWithT(t)
		ingress.Annotations = map[string]string{
			"cloudflare-operator.io/content":  "1.1.1.1",
			"cloudflare-operator.io/ip-ref":   "ip",
			"cloudflare-operator.io/proxied":  "true",
			"cloudflare-operator.io/ttl":      "120", // Expecting to return 1 because proxied is true
			"cloudflare-operator.io/type":     "A",
			"cloudflare-operator.io/interval": "10s",
		}

		parsedSpec := r.parseAnnotations(ingress.Annotations)

		g.Expect(parsedSpec).To(HaveField("Content", Equal("1.1.1.1")))
		g.Expect(parsedSpec.IPRef).To(HaveField("Name", Equal("ip")))
		g.Expect(parsedSpec).To(HaveField("Proxied", Equal(&[]bool{true}[0])))
		g.Expect(parsedSpec).To(HaveField("TTL", Equal(1)))
		g.Expect(parsedSpec).To(HaveField("Type", Equal("A")))
		g.Expect(parsedSpec.Interval.Duration).To(Equal(10 * time.Second))
	})
}
