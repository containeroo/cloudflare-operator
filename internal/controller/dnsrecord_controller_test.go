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
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/cloudflare/cloudflare-go/v7/dns"
	cloudflareoperatoriov1 "github.com/containeroo/cloudflare-operator/api/v1"
	"github.com/fluxcd/pkg/runtime/conditions"
	. "github.com/onsi/gomega"
	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestDNSRecordReconciler_reconcileDNSRecord(t *testing.T) {
	if os.Getenv("CF_API_TOKEN") == "" || os.Getenv("CF_ZONE_ID") == "" {
		t.Skip("requires CF_API_TOKEN and CF_ZONE_ID for live Cloudflare tests")
	}
	api := newCloudflareClient(os.Getenv("CF_API_TOKEN"))
	for _, tt := range []struct {
		name  string
		ipRef bool
		adopt bool
	}{
		{name: "create"},
		{name: "resolve ipref", ipRef: true},
		{name: "adopt", adopt: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			ctx := t.Context()
			zone := &cloudflareoperatoriov1.Zone{
				Spec: cloudflareoperatoriov1.ZoneSpec{Name: "containeroo-test.org"},
				Status: cloudflareoperatoriov1.ZoneStatus{ID: os.Getenv("CF_ZONE_ID"), Conditions: []metav1.Condition{
					*conditions.TrueCondition(cloudflareoperatoriov1.ConditionTypeReady, cloudflareoperatoriov1.ConditionReasonReady, "Zone is ready"),
				}},
			}
			record := &cloudflareoperatoriov1.DNSRecord{
				ObjectMeta: metav1.ObjectMeta{Name: "dnsrecord", Namespace: testDefaultNamespace},
				Spec:       cloudflareoperatoriov1.DNSRecordSpec{Name: testDNSRecordHost, Content: testIPv4Address, Type: "A", Proxied: new(bool)},
			}
			ip := &cloudflareoperatoriov1.IP{
				ObjectMeta: metav1.ObjectMeta{Name: "ip"},
				Status:     cloudflareoperatoriov1.IPStatus{Address: testAlternateIPv4Address},
			}
			secret, account := newTestAccountObjects(os.Getenv("CF_API_TOKEN"))
			r := &DNSRecordReconciler{Client: fake.NewClientBuilder().WithScheme(newTestScheme()).WithObjects(ip, secret, account).Build()}
			cleanupID := ""
			t.Cleanup(func() {
				id := record.Status.RecordID
				if id == "" {
					id = cleanupID
				}
				if err := deleteCloudflareDNSRecord(context.Background(), api, zone.Status.ID, id); err != nil && !isCloudflareDNSRecordNotFound(err) {
					t.Errorf("clean up DNS record: %v", err)
				}
			})
			expectedContent := testIPv4Address
			if tt.ipRef {
				record.Spec.Content = ""
				record.Spec.IPRef.Name = ip.Name
				expectedContent = testAlternateIPv4Address
			}
			if tt.adopt {
				record.Spec.Name = "adopt.containeroo-test.org"
				existing, err := createCloudflareDNSRecord(ctx, api, zone.Status.ID, record.Spec)
				g.Expect(err).NotTo(HaveOccurred())
				cleanupID = existing.ID
			}
			_, err := r.reconcileDNSRecord(ctx, record, zone)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(conditions.IsReady(record)).To(BeTrue())
			remote, err := getCloudflareDNSRecord(ctx, api, zone.Status.ID, record.Status.RecordID)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(remote.Content).To(Equal(expectedContent))
			if tt.ipRef {
				g.Expect(record.Spec.Content).To(BeEmpty())
			}
			if tt.adopt {
				g.Expect(record.Status.RecordID).To(Equal(cleanupID))
			}
			g.Expect(r.reconcileDelete(ctx, zone, record)).To(Succeed())
			_, err = getCloudflareDNSRecord(ctx, api, zone.Status.ID, remote.ID)
			g.Expect(isCloudflareDNSRecordNotFound(err)).To(BeTrue())
		})
	}
}

func TestCompareDNSRecord(t *testing.T) {
	spec := cloudflareoperatoriov1.DNSRecordSpec{
		Name: testDNSRecordHost, Type: "A", Content: testIPv4Address, TTL: 1,
		Proxied: nil, Priority: new(uint16(10)),
		Data: &v1.JSON{Raw: []byte(`{"key":"value"}`)}, Comment: "comment",
	}
	tests := []struct {
		name   string
		change func(*dns.RecordResponse)
		want   bool
	}{
		{name: "equal with default proxied", want: true},
		{name: "changed record name", change: func(r *dns.RecordResponse) { r.Name = testAlternateDNSRecordHost }},
		{name: "type", change: func(r *dns.RecordResponse) { r.Type = "AAAA" }},
		{name: "content", change: func(r *dns.RecordResponse) { r.Content = testAlternateIPv4Address }},
		{name: "ttl", change: func(r *dns.RecordResponse) { r.TTL = 120 }},
		{name: "proxied", change: func(r *dns.RecordResponse) { r.Proxied = false }},
		{name: "irrelevant priority is ignored", change: func(r *dns.RecordResponse) { r.Priority = 20 }, want: true},
		{name: "data", change: func(r *dns.RecordResponse) { r.Data = map[string]any{"key": "other"} }},
		{name: "missing data", change: func(r *dns.RecordResponse) { r.Data = nil }},
		{name: "changed comment", change: func(r *dns.RecordResponse) { r.Comment = "other" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			remote := dns.RecordResponse{
				Name: testDNSRecordHost, Type: "A", Content: testIPv4Address, TTL: 1,
				Proxied: true, Priority: 10, Data: map[string]any{"key": "value"}, Comment: "comment",
			}
			if tt.change != nil {
				tt.change(&remote)
			}
			g := NewWithT(t)
			g.Expect((&DNSRecordReconciler{}).compareDNSRecord(spec, remote)).To(Equal(tt.want))
		})
	}
}

func TestFindExistingRecordForAdoption(t *testing.T) {
	desired := cloudflareoperatoriov1.DNSRecordSpec{Name: testDNSRecordHost, Type: "A", Content: testAlternateIPv4Address}
	match := dns.RecordResponse{ID: "matching-record", Name: testDNSRecordHost, Type: "A", Content: testAlternateIPv4Address}
	wrongName, wrongType, wrongContent := match, match, match
	wrongName.ID, wrongName.Name = "wrong-name", testAlternateDNSRecordHost
	wrongType.ID, wrongType.Type = "wrong-type", "AAAA"
	wrongContent.ID, wrongContent.Content = "wrong-content", testIPv4Address
	tests := []struct {
		name      string
		records   []dns.RecordResponse
		want      string
		wantError bool
	}{
		{name: "no candidates"},
		{name: "different content creates a separate record", records: []dns.RecordResponse{wrongContent}},
		{name: "match name type and resolved content", records: []dns.RecordResponse{wrongName, wrongType, wrongContent, match}, want: "matching-record"},
		{name: "ambiguous candidates", records: []dns.RecordResponse{match, match}, wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			record, err := findExistingRecordForAdoption(desired, tt.records, nil)
			if tt.wantError {
				g.Expect(err).To(MatchError(ContainSubstring("multiple Cloudflare records matched")))
			} else {
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(record.ID).To(Equal(tt.want))
			}
		})
	}
}

func TestFindZoneForDNSRecord(t *testing.T) {
	g := NewWithT(t)

	zones := []cloudflareoperatoriov1.Zone{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "root"},
			Spec:       cloudflareoperatoriov1.ZoneSpec{Name: regressionZoneName},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "sub"},
			Spec:       cloudflareoperatoriov1.ZoneSpec{Name: "tst.example.com"},
		},
	}

	zone := findZoneForDNSRecord("podinfo.tst.example.com", zones)
	g.Expect(zone).ToNot(BeNil())
	g.Expect(zone.Spec.Name).To(Equal("tst.example.com"))

	zone = findZoneForDNSRecord("foo.example.com", zones)
	g.Expect(zone).ToNot(BeNil())
	g.Expect(zone.Spec.Name).To(Equal(regressionZoneName))

	zone = findZoneForDNSRecord("no.match.test", zones)
	g.Expect(zone).To(BeNil())
}

func TestDNSRecordReconciler_reconcileDelete(t *testing.T) {
	g := NewWithT(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if (req.Method != http.MethodDelete && req.Method != http.MethodGet) || req.URL.Path != "/zones/zone-id/dns_records/record-id" {
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
		}
		if req.Header.Get("Authorization") != "Bearer zone-token" {
			t.Errorf("unexpected authorization: %q", req.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"result":{"id":"record-id"}}`))
	}))
	t.Cleanup(server.Close)
	t.Setenv("CLOUDFLARE_BASE_URL", server.URL)
	secret, account := newTestAccountObjects("zone-token")
	otherAccount := &cloudflareoperatoriov1.Account{ObjectMeta: metav1.ObjectMeta{Name: "unrelated-account"}}
	zone := &cloudflareoperatoriov1.Zone{
		Spec:   cloudflareoperatoriov1.ZoneSpec{AccountRef: cloudflareoperatoriov1.AccountRef{Name: account.Name}},
		Status: cloudflareoperatoriov1.ZoneStatus{ID: testRemoteZoneID},
	}
	record := &cloudflareoperatoriov1.DNSRecord{
		ObjectMeta: metav1.ObjectMeta{Finalizers: []string{cloudflareoperatoriov1.CloudflareOperatorFinalizer}},
		Status:     cloudflareoperatoriov1.DNSRecordStatus{RecordID: "record-id"},
	}
	// The resolved Zone is supplied by the caller, not looked up in the client.
	r := &DNSRecordReconciler{Client: fake.NewClientBuilder().WithScheme(newTestScheme()).WithObjects(secret, account, otherAccount).Build()}
	g.Expect(r.reconcileDelete(t.Context(), zone, record)).To(Succeed())
	g.Expect(record.Finalizers).To(BeEmpty())
}
