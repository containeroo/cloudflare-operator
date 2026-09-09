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
	"os"
	"testing"

	"github.com/cloudflare/cloudflare-go/v7/dns"
	"github.com/fluxcd/pkg/runtime/conditions"
	. "github.com/onsi/gomega"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	cloudflareoperatoriov1 "github.com/containeroo/cloudflare-operator/api/v1"
)

func TestZoneReconciler_reconcileZone(t *testing.T) {
	if os.Getenv("CF_API_TOKEN") == "" || os.Getenv("CF_ZONE_ID") == "" {
		t.Skip("requires CF_API_TOKEN and CF_ZONE_ID for live Cloudflare tests")
	}
	cloudflareAPI := newCloudflareClient(os.Getenv("CF_API_TOKEN"))

	zoneID := os.Getenv("CF_ZONE_ID")
	setup := func() (*ZoneReconciler, *cloudflareoperatoriov1.Zone, *cloudflareoperatoriov1.Account) {
		zone := &cloudflareoperatoriov1.Zone{
			ObjectMeta: metav1.ObjectMeta{
				Name: "zone",
			},
			Spec: cloudflareoperatoriov1.ZoneSpec{
				Name: "containeroo-test.org",
			},
		}
		secret, account := newTestAccountObjects(os.Getenv("CF_API_TOKEN"))

		r := &ZoneReconciler{
			Client: fake.NewClientBuilder().
				WithScheme(newTestScheme()).
				WithObjects(zone, secret, account).
				Build(),
		}

		return r, zone, account
	}
	createRecord := func(t *testing.T, spec cloudflareoperatoriov1.DNSRecordSpec) dns.RecordResponse {
		t.Helper()
		record, err := createCloudflareDNSRecord(t.Context(), cloudflareAPI, zoneID, spec)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := deleteCloudflareDNSRecord(context.Background(), cloudflareAPI, zoneID, record.ID); err != nil && !isCloudflareDNSRecordNotFound(err) {
				t.Errorf("clean up DNS record: %v", err)
			}
		})
		return record
	}

	t.Run("reconcile zone without prune", func(t *testing.T) {
		g := NewWithT(t)
		r, zone, _ := setup()

		testRecord := createRecord(t, cloudflareoperatoriov1.DNSRecordSpec{
			Name: "test.containeroo-test.org", Content: testIPv4Address, Type: "A", Proxied: new(bool),
		})
		zone.Spec.Prune = false

		_, err := r.reconcileZone(context.TODO(), zone)
		g.Expect(err).ToNot(HaveOccurred())

		g.Expect(zone.Status.Conditions).To(conditions.MatchConditions([]metav1.Condition{
			*conditions.TrueCondition(cloudflareoperatoriov1.ConditionTypeReady, cloudflareoperatoriov1.ConditionReasonReady, "Zone is ready"),
		}))
		g.Expect(zone.Status.ID).To(Equal(zoneID))

		_, err = getCloudflareDNSRecord(context.TODO(), cloudflareAPI, zoneID, testRecord.ID)
		g.Expect(err).ToNot(HaveOccurred())
	})

	t.Run("reconcile zone with prune", func(t *testing.T) {
		g := NewWithT(t)
		r, zone, _ := setup()

		testRecord := createRecord(t, cloudflareoperatoriov1.DNSRecordSpec{
			Name: "test.containeroo-test.org", Content: testIPv4Address, Type: "A", Proxied: new(bool),
		})
		zone.Spec.Prune = true
		zone.Spec.IgnoredRecords = map[string][]string{
			testRecordTypeTXT: {"_acme-challenge", "cf2024-1._domainkey"},
			"A":               {"^mytest.*$"},
		}

		acmeRecord := createRecord(t, cloudflareoperatoriov1.DNSRecordSpec{
			Name:    "_acme-challenge.abc.containeroo-test.org",
			Type:    testRecordTypeTXT,
			Content: "test",
			Proxied: new(bool),
		})
		dkimRecord := createRecord(t, cloudflareoperatoriov1.DNSRecordSpec{
			Name:    "cf2024-1._domainkey.containeroo-test.org",
			Type:    testRecordTypeTXT,
			Content: "test",
			Proxied: new(bool),
		})
		aRecord := createRecord(t, cloudflareoperatoriov1.DNSRecordSpec{
			Name:    "mytestabc.containeroo-test.org",
			Type:    "A",
			Content: testIPv4Address,
			Proxied: new(bool),
		})

		_, err := r.reconcileZone(context.TODO(), zone)
		g.Expect(err).NotTo(HaveOccurred())

		_, err = getCloudflareDNSRecord(context.TODO(), cloudflareAPI, zone.Status.ID, testRecord.ID)
		g.Expect(isCloudflareDNSRecordNotFound(err)).To(BeTrue())

		_, err = getCloudflareDNSRecord(context.TODO(), cloudflareAPI, zone.Status.ID, acmeRecord.ID)
		g.Expect(err).ToNot(HaveOccurred())
		_, err = getCloudflareDNSRecord(context.TODO(), cloudflareAPI, zone.Status.ID, dkimRecord.ID)
		g.Expect(err).ToNot(HaveOccurred())
		_, err = getCloudflareDNSRecord(context.TODO(), cloudflareAPI, zone.Status.ID, aRecord.ID)
		g.Expect(err).ToNot(HaveOccurred())
	})

	t.Run("reconcile zone error zone not found", func(t *testing.T) {
		g := NewWithT(t)
		r, zone, _ := setup()

		zone.Spec.Name = "not-found.org"

		_, err := r.reconcileZone(context.TODO(), zone)
		g.Expect(err).To(HaveOccurred())

		g.Expect(zone.Status.Conditions).To(conditions.MatchConditions([]metav1.Condition{
			*conditions.FalseCondition(cloudflareoperatoriov1.ConditionTypeReady, cloudflareoperatoriov1.ConditionReasonFailed, "zone could not be found"),
		}))
	})

	t.Run("reconcile zone error account not ready", func(t *testing.T) {
		g := NewWithT(t)
		r, zone, account := setup()

		err := r.Delete(context.TODO(), account)
		g.Expect(err).ToNot(HaveOccurred())

		_, err = r.reconcileZone(context.TODO(), zone)
		g.Expect(err).To(Equal(errWaitForAccount))

		g.Expect(zone.Status.Conditions).To(conditions.MatchConditions([]metav1.Condition{
			*conditions.UnknownCondition(cloudflareoperatoriov1.ConditionTypeReady, cloudflareoperatoriov1.ConditionReasonNotReady, "Cloudflare account is not ready"),
		}))
	})
}

func TestManagedDNSRecordKeysForZone(t *testing.T) {
	g := NewWithT(t)

	rootZone := cloudflareoperatoriov1.Zone{
		ObjectMeta: metav1.ObjectMeta{Name: "root"},
		Spec:       cloudflareoperatoriov1.ZoneSpec{Name: regressionZoneName},
	}
	subZone := cloudflareoperatoriov1.Zone{
		ObjectMeta: metav1.ObjectMeta{Name: "sub"},
		Spec:       cloudflareoperatoriov1.ZoneSpec{Name: "apps.example.com"},
	}

	managedByRecordID := cloudflareoperatoriov1.DNSRecord{
		Spec: cloudflareoperatoriov1.DNSRecordSpec{
			Name: regressionDNSName,
			Type: "A",
		},
		Status: cloudflareoperatoriov1.DNSRecordStatus{
			RecordID: "record-id",
		},
	}
	adoptableSubZoneRecord := cloudflareoperatoriov1.DNSRecord{
		Spec: cloudflareoperatoriov1.DNSRecordSpec{
			Name: "api.apps.example.com",
			Type: "A",
		},
	}

	recordIDs, specKeys := managedDNSRecordKeysForZone(&rootZone, []cloudflareoperatoriov1.DNSRecord{
		managedByRecordID,
		adoptableSubZoneRecord,
	}, []cloudflareoperatoriov1.Zone{rootZone, subZone})

	_, hasRecordID := recordIDs["record-id"]
	g.Expect(hasRecordID).To(BeTrue())

	_, protectsRootZoneRecord := specKeys[dnsRecordKey("A", regressionDNSName)]
	g.Expect(protectsRootZoneRecord).To(BeFalse())

	_, protectsSubZoneRecord := specKeys[dnsRecordKey("A", "api.apps.example.com")]
	g.Expect(protectsSubZoneRecord).To(BeFalse())

	recordIDs, specKeys = managedDNSRecordKeysForZone(&subZone, []cloudflareoperatoriov1.DNSRecord{
		managedByRecordID,
		adoptableSubZoneRecord,
	}, []cloudflareoperatoriov1.Zone{rootZone, subZone})

	g.Expect(recordIDs).To(BeEmpty())
	_, protectsAdoptableRecord := specKeys[dnsRecordKey("A", "api.apps.example.com")]
	g.Expect(protectsAdoptableRecord).To(BeTrue())
}
