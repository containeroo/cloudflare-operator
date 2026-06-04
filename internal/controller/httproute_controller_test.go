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
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestHTTPRouteReconciler_reconcileHTTPRoute(t *testing.T) {
	httpRoute := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "httproute",
			Namespace: testDefaultNamespace,
			Annotations: map[string]string{
				testContentAnnotation: testIPv4Address,
			},
		},
		Spec: gatewayv1.HTTPRouteSpec{
			Hostnames: []gatewayv1.Hostname{
				"app.containeroo-test.org",
			},
		},
	}

	r := &HTTPRouteReconciler{
		Client: fake.NewClientBuilder().
			WithScheme(NewTestScheme()).
			WithObjects(httpRoute).
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
		Scheme:                   NewTestScheme(),
		DefaultReconcileInterval: time.Minute,
	}

	t.Run("reconcile httproute", func(t *testing.T) {
		g := NewWithT(t)
		httpRoute.Spec.Hostnames = []gatewayv1.Hostname{"app.containeroo-test.org"}

		_, err := r.reconcileHTTPRoute(context.TODO(), httpRoute)
		g.Expect(err).NotTo(HaveOccurred())

		dnsRecord := &cloudflareoperatoriov1.DNSRecord{}
		err = r.Get(context.TODO(), client.ObjectKey{Namespace: testDefaultNamespace, Name: "app-containeroo-test-org"}, dnsRecord)
		g.Expect(err).NotTo(HaveOccurred())

		g.Expect(dnsRecord.Spec).To(HaveField("Name", Equal("app.containeroo-test.org")))
		g.Expect(dnsRecord.Spec).To(HaveField("Content", Equal(testIPv4Address)))
	})

	t.Run("change dnsrecord spec when annotations change", func(t *testing.T) {
		g := NewWithT(t)
		httpRoute.Annotations = map[string]string{
			testContentAnnotation: testAlternateIPv4Address,
		}

		_, err := r.reconcileHTTPRoute(context.TODO(), httpRoute)
		g.Expect(err).NotTo(HaveOccurred())

		dnsRecord := &cloudflareoperatoriov1.DNSRecord{}
		err = r.Get(context.TODO(), client.ObjectKey{Namespace: testDefaultNamespace, Name: "app-containeroo-test-org"}, dnsRecord)
		g.Expect(err).NotTo(HaveOccurred())

		g.Expect(dnsRecord.Spec).To(HaveField("Name", Equal("app.containeroo-test.org")))
		g.Expect(dnsRecord.Spec).To(HaveField("Content", Equal(testAlternateIPv4Address)))
	})

	t.Run("reconcile httproute wildcard", func(t *testing.T) {
		g := NewWithT(t)
		httpRoute.Spec.Hostnames = []gatewayv1.Hostname{testWildcardHost}

		_, err := r.reconcileHTTPRoute(context.TODO(), httpRoute)
		g.Expect(err).NotTo(HaveOccurred())

		dnsRecord := &cloudflareoperatoriov1.DNSRecord{}
		err = r.Get(context.TODO(), client.ObjectKey{Namespace: testDefaultNamespace, Name: testWildcardDNSRecordName}, dnsRecord)
		g.Expect(err).NotTo(HaveOccurred())

		g.Expect(dnsRecord.Spec).To(HaveField("Name", Equal(testWildcardHost)))
		g.Expect(dnsRecord.Spec).To(HaveField("Content", Equal(testAlternateIPv4Address)))
	})

	t.Run("remove dnsrecord when annotations are absent", func(t *testing.T) {
		g := NewWithT(t)
		httpRoute.Annotations = map[string]string{}

		_, err := r.reconcileHTTPRoute(context.TODO(), httpRoute)
		g.Expect(err).NotTo(HaveOccurred())

		dnsRecord := &cloudflareoperatoriov1.DNSRecord{}
		err = r.Get(context.TODO(), client.ObjectKey{Namespace: testDefaultNamespace, Name: testWildcardDNSRecordName}, dnsRecord)
		g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
	})
}
