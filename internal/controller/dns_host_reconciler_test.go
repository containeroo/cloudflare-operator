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

	cloudflareoperatoriov1 "github.com/containeroo/cloudflare-operator/api/v1"
	. "github.com/onsi/gomega"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func TestDNSHostReconciler_skipsDeletingOwners(t *testing.T) {
	deletionTimestamp := metav1.Now()
	objectMeta := func(name string) metav1.ObjectMeta {
		return metav1.ObjectMeta{
			Name:              name,
			Namespace:         testDefaultNamespace,
			DeletionTimestamp: &deletionTimestamp,
		}
	}

	tests := []struct {
		name  string
		owner client.Object
	}{
		{name: "Ingress", owner: &networkingv1.Ingress{ObjectMeta: objectMeta("ingress")}},
		{name: "HTTPRoute", owner: &gatewayv1.HTTPRoute{ObjectMeta: objectMeta("httproute")}},
		{name: "TLSRoute", owner: &gatewayv1.TLSRoute{ObjectMeta: objectMeta("tlsroute")}},
		{name: "GRPCRoute", owner: &gatewayv1.GRPCRoute{ObjectMeta: objectMeta("grpcroute")}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			r := &DNSHostReconciler{
				Client: fake.NewClientBuilder().WithScheme(NewTestScheme()).Build(),
				Scheme: NewTestScheme(),
			}

			result, err := r.Reconcile(
				context.TODO(),
				tt.owner,
				map[string]string{testContentAnnotation: testIPv4Address},
				map[string]struct{}{testDNSRecordHost: {}},
			)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(result.IsZero()).To(BeTrue())

			dnsRecords := &cloudflareoperatoriov1.DNSRecordList{}
			g.Expect(r.List(context.TODO(), dnsRecords)).To(Succeed())
			g.Expect(dnsRecords.Items).To(BeEmpty())
		})
	}
}
