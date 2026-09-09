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
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cloudflare/cloudflare-go/v7/dns"
	api "github.com/containeroo/cloudflare-operator/api/v1"
	intpredicates "github.com/containeroo/cloudflare-operator/internal/predicates"
	"github.com/fluxcd/pkg/runtime/conditions"
	networkingv1 "k8s.io/api/networking/v1"
	extv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/kube-openapi/pkg/validation/spec"
	"k8s.io/kube-openapi/pkg/validation/strfmt"
	"k8s.io/kube-openapi/pkg/validation/validate"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/yaml"
)

const (
	regressionNewZoneID     = "new-zone"
	regressionDNSName       = "www.example.com"
	regressionZoneName      = "example.com"
	regressionOwnerName     = "owner"
	regressionFirstOwner    = "first"
	regressionSecondOwner   = "second"
	regressionSRVType       = "SRV"
	regressionPriorityField = "priority"
)

func regressionZone() *api.Zone {
	return &api.Zone{ObjectMeta: metav1.ObjectMeta{Name: "zone"}, Spec: api.ZoneSpec{Name: regressionZoneName}, Status: api.ZoneStatus{
		ID: testRemoteZoneID, Conditions: []metav1.Condition{*conditions.TrueCondition("Ready", "Ready", "ready")},
	}}
}

func TestRegressionDeletionEvent(t *testing.T) {
	ip := &api.IP{ObjectMeta: metav1.ObjectMeta{Name: testIPTypeStatic, Generation: 1, Finalizers: []string{api.CloudflareOperatorFinalizer}}, Spec: api.IPSpec{Type: testIPTypeStatic, Address: testIPv4Address}}
	result := (&IPReconciler{}).reconcileIP(t.Context(), ip)
	deleting := ip.DeepCopy()
	now := metav1.Now()
	deleting.DeletionTimestamp = &now
	accepted := (intpredicates.ResourceChanged{}).Update(event.UpdateEvent{ObjectOld: ip, ObjectNew: deleting})
	if !accepted && result.IsZero() {
		t.Fatal("static IP has no timer and the deletionTimestamp update is rejected; finalizer cannot run")
	}
}

func TestRegressionMissingRemoteRecord(t *testing.T) {
	var creates, staleGets int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/deleted-id"):
			staleGets++
			w.WriteHeader(http.StatusNotFound)
			_, _ = fmt.Fprint(w, `{"success":false,"errors":[{"code":81044,"message":"Record does not exist"}]}`)
		case r.Method == http.MethodPost:
			creates++
			_, _ = fmt.Fprint(w, `{"success":true,"result":{"id":"replacement-id"}}`)
		case strings.HasSuffix(r.URL.Path, "/replacement-id"):
			_, _ = fmt.Fprint(w, `{"success":true,"result":{"id":"replacement-id","name":"www.example.com","type":"A","content":"1.1.1.1","ttl":1,"proxied":false}}`)
		default:
			_, _ = fmt.Fprint(w, `{"success":true,"result":[]}`)
		}
	}))
	defer server.Close()
	t.Setenv("CLOUDFLARE_BASE_URL", server.URL)
	secret, account := newTestAccountObjects("token")
	r := &DNSRecordReconciler{Client: fake.NewClientBuilder().WithScheme(newTestScheme()).WithObjects(secret, account).Build(), RetryInterval: time.Second}
	record := &api.DNSRecord{Spec: api.DNSRecordSpec{Name: regressionDNSName, Type: "A", Content: testIPv4Address, TTL: 1, Proxied: new(bool)}, Status: api.DNSRecordStatus{RecordID: "deleted-id"}}
	for i := range 3 {
		if _, err := r.reconcileDNSRecord(t.Context(), record, regressionZone()); err != nil {
			t.Fatal(err)
		}
		if i == 0 && (record.Status.RecordID != "" || creates != 0) {
			t.Fatal("replacement created before clearing the persisted binding")
		}
	}
	if record.Status.RecordID != "replacement-id" || creates != 1 || staleGets != 1 || !conditions.IsReady(record) {
		t.Fatalf("record did not recover and converge: status=%+v creates=%d staleGets=%d", record.Status, creates, staleGets)
	}
	if record.Status.ZoneID != testRemoteZoneID || record.Status.AccountName != account.Name {
		t.Fatalf("remote identity not persisted: %+v", record.Status)
	}
}

func TestRegressionMissingZoneBlocksDeletion(t *testing.T) {
	now := metav1.Now()
	record := &api.DNSRecord{ObjectMeta: metav1.ObjectMeta{Name: "record", Namespace: testDefaultNamespace, Finalizers: []string{api.CloudflareOperatorFinalizer}, DeletionTimestamp: &now}, Spec: api.DNSRecordSpec{Name: regressionDNSName}}
	kube := fake.NewClientBuilder().WithScheme(newTestScheme()).WithStatusSubresource(&api.DNSRecord{}).WithObjects(record).Build()
	r := &DNSRecordReconciler{Client: kube, RetryInterval: time.Second}
	result, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(record)})
	if err != nil {
		t.Fatal(err)
	}
	remaining := &api.DNSRecord{}
	if err := kube.Get(t.Context(), client.ObjectKeyFromObject(record), remaining); err == nil && len(remaining.Finalizers) > 0 {
		t.Fatalf("record that never had a Cloudflare ID is stuck on missing Zone: requeue %v", result.RequeueAfter)
	}
}

func TestRegressionInvalidPruneRegex(t *testing.T) {
	var deleted []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodDelete {
			deleted = append(deleted, r.URL.Path)
			_, _ = fmt.Fprint(w, `{"success":true,"result":{"id":"protected"}}`)
			return
		}
		if r.URL.Query().Get("page") != "" {
			_, _ = fmt.Fprint(w, `{"success":true,"result":[]}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"success":true,"result":[{"id":"protected","type":"TXT","name":"protected.example.com","content":"verification"}],"result_info":{"page":1,"per_page":1000,"count":1,"total_count":1,"total_pages":1}}`)
	}))
	defer server.Close()
	t.Setenv("CLOUDFLARE_BASE_URL", server.URL)
	zone := regressionZone()
	zone.Spec.IgnoredRecords = map[string][]string{"TXT": {"^protected["}}
	r := &ZoneReconciler{Client: fake.NewClientBuilder().WithScheme(newTestScheme()).WithObjects(zone).Build()}
	err := r.handlePrune(t.Context(), newCloudflareClient("token"), zone)
	if err == nil || len(deleted) > 0 {
		t.Fatalf("invalid exclusion failed open: err=%v deleted=%v", err, deleted)
	}
}

func TestRegressionHostDeleteFailure(t *testing.T) {
	owner := &networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: regressionOwnerName, Namespace: testDefaultNamespace, UID: regressionOwnerName}}
	r := newDNSHostTestReconciler(owner)
	if _, err := r.Reconcile(t.Context(), owner, map[string]string{testContentAnnotation: testIPv4Address}, map[string]struct{}{regressionDNSName: {}}); err != nil {
		t.Fatal(err)
	}
	r.Client = interceptor.NewClient(r.Client.(client.WithWatch), interceptor.Funcs{Delete: func(context.Context, client.WithWatch, client.Object, ...client.DeleteOption) error {
		return errors.New("temporary API failure")
	}})
	result, err := r.Reconcile(t.Context(), owner, nil, nil)
	if err == nil && result.IsZero() {
		t.Fatal("DNSRecord delete failed but reconciliation reports success with no retry")
	}
}

func TestRegressionHostNameCollision(t *testing.T) {
	owner := &networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: regressionOwnerName, Namespace: testDefaultNamespace, UID: regressionOwnerName}}
	r := newDNSHostTestReconciler(owner)
	result, err := r.Reconcile(t.Context(), owner, map[string]string{testContentAnnotation: testIPv4Address}, map[string]struct{}{"a.b.example.com": {}, "a-b.example.com": {}})
	records := &api.DNSRecordList{}
	if e := r.List(t.Context(), records); e != nil {
		t.Fatal(e)
	}
	if len(records.Items) != 2 {
		t.Fatalf("distinct valid hosts collide: got %d records, err=%v, retry=%v", len(records.Items), err, result.RequeueAfter)
	}
}

func TestRegressionSharedHostOwnership(t *testing.T) {
	owner := &networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: regressionFirstOwner, Namespace: testDefaultNamespace, UID: regressionFirstOwner}}
	second := owner.DeepCopy()
	second.Name = regressionSecondOwner
	second.UID = regressionSecondOwner
	r := newDNSHostTestReconciler(owner, second)
	annotations := map[string]string{testContentAnnotation: testIPv4Address}
	hosts := map[string]struct{}{regressionDNSName: {}}
	if result, err := r.Reconcile(t.Context(), owner, annotations, hosts); err != nil || !result.IsZero() {
		t.Fatalf("unexpected reconcile result: %v, %v", result, err)
	}
	if result, err := r.Reconcile(t.Context(), second, annotations, hosts); err != nil || !result.IsZero() {
		t.Fatalf("unexpected reconcile result: %v, %v", result, err)
	}
	if result, err := r.Reconcile(t.Context(), owner, nil, nil); err != nil || !result.IsZero() {
		t.Fatalf("unexpected reconcile result: %v, %v", result, err)
	}
	records := &api.DNSRecordList{}
	if err := r.List(t.Context(), records); err != nil {
		t.Fatal(err)
	}
	if len(records.Items) == 0 {
		t.Fatal("removing first owner's annotations deletes shared host despite second owner still requesting it")
	}
}

func TestRegressionStructuredAdoption(t *testing.T) {
	spec := api.DNSRecordSpec{Name: "_service._tcp.example.com", Type: regressionSRVType, Data: &extv1.JSON{Raw: []byte(`{"target":"wanted.example.com","port":443,"priority":10,"weight":1}`)}}
	record, err := findExistingRecordForAdoption(spec, []dns.RecordResponse{
		{ID: "wrong", Name: spec.Name, Type: regressionSRVType, Data: map[string]any{"target": "other.example.com", "port": 443, regressionPriorityField: 10, "weight": 1}},
		{ID: "correct", Name: spec.Name, Type: regressionSRVType, Data: map[string]any{"target": "wanted.example.com", "port": 443, regressionPriorityField: 10, "weight": 1}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if record.ID != "correct" {
		t.Fatalf("adopted %q instead of matching structured data", record.ID)
	}
}

func TestRegressionPriorityRemoval(t *testing.T) {
	desired := api.DNSRecordSpec{Name: regressionZoneName, Type: "MX", Content: "mail.example.com", TTL: 1, Proxied: new(bool)}
	existing := dns.RecordResponse{Name: desired.Name, Type: "MX", Content: desired.Content, TTL: 1, Priority: 10}
	if (&DNSRecordReconciler{}).compareDNSRecord(desired, existing) {
		t.Fatal("fixture should need update")
	}
	body, err := editCloudflareDNSRecordBody(desired)
	if err != nil {
		t.Fatal(err)
	}
	if !body.Priority.Present {
		t.Fatal("comparison requires priority=0, but PATCH omits priority and cannot converge")
	}
}

func TestRegressionCanceledIPRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = fmt.Fprint(w, testIPv4Address) }))
	defer server.Close()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	result, err := (&IPReconciler{HTTPClientTimeout: time.Second}).getIPSource(ctx, api.IPSpecIPSources{URL: server.URL})
	if err == nil {
		t.Fatalf("canceled reconcile still fetched %q", result)
	}
}

func TestRegressionZeroIPInterval(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = fmt.Fprint(w, testIPv4Address) }))
	defer server.Close()
	ip := &api.IP{Spec: api.IPSpec{Type: "dynamic", Interval: &metav1.Duration{}, IPSources: []api.IPSpecIPSources{{URL: server.URL}}}}
	result := (&IPReconciler{DefaultReconcileInterval: time.Minute, HTTPClientTimeout: time.Second}).reconcileIP(t.Context(), ip)
	if conditions.IsReady(ip) && result.IsZero() {
		t.Fatal("explicit 0s interval is accepted as Ready but stops polling")
	}
}

func TestRegressionZoneMove(t *testing.T) {
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"success":true,"result":{"id":"old-record"}}`)
	}))
	defer server.Close()
	t.Setenv("CLOUDFLARE_BASE_URL", server.URL)
	secret, account := newTestAccountObjects("token")
	r := &DNSRecordReconciler{Client: fake.NewClientBuilder().WithScheme(newTestScheme()).WithObjects(secret, account).Build()}
	record := &api.DNSRecord{Spec: api.DNSRecordSpec{Name: "www.new.example.com"}, Status: api.DNSRecordStatus{RecordID: "old-record", ZoneID: "old-zone", AccountName: account.Name}}
	zone := regressionZone()
	zone.Status.ID = regressionNewZoneID
	if err := r.reconcileDelete(t.Context(), zone, record); err != nil {
		t.Fatal(err)
	}
	if path != "/zones/old-zone/dns_records/old-record" {
		t.Fatalf("record identity combines old record ID with newly selected zone: DELETE %s", path)
	}
}

func TestRegressionDuplicateRemoteOwnership(t *testing.T) {
	var patches int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			_, _ = fmt.Fprint(w, `{"success":true,"result":{"id":"separate-id"}}`)
			return
		}
		if r.Method == http.MethodPatch {
			patches++
			_, _ = fmt.Fprint(w, `{"success":true,"result":{"id":"shared-id"}}`)
			return
		}
		if r.URL.Query().Get("page") != "" {
			_, _ = fmt.Fprint(w, `{"success":true,"result":[]}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"success":true,"result":[{"id":"shared-id","name":"www.example.com","type":"A","content":"1.1.1.1","ttl":1,"proxied":false}]}`)
	}))
	defer server.Close()
	t.Setenv("CLOUDFLARE_BASE_URL", server.URL)
	secret, account := newTestAccountObjects("token")
	first := &api.DNSRecord{ObjectMeta: metav1.ObjectMeta{Name: regressionFirstOwner, Namespace: testDefaultNamespace}, Spec: api.DNSRecordSpec{Name: regressionDNSName, Type: "A", Content: testIPv4Address, TTL: 1, Proxied: new(bool)}, Status: api.DNSRecordStatus{RecordID: "shared-id"}}
	second := first.DeepCopy()
	second.Name = regressionSecondOwner
	second.Spec.Content = "2.2.2.2"
	second.Status = api.DNSRecordStatus{}
	r := &DNSRecordReconciler{Client: fake.NewClientBuilder().WithScheme(newTestScheme()).WithObjects(first, second, secret, account).Build()}
	_, err := r.reconcileDNSRecord(t.Context(), second, regressionZone())
	if err != nil {
		t.Fatal(err)
	}
	if second.Status.RecordID != "separate-id" || patches != 0 || !conditions.IsReady(second) {
		t.Fatalf("second DNSRecord did not create its own record: status=%+v patches=%d", second.Status, patches)
	}
}

func TestRegressionDNSNameNormalization(t *testing.T) {
	zones := []api.Zone{*regressionZone()}
	for _, host := range []string{"WWW.EXAMPLE.COM", "www.example.com."} {
		if zone := findZoneForDNSRecord(host, zones); zone == nil {
			t.Errorf("valid DNS name %q does not resolve to example.com Zone", host)
		}
	}
}

func TestRegressionInvalidIntervalSchema(t *testing.T) {
	raw, err := os.ReadFile("../../config/crd/bases/cloudflare-operator.io_ips.yaml")
	if err != nil {
		t.Fatal(err)
	}
	crd := &extv1.CustomResourceDefinition{}
	if err = yaml.Unmarshal(raw, crd); err != nil {
		t.Fatal(err)
	}
	schema := &spec.Schema{}
	schemaJSON, _ := json.Marshal(crd.Spec.Versions[0].Schema.OpenAPIV3Schema)
	if err = json.Unmarshal(schemaJSON, schema); err != nil {
		t.Fatal(err)
	}
	validator := validate.NewSchemaValidator(schema, nil, "", strfmt.Default)
	obj := map[string]any{"apiVersion": "cloudflare-operator.io/v1", "kind": "IP", "metadata": map[string]any{testJSONNameField: "broken"}, "spec": map[string]any{"type": "dynamic", "interval": "tomorrow"}}
	errs := validator.Validate(obj).Errors
	if len(errs) > 0 {
		t.Logf("schema correctly rejected malformed duration: %v", errs)
		return
	}
	payload, _ := json.Marshal(map[string]any{"items": []any{obj}})
	var list api.IPList
	if err = json.Unmarshal(payload, &list); err != nil {
		t.Fatalf("CRD admits interval=tomorrow but typed IPList cannot decode it: %v", err)
	}
}

func TestRegressionPruneUnmanagedDuplicate(t *testing.T) {
	var deleted []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodDelete {
			deleted = append(deleted, r.URL.Path)
			_, _ = fmt.Fprint(w, `{"success":true,"result":{"id":"stale"}}`)
			return
		}
		if r.URL.Query().Get("page") != "" {
			_, _ = fmt.Fprint(w, `{"success":true,"result":[]}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"success":true,"result":[{"id":"managed","name":"www.example.com","type":"A","content":"1.1.1.1"},{"id":"stale","name":"www.example.com","type":"A","content":"2.2.2.2"}]}`)
	}))
	defer server.Close()
	t.Setenv("CLOUDFLARE_BASE_URL", server.URL)
	zone := regressionZone()
	record := &api.DNSRecord{ObjectMeta: metav1.ObjectMeta{Name: "record", Namespace: testDefaultNamespace}, Spec: api.DNSRecordSpec{Name: regressionDNSName, Type: "A", Content: testIPv4Address}, Status: api.DNSRecordStatus{RecordID: "managed"}}
	r := &ZoneReconciler{Client: fake.NewClientBuilder().WithScheme(newTestScheme()).WithObjects(zone, record).Build()}
	if err := r.handlePrune(t.Context(), newCloudflareClient("token"), zone); err != nil {
		t.Fatal(err)
	}
	if len(deleted) == 0 {
		t.Fatal("unmanaged record stale kept solely because its name/type matches managed record")
	}
}

func TestDNSRecordDeletionAfterZoneRemoval(t *testing.T) {
	var deletes int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/zones/zone-id/dns_records/bound-id" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		deletes++
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"success":true,"result":{"id":"bound-id"}}`)
	}))
	defer server.Close()
	t.Setenv("CLOUDFLARE_BASE_URL", server.URL)
	secret, account := newTestAccountObjects("token")
	now := metav1.Now()
	record := &api.DNSRecord{ObjectMeta: metav1.ObjectMeta{Name: "bound", Namespace: testDefaultNamespace, Finalizers: []string{api.CloudflareOperatorFinalizer}, DeletionTimestamp: &now},
		Spec: api.DNSRecordSpec{Name: regressionDNSName}, Status: api.DNSRecordStatus{RecordID: testBoundRecordID, ZoneID: testRemoteZoneID, AccountName: account.Name}}
	kube := fake.NewClientBuilder().WithScheme(newTestScheme()).WithStatusSubresource(&api.DNSRecord{}).WithObjects(secret, account, record).Build()
	r := &DNSRecordReconciler{Client: kube}
	if _, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(record)}); err != nil {
		t.Fatal(err)
	}
	if deletes != 1 {
		t.Fatalf("remote record not deleted: %d calls", deletes)
	}
	if err := kube.Get(t.Context(), client.ObjectKeyFromObject(record), record); !apierrors.IsNotFound(err) {
		t.Fatalf("record not finalized: %v", err)
	}
}

func TestSharedHostUpdatesRequireAgreement(t *testing.T) {
	owner := &networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: regressionFirstOwner, Namespace: testDefaultNamespace, UID: regressionFirstOwner, Annotations: map[string]string{testContentAnnotation: testIPv4Address}}}
	other := owner.DeepCopy()
	other.Name = regressionSecondOwner
	other.UID = regressionSecondOwner
	r := newDNSHostTestReconciler(owner, other)
	hosts := map[string]struct{}{regressionDNSName: {}}
	for _, o := range []*networkingv1.Ingress{owner, other} {
		if result, err := r.Reconcile(t.Context(), o, o.Annotations, hosts); err != nil || !result.IsZero() {
			t.Fatalf("creating shared record: %v %v", result, err)
		}
	}
	owner.Annotations[testContentAnnotation] = testAlternateIPv4Address
	if err := r.Update(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	result, err := r.Reconcile(t.Context(), owner, owner.Annotations, hosts)
	if err == nil && result.IsZero() {
		t.Fatal("conflicting change should retry")
	}
	var record api.DNSRecord
	key := client.ObjectKey{Namespace: testDefaultNamespace, Name: dnsRecordResourceName(regressionDNSName)}
	if err := r.Get(t.Context(), key, &record); err != nil {
		t.Fatal(err)
	}
	if record.Spec.Content != testIPv4Address {
		t.Fatal("conflicting change overwrote shared DNS")
	}
	other.Annotations[testContentAnnotation] = testAlternateIPv4Address
	if err := r.Update(t.Context(), other); err != nil {
		t.Fatal(err)
	}
	if result, err := r.Reconcile(t.Context(), other, other.Annotations, hosts); err != nil || !result.IsZero() {
		t.Fatalf("agreed change did not converge: %v %v", result, err)
	}
	if err := r.Get(t.Context(), key, &record); err != nil {
		t.Fatal(err)
	}
	if record.Spec.Content != testAlternateIPv4Address {
		t.Fatal("agreed change not applied")
	}
}

func TestDNSRecordAuthenticationErrorPreservesBinding(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/bound-id") {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = fmt.Fprint(w, `{"success":false,"errors":[{"code":10000,"message":"Authentication error"}]}`)
	}))
	defer server.Close()
	t.Setenv("CLOUDFLARE_BASE_URL", server.URL)
	secret, account := newTestAccountObjects("token")
	r := &DNSRecordReconciler{Client: fake.NewClientBuilder().WithScheme(newTestScheme()).WithObjects(secret, account).Build(), RetryInterval: time.Second}
	record := &api.DNSRecord{Spec: api.DNSRecordSpec{Name: regressionDNSName}, Status: api.DNSRecordStatus{RecordID: testBoundRecordID}}
	result, err := r.reconcileDNSRecord(t.Context(), record, regressionZone())
	if err != nil || result.IsZero() || requests != 1 || record.Status.RecordID != testBoundRecordID || conditions.IsReady(record) {
		t.Fatalf("authentication failure lost binding or attempted creation: result=%v err=%v status=%+v requests=%d", result, err, record.Status, requests)
	}
}

func TestDNSRecordMoveUsesOriginalAccountThenNewAccount(t *testing.T) {
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path+" "+r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodDelete:
			_, _ = fmt.Fprint(w, `{"success":true,"result":{"id":"old-record"}}`)
		case http.MethodPost:
			_, _ = fmt.Fprint(w, `{"success":true,"result":{"id":"new-record"}}`)
		default:
			_, _ = fmt.Fprint(w, `{"success":true,"result":[]}`)
		}
	}))
	defer server.Close()
	t.Setenv("CLOUDFLARE_BASE_URL", server.URL)
	secret, account := newTestAccountObjects("old-token")
	newSecret, newAccount := newTestAccountObjects("new-token")
	newSecret.Name = "new-secret"
	newAccount.Name = "new-account"
	newAccount.Spec.ApiToken.SecretRef.Name = newSecret.Name
	r := &DNSRecordReconciler{Client: fake.NewClientBuilder().WithScheme(newTestScheme()).WithObjects(secret, account, newSecret, newAccount).Build(), RetryInterval: time.Second}
	zone := regressionZone()
	zone.Status.ID = regressionNewZoneID
	zone.Spec.AccountRef.Name = newAccount.Name
	record := &api.DNSRecord{Spec: api.DNSRecordSpec{Name: regressionDNSName, Type: "A", Content: testIPv4Address, TTL: 1, Proxied: new(bool)}, Status: api.DNSRecordStatus{RecordID: "old-record", ZoneID: "old-zone", AccountName: account.Name}}
	result, err := r.reconcileDNSRecord(t.Context(), record, zone)
	if err != nil || result.IsZero() || record.Status.RecordID != "" || conditions.IsReady(record) {
		t.Fatalf("old binding not cleared before recreation: result=%v err=%v status=%+v", result, err, record.Status)
	}
	if len(calls) != 1 || calls[0] != "DELETE /zones/old-zone/dns_records/old-record Bearer old-token" {
		t.Fatalf("old binding used wrong credentials: %v", calls)
	}
	if _, err := r.reconcileDNSRecord(t.Context(), record, zone); err != nil {
		t.Fatal(err)
	}
	if record.Status.RecordID != "new-record" || record.Status.ZoneID != regressionNewZoneID || record.Status.AccountName != newAccount.Name || !conditions.IsReady(record) {
		t.Fatalf("new binding not ready: %+v", record.Status)
	}
	if calls[len(calls)-1] != "POST /zones/new-zone/dns_records Bearer new-token" {
		t.Fatalf("new binding used wrong credentials: %v", calls)
	}
}

func TestDeletingLegacyDuplicatePreservesRemainingOwner(t *testing.T) {
	now := metav1.Now()
	first := &api.DNSRecord{ObjectMeta: metav1.ObjectMeta{Name: regressionFirstOwner, Namespace: testDefaultNamespace, Finalizers: []string{api.CloudflareOperatorFinalizer}, DeletionTimestamp: &now},
		Status: api.DNSRecordStatus{RecordID: testBoundRecordID, ZoneID: testRemoteZoneID, AccountName: testAccountName}}
	second := first.DeepCopy()
	second.Name = regressionSecondOwner
	second.DeletionTimestamp = nil
	kube := fake.NewClientBuilder().WithScheme(newTestScheme()).WithStatusSubresource(&api.DNSRecord{}).WithObjects(first, second).Build()
	r := &DNSRecordReconciler{Client: kube}
	// No Account exists: cleanup must release the duplicate without any remote call.
	if _, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(first)}); err != nil {
		t.Fatal(err)
	}
	if err := kube.Get(t.Context(), client.ObjectKeyFromObject(first), first); !apierrors.IsNotFound(err) {
		t.Fatalf("duplicate not released: %v", err)
	}
	if err := kube.Get(t.Context(), client.ObjectKeyFromObject(second), second); err != nil {
		t.Fatal(err)
	}
	if second.Status.RecordID != testBoundRecordID {
		t.Fatal("remaining owner's binding changed")
	}
}

func TestPruningReadsOwnershipAfterRemoteSnapshot(t *testing.T) {
	zone := regressionZone()
	record := &api.DNSRecord{ObjectMeta: metav1.ObjectMeta{Name: "recovering", Namespace: testDefaultNamespace},
		Spec: api.DNSRecordSpec{Name: regressionDNSName, Type: "A"}, Status: api.DNSRecordStatus{RecordID: "deleted-id", ZoneID: zone.Status.ID}}
	stale := fake.NewClientBuilder().WithScheme(newTestScheme()).WithObjects(zone, record).Build()
	live := fake.NewClientBuilder().WithScheme(newTestScheme()).WithStatusSubresource(record).WithObjects(zone, record).Build()
	var deletes int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if req.Method == http.MethodDelete {
			deletes++
			_, _ = fmt.Fprint(w, `{"success":true,"result":{"id":"replacement-id"}}`)
			return
		}
		if req.URL.Query().Get("page") != "" {
			_, _ = fmt.Fprint(w, `{"success":true,"result":[]}`)
			return
		}
		// Recovery persists an empty binding before creating the remote replacement.
		pending := &api.DNSRecord{}
		if err := live.Get(t.Context(), client.ObjectKeyFromObject(record), pending); err != nil {
			t.Error(err)
		}
		pending.Status.RecordID = ""
		if err := live.Status().Update(t.Context(), pending); err != nil {
			t.Error(err)
		}
		_, _ = fmt.Fprint(w, `{"success":true,"result":[{"id":"replacement-id","name":"www.example.com","type":"A","content":"1.1.1.1"}]}`)
	}))
	defer server.Close()
	t.Setenv("CLOUDFLARE_BASE_URL", server.URL)
	r := &ZoneReconciler{Client: stale, APIReader: live}
	if err := r.handlePrune(t.Context(), newCloudflareClient("token"), zone); err != nil {
		t.Fatal(err)
	}
	if deletes != 0 {
		t.Fatal("pruning deleted a newly created replacement using stale ownership")
	}
}
