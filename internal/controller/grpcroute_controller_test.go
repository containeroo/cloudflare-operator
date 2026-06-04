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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func TestGRPCRouteReconciler_reconcileGRPCRoute(t *testing.T) {
	grpcRoute := &gatewayv1.GRPCRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "grpcroute",
			Namespace: testDefaultNamespace,
			Annotations: map[string]string{
				testContentAnnotation: testIPv4Address,
			},
		},
		Spec: gatewayv1.GRPCRouteSpec{
			Hostnames: []gatewayv1.Hostname{
				"grpc.containeroo-test.org",
			},
		},
	}

	r := &GRPCRouteReconciler{
		Client: fake.NewClientBuilder().
			WithScheme(NewTestScheme()).
			WithObjects(grpcRoute).
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

	t.Run("reconcile grpcroute", func(t *testing.T) {
		g := NewWithT(t)
		grpcRoute.Spec.Hostnames = []gatewayv1.Hostname{"grpc.containeroo-test.org"}

		_, err := r.reconcileGRPCRoute(context.TODO(), grpcRoute)
		g.Expect(err).NotTo(HaveOccurred())

		dnsRecord := &cloudflareoperatoriov1.DNSRecord{}
		err = r.Get(context.TODO(), client.ObjectKey{Namespace: testDefaultNamespace, Name: "grpc-containeroo-test-org"}, dnsRecord)
		g.Expect(err).NotTo(HaveOccurred())

		g.Expect(dnsRecord.Spec).To(HaveField("Name", Equal("grpc.containeroo-test.org")))
		g.Expect(dnsRecord.Spec).To(HaveField("Content", Equal(testIPv4Address)))
	})

	t.Run("change dnsrecord spec when annotations change", func(t *testing.T) {
		g := NewWithT(t)
		grpcRoute.Annotations = map[string]string{
			testContentAnnotation: testAlternateIPv4Address,
		}

		_, err := r.reconcileGRPCRoute(context.TODO(), grpcRoute)
		g.Expect(err).NotTo(HaveOccurred())

		dnsRecord := &cloudflareoperatoriov1.DNSRecord{}
		err = r.Get(context.TODO(), client.ObjectKey{Namespace: testDefaultNamespace, Name: "grpc-containeroo-test-org"}, dnsRecord)
		g.Expect(err).NotTo(HaveOccurred())

		g.Expect(dnsRecord.Spec).To(HaveField("Name", Equal("grpc.containeroo-test.org")))
		g.Expect(dnsRecord.Spec).To(HaveField("Content", Equal(testAlternateIPv4Address)))
	})

	t.Run("reconcile grpcroute wildcard", func(t *testing.T) {
		g := NewWithT(t)
		grpcRoute.Spec.Hostnames = []gatewayv1.Hostname{testWildcardHost}

		_, err := r.reconcileGRPCRoute(context.TODO(), grpcRoute)
		g.Expect(err).NotTo(HaveOccurred())

		dnsRecord := &cloudflareoperatoriov1.DNSRecord{}
		err = r.Get(context.TODO(), client.ObjectKey{Namespace: testDefaultNamespace, Name: testWildcardDNSRecordName}, dnsRecord)
		g.Expect(err).NotTo(HaveOccurred())

		g.Expect(dnsRecord.Spec).To(HaveField("Name", Equal(testWildcardHost)))
		g.Expect(dnsRecord.Spec).To(HaveField("Content", Equal(testAlternateIPv4Address)))
	})

	t.Run("remove dnsrecord when annotations are absent", func(t *testing.T) {
		g := NewWithT(t)
		grpcRoute.Annotations = map[string]string{}

		_, err := r.reconcileGRPCRoute(context.TODO(), grpcRoute)
		g.Expect(err).NotTo(HaveOccurred())

		dnsRecord := &cloudflareoperatoriov1.DNSRecord{}
		err = r.Get(context.TODO(), client.ObjectKey{Namespace: testDefaultNamespace, Name: testWildcardDNSRecordName}, dnsRecord)
		g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
	})
}
