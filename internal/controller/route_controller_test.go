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
	"testing"

	cloudflareoperatoriov1 "github.com/containeroo/cloudflare-operator/api/v1"
	. "github.com/onsi/gomega"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func TestHostControllers(t *testing.T) {
	tests := []struct {
		name       string
		owner      client.Object
		reconciler func(*DNSHostReconciler) reconcile.Reconciler
	}{
		{name: "Ingress", owner: &networkingv1.Ingress{Spec: networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{{Host: testDNSRecordHost}, {Host: ""}, {Host: testDNSRecordHost}},
		}}, reconciler: func(r *DNSHostReconciler) reconcile.Reconciler {
			return &IngressReconciler{Client: r.Client, Scheme: r.Scheme, RetryInterval: r.RetryInterval, DefaultReconcileInterval: r.DefaultReconcileInterval}
		}},
		{name: "HTTPRoute", owner: &gatewayv1.HTTPRoute{Spec: gatewayv1.HTTPRouteSpec{
			Hostnames: []gatewayv1.Hostname{testDNSRecordHost, "", testDNSRecordHost},
		}}, reconciler: func(r *DNSHostReconciler) reconcile.Reconciler {
			return &HTTPRouteReconciler{Client: r.Client, Scheme: r.Scheme, RetryInterval: r.RetryInterval, DefaultReconcileInterval: r.DefaultReconcileInterval}
		}},
		{name: "TLSRoute", owner: &gatewayv1.TLSRoute{Spec: gatewayv1.TLSRouteSpec{
			Hostnames: []gatewayv1.Hostname{testDNSRecordHost, "", testDNSRecordHost},
		}}, reconciler: func(r *DNSHostReconciler) reconcile.Reconciler {
			return &TLSRouteReconciler{Client: r.Client, Scheme: r.Scheme, RetryInterval: r.RetryInterval, DefaultReconcileInterval: r.DefaultReconcileInterval}
		}},
		{name: "GRPCRoute", owner: &gatewayv1.GRPCRoute{Spec: gatewayv1.GRPCRouteSpec{
			Hostnames: []gatewayv1.Hostname{testDNSRecordHost, "", testDNSRecordHost},
		}}, reconciler: func(r *DNSHostReconciler) reconcile.Reconciler {
			return &GRPCRouteReconciler{Client: r.Client, Scheme: r.Scheme, RetryInterval: r.RetryInterval, DefaultReconcileInterval: r.DefaultReconcileInterval}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			tt.owner.SetName("owner")
			tt.owner.SetNamespace(testDefaultNamespace)
			tt.owner.SetUID("owner-uid")
			tt.owner.SetAnnotations(map[string]string{testContentAnnotation: testIPv4Address})
			r := newDNSHostTestReconciler(tt.owner)
			result, err := tt.reconciler(r).Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(tt.owner)})
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(result.IsZero()).To(BeTrue())
			records := &cloudflareoperatoriov1.DNSRecordList{}
			g.Expect(r.List(t.Context(), records)).To(Succeed())
			g.Expect(records.Items).To(HaveLen(1))
			record := records.Items[0]
			g.Expect(record.Spec.Name).To(Equal(testDNSRecordHost))
			g.Expect(record.Spec.Content).To(Equal(testIPv4Address))
			g.Expect(record.Spec.Interval.Duration).To(Equal(r.DefaultReconcileInterval))
			ownerRef := metav1.GetControllerOf(&record)
			g.Expect(ownerRef).NotTo(BeNil())
			g.Expect(ownerRef.Kind).To(Equal(tt.name))
			g.Expect(ownerRef.UID).To(Equal(tt.owner.GetUID()))
		})
	}
}
