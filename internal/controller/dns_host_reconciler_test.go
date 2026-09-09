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
	"time"

	cloudflareoperatoriov1 "github.com/containeroo/cloudflare-operator/api/v1"
	. "github.com/onsi/gomega"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newDNSHostTestReconciler(objects ...client.Object) *DNSHostReconciler {
	scheme := newTestScheme()
	return &DNSHostReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).
			WithIndex(&cloudflareoperatoriov1.DNSRecord{}, cloudflareoperatoriov1.OwnerRefUIDIndexKey, func(obj client.Object) []string {
				if owner := metav1.GetControllerOf(obj); owner != nil {
					return []string{string(owner.UID)}
				}
				return nil
			}).Build(),
		Scheme:                   scheme,
		RetryInterval:            time.Second,
		DefaultReconcileInterval: time.Minute,
	}
}

func TestDNSHostReconciler(t *testing.T) {
	tests := []struct {
		name        string
		existing    bool
		hosts       map[string]struct{}
		annotations map[string]string
		deleting    bool
		want        map[string]string
	}{
		{name: "create", hosts: map[string]struct{}{testDNSRecordHost: {}},
			annotations: map[string]string{testContentAnnotation: testIPv4Address},
			want:        map[string]string{testDNSRecordHost: testIPv4Address}},
		{name: "update content", existing: true, hosts: map[string]struct{}{testDNSRecordHost: {}},
			annotations: map[string]string{testContentAnnotation: testAlternateIPv4Address},
			want:        map[string]string{testDNSRecordHost: testAlternateIPv4Address}},
		{name: "replace host with wildcard", existing: true, hosts: map[string]struct{}{testWildcardHost: {}},
			annotations: map[string]string{testContentAnnotation: testIPv4Address},
			want:        map[string]string{testWildcardHost: testIPv4Address}},
		{name: "remove annotations", existing: true, hosts: map[string]struct{}{testDNSRecordHost: {}}, want: map[string]string{}},
		{name: "remove hosts", existing: true, annotations: map[string]string{testContentAnnotation: testIPv4Address}, want: map[string]string{}},
		{name: "skip deleting owner", deleting: true, hosts: map[string]struct{}{testDNSRecordHost: {}},
			annotations: map[string]string{testContentAnnotation: testIPv4Address}, want: map[string]string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			owner := &networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{
				Name: "ingress", Namespace: testDefaultNamespace, UID: "ingress-owner",
			}}
			otherOwner := owner.DeepCopy()
			otherOwner.Name, otherOwner.UID = "other-ingress", "other-owner"
			r := newDNSHostTestReconciler(owner, otherOwner)
			ctx := t.Context()
			_, err := r.Reconcile(ctx, otherOwner, map[string]string{testContentAnnotation: testIPv4Address}, map[string]struct{}{testAlternateDNSRecordHost: {}})
			g.Expect(err).NotTo(HaveOccurred())
			if tt.existing {
				_, err = r.Reconcile(ctx, owner, map[string]string{testContentAnnotation: testIPv4Address}, map[string]struct{}{testDNSRecordHost: {}})
				g.Expect(err).NotTo(HaveOccurred())
				before := &cloudflareoperatoriov1.DNSRecord{}
				g.Expect(r.Get(ctx, client.ObjectKey{Namespace: testDefaultNamespace, Name: "dnstest-containeroo-test-org"}, before)).To(Succeed())
			}
			if tt.deleting {
				now := metav1.Now()
				owner.DeletionTimestamp = &now
			}
			result, err := r.Reconcile(ctx, owner, tt.annotations, tt.hosts)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(result.IsZero()).To(BeTrue())
			records := &cloudflareoperatoriov1.DNSRecordList{}
			g.Expect(r.List(ctx, records, client.MatchingFields{cloudflareoperatoriov1.OwnerRefUIDIndexKey: string(owner.UID)})).To(Succeed())
			actual := make(map[string]string)
			for _, record := range records.Items {
				actual[record.Spec.Name] = record.Spec.Content
				g.Expect(metav1.GetControllerOf(&record).UID).To(Equal(owner.UID))
				g.Expect(record.Labels).To(HaveKeyWithValue("app.kubernetes.io/managed-by", "cloudflare-operator"))
				g.Expect(record.Spec.Interval.Duration).To(Equal(time.Minute))
				if record.Spec.Name == testWildcardHost {
					g.Expect(record.Name).To(Equal(testWildcardDNSRecordName))
				}
			}
			g.Expect(actual).To(Equal(tt.want))
			other := &cloudflareoperatoriov1.DNSRecord{}
			g.Expect(r.Get(ctx, client.ObjectKey{Namespace: testDefaultNamespace, Name: "other-example-com"}, other)).To(Succeed())
		})
	}
}
