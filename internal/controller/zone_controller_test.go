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
	zone := &cloudflareoperatoriov1.Zone{
		ObjectMeta: metav1.ObjectMeta{
			Name: "zone",
		},
		Spec: cloudflareoperatoriov1.ZoneSpec{
			Name: "containeroo-test.org",
		},
	}

	r := &ZoneReconciler{
		Client: fake.NewClientBuilder().
			WithScheme(NewTestScheme()).
			WithObjects(zone).
			Build(),
		Cf: &cf,
	}

	zoneID := os.Getenv("CF_ZONE_ID")
	var testRecord cloudflare.DNSRecord

	t.Run("create dns record for testing", func(t *testing.T) {
		g := NewWithT(t)

		var err error
		testRecord, err = cf.CreateDNSRecord(context.TODO(), cloudflare.ZoneIdentifier(zoneID), cloudflare.CreateDNSRecordParams{
			Name:    "test.containeroo-test.org",
			Content: "1.1.1.1",
			Type:    "A",
		})
		g.Expect(err).ToNot(HaveOccurred())
	})

	t.Run("reconcile zone without prune", func(t *testing.T) {
		g := NewWithT(t)

		zone.Spec.Prune = false

		_ = r.reconcileZone(context.TODO(), zone)

		g.Expect(zone.Status.Conditions).To(conditions.MatchConditions([]metav1.Condition{
			*conditions.TrueCondition(cloudflareoperatoriov1.ConditionTypeReady, cloudflareoperatoriov1.ConditionReasonReady, "Zone is ready"),
		}))
		g.Expect(zone.Status.ID).To(Equal(zoneID))

		_, err := cf.GetDNSRecord(context.TODO(), cloudflare.ZoneIdentifier(zoneID), testRecord.ID)
		g.Expect(err).ToNot(HaveOccurred())
	})

	t.Run("reconcile zone with prune", func(t *testing.T) {
		g := NewWithT(t)

		zone.Spec.Prune = true

		acmeRecord, err := cf.CreateDNSRecord(context.TODO(), cloudflare.ZoneIdentifier(zoneID), cloudflare.CreateDNSRecordParams{
			Name:    "_acme-challenge.abc.containeroo-test.org",
			Type:    "TXT",
			Content: "test",
		})
		g.Expect(err).ToNot(HaveOccurred())
		dkimRecord, err := cf.CreateDNSRecord(context.TODO(), cloudflare.ZoneIdentifier(zoneID), cloudflare.CreateDNSRecordParams{
			Name:    "cf2024-1._domainkey.containeroo-test.org",
			Type:    "TXT",
			Content: "test",
		})
		g.Expect(err).ToNot(HaveOccurred())

		_ = r.reconcileZone(context.TODO(), zone)

		_, err = cf.GetDNSRecord(context.TODO(), cloudflare.ZoneIdentifier(zone.Status.ID), testRecord.ID)
		g.Expect(err.Error()).To(ContainSubstring("Record does not exist"))

		_, err = cf.GetDNSRecord(context.TODO(), cloudflare.ZoneIdentifier(zone.Status.ID), acmeRecord.ID)
		g.Expect(err).ToNot(HaveOccurred())
		_, err = cf.GetDNSRecord(context.TODO(), cloudflare.ZoneIdentifier(zone.Status.ID), dkimRecord.ID)
		g.Expect(err).ToNot(HaveOccurred())

		err = cf.DeleteDNSRecord(context.TODO(), cloudflare.ZoneIdentifier(zoneID), acmeRecord.ID)
		g.Expect(err).ToNot(HaveOccurred())
		err = cf.DeleteDNSRecord(context.TODO(), cloudflare.ZoneIdentifier(zoneID), dkimRecord.ID)
		g.Expect(err).ToNot(HaveOccurred())
	})

	t.Run("reconcile zone error zone not found", func(t *testing.T) {
		g := NewWithT(t)

		zone.Spec.Name = "not-found.org"

		_ = r.reconcileZone(context.TODO(), zone)

		g.Expect(zone.Status.Conditions).To(conditions.MatchConditions([]metav1.Condition{
			*conditions.FalseCondition(cloudflareoperatoriov1.ConditionTypeReady, cloudflareoperatoriov1.ConditionReasonFailed, "zone could not be found"),
		}))
	})

	t.Run("reconcile zone error account not ready", func(t *testing.T) {
		g := NewWithT(t)

		cf.APIToken = ""

		_ = r.reconcileZone(context.TODO(), zone)

		g.Expect(zone.Status.Conditions).To(conditions.MatchConditions([]metav1.Condition{
			*conditions.UnknownCondition(cloudflareoperatoriov1.ConditionTypeReady, cloudflareoperatoriov1.ConditionReasonNotReady, "Cloudflare account is not ready"),
		}))
	})
}
