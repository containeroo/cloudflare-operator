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

	"github.com/cloudflare/cloudflare-go"
	"github.com/fluxcd/pkg/runtime/conditions"
	. "github.com/onsi/gomega"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	cloudflareoperatoriov1 "github.com/containeroo/cloudflare-operator/api/v1"
)

func TestZoneReconciler_reconcileZone(t *testing.T) {
	g := NewWithT(t)

	apiToken := os.Getenv("CF_API_TOKEN")
	g.Expect(apiToken).ToNot(BeEmpty(), "CF_API_TOKEN must be set for this test")

	cloudflareAPI, err := cloudflare.NewWithAPIToken(apiToken)
	g.Expect(err).ToNot(HaveOccurred())

	zone := &cloudflareoperatoriov1.Zone{
		ObjectMeta: metav1.ObjectMeta{
			Name: "zone",
		},
		Spec: cloudflareoperatoriov1.ZoneSpec{
			Name: "containeroo-test.org",
		},
	}

	accountManager := NewAccountManager()
	accountManager.UpsertAccount("account", cloudflareAPI, apiToken, []string{zone.Spec.Name})

	r := &ZoneReconciler{
		Client: fake.NewClientBuilder().
			WithScheme(NewTestScheme()).
			WithObjects(zone).
			Build(),
		AccountManager: accountManager,
	}

	zoneID := os.Getenv("CF_ZONE_ID")
	g.Expect(zoneID).ToNot(BeEmpty(), "CF_ZONE_ID must be set for this test")

	var testRecord cloudflare.DNSRecord

	t.Run("create dns record for testing", func(t *testing.T) {
		g := NewWithT(t)

		var err error
		testRecord, err = cloudflareAPI.CreateDNSRecord(context.TODO(), cloudflare.ZoneIdentifier(zoneID), cloudflare.CreateDNSRecordParams{
			Name:    "test.containeroo-test.org",
			Content: "1.1.1.1",
			Type:    "A",
		})
		g.Expect(err).ToNot(HaveOccurred())
	})

	t.Run("reconcile zone without prune", func(t *testing.T) {
		g := NewWithT(t)

		zone.Spec.Prune = false

		_, err := r.reconcileZone(context.TODO(), zone)
		g.Expect(err).ToNot(HaveOccurred())

		g.Expect(zone.Status.Conditions).To(conditions.MatchConditions([]metav1.Condition{
			*conditions.TrueCondition(cloudflareoperatoriov1.ConditionTypeReady, cloudflareoperatoriov1.ConditionReasonReady, "Zone is ready"),
		}))
		g.Expect(zone.Status.ID).To(Equal(zoneID))

		_, err = cloudflareAPI.GetDNSRecord(context.TODO(), cloudflare.ZoneIdentifier(zoneID), testRecord.ID)
		g.Expect(err).ToNot(HaveOccurred())
	})

	t.Run("reconcile zone with prune", func(t *testing.T) {
		g := NewWithT(t)

		zone.Spec.Prune = true
		zone.Spec.IgnoredRecords = map[string][]string{
			"TXT": {"_acme-challenge", "cf2024-1._domainkey"},
			"A":   {"^mytest.*$"},
		}

		acmeRecord, err := cloudflareAPI.CreateDNSRecord(context.TODO(), cloudflare.ZoneIdentifier(zoneID), cloudflare.CreateDNSRecordParams{
			Name:    "_acme-challenge.abc.containeroo-test.org",
			Type:    "TXT",
			Content: "test",
		})
		g.Expect(err).ToNot(HaveOccurred())
		dkimRecord, err := cloudflareAPI.CreateDNSRecord(context.TODO(), cloudflare.ZoneIdentifier(zoneID), cloudflare.CreateDNSRecordParams{
			Name:    "cf2024-1._domainkey.containeroo-test.org",
			Type:    "TXT",
			Content: "test",
		})
		g.Expect(err).ToNot(HaveOccurred())
		aRecord, err := cloudflareAPI.CreateDNSRecord(context.TODO(), cloudflare.ZoneIdentifier(zoneID), cloudflare.CreateDNSRecordParams{
			Name:    "mytestabc.containeroo-test.org",
			Type:    "A",
			Content: "1.1.1.1",
		})
		g.Expect(err).ToNot(HaveOccurred())

		_, _ = r.reconcileZone(context.TODO(), zone)

		_, err = cloudflareAPI.GetDNSRecord(context.TODO(), cloudflare.ZoneIdentifier(zone.Status.ID), testRecord.ID)
		g.Expect(err.Error()).To(ContainSubstring("Record does not exist"))

		_, err = cloudflareAPI.GetDNSRecord(context.TODO(), cloudflare.ZoneIdentifier(zone.Status.ID), acmeRecord.ID)
		g.Expect(err).ToNot(HaveOccurred())
		_, err = cloudflareAPI.GetDNSRecord(context.TODO(), cloudflare.ZoneIdentifier(zone.Status.ID), dkimRecord.ID)
		g.Expect(err).ToNot(HaveOccurred())
		_, err = cloudflareAPI.GetDNSRecord(context.TODO(), cloudflare.ZoneIdentifier(zone.Status.ID), aRecord.ID)
		g.Expect(err).ToNot(HaveOccurred())

		err = cloudflareAPI.DeleteDNSRecord(context.TODO(), cloudflare.ZoneIdentifier(zoneID), acmeRecord.ID)
		g.Expect(err).ToNot(HaveOccurred())
		err = cloudflareAPI.DeleteDNSRecord(context.TODO(), cloudflare.ZoneIdentifier(zoneID), dkimRecord.ID)
		g.Expect(err).ToNot(HaveOccurred())
		err = cloudflareAPI.DeleteDNSRecord(context.TODO(), cloudflare.ZoneIdentifier(zoneID), aRecord.ID)
		g.Expect(err).ToNot(HaveOccurred())
	})

	t.Run("reconcile zone error zone not found", func(t *testing.T) {
		g := NewWithT(t)

		zone.Spec.Name = "not-found.org"
		accountManager.UpsertAccount("account", cloudflareAPI, apiToken, []string{zone.Spec.Name})

		_, err := r.reconcileZone(context.TODO(), zone)
		g.Expect(err).To(HaveOccurred())

		g.Expect(zone.Status.Conditions).To(conditions.MatchConditions([]metav1.Condition{
			*conditions.FalseCondition(cloudflareoperatoriov1.ConditionTypeReady, cloudflareoperatoriov1.ConditionReasonFailed, "zone could not be found"),
		}))
	})

	t.Run("reconcile zone error account not ready", func(t *testing.T) {
		g := NewWithT(t)

		accountManager.RemoveAccount("account")

		_, err := r.reconcileZone(context.TODO(), zone)
		g.Expect(err).To(Equal(errWaitForAccount))

		g.Expect(zone.Status.Conditions).To(conditions.MatchConditions([]metav1.Condition{
			*conditions.FalseCondition(cloudflareoperatoriov1.ConditionTypeReady, cloudflareoperatoriov1.ConditionReasonFailed, "no Cloudflare account manages zone \"not-found.org\""),
		}))
	})
}
