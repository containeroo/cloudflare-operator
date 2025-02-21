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

	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	cloudflareoperatoriov1 "github.com/containeroo/cloudflare-operator/api/v1"
)

func TestDNSRecordReconciler_reconcileDNSRecord(t *testing.T) {
	zone := &cloudflareoperatoriov1.Zone{
		ObjectMeta: metav1.ObjectMeta{
			Name: "zone",
		},
		Spec: cloudflareoperatoriov1.ZoneSpec{
			Name: "containeroo-test.org",
		},
		Status: cloudflareoperatoriov1.ZoneStatus{
			ID: os.Getenv("CF_ZONE_ID"),
			Conditions: []metav1.Condition{{
				Type:    cloudflareoperatoriov1.ConditionTypeReady,
				Status:  metav1.ConditionTrue,
				Reason:  cloudflareoperatoriov1.ConditionReasonReady,
				Message: "Zone is ready",
			}},
		},
	}

	dnsRecord := &cloudflareoperatoriov1.DNSRecord{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "dnsrecord",
			Namespace: "default",
		},
	}

	ip := &cloudflareoperatoriov1.IP{
		ObjectMeta: metav1.ObjectMeta{
			Name: "ip",
		},
		Spec: cloudflareoperatoriov1.IPSpec{
			Address: "2.2.2.2",
		},
	}

	r := &DNSRecordReconciler{
		Client: fake.NewClientBuilder().
			WithScheme(NewTestScheme()).
			WithObjects(dnsRecord, ip).
			Build(),
		Cf: &cf,
	}

	t.Run("reconcile dnsrecord", func(t *testing.T) {
		g := NewWithT(t)
		dnsRecord.Spec = cloudflareoperatoriov1.DNSRecordSpec{
			Name:    "dnstest.containeroo-test.org",
			Content: "1.1.1.1",
			Type:    "A",
			Proxied: new(bool),
		}

		_ = r.reconcileDNSRecord(context.TODO(), dnsRecord, zone)

		g.Expect(dnsRecord.Status.Conditions).To(conditions.MatchConditions([]metav1.Condition{
			*conditions.TrueCondition(cloudflareoperatoriov1.ConditionTypeReady, cloudflareoperatoriov1.ConditionReasonReady, "DNS record synced"),
		}))

		cfDnsRecord, err := cf.GetDNSRecord(context.TODO(), cloudflare.ZoneIdentifier(zone.Status.ID), dnsRecord.Status.RecordID)
		g.Expect(err).ToNot(HaveOccurred())

		g.Expect(dnsRecord.Status.RecordID).To(Equal(cfDnsRecord.ID))

		_ = r.reconcileDelete(context.TODO(), zone.Status.ID, dnsRecord)
		_, err = cf.GetDNSRecord(context.TODO(), cloudflare.ZoneIdentifier(zone.Status.ID), dnsRecord.Status.RecordID)
		g.Expect(err.Error()).To(ContainSubstring("Record does not exist"))
	})

	t.Run("reconcile dnsrecord with ipref", func(t *testing.T) {
		g := NewWithT(t)
		dnsRecord.Status = cloudflareoperatoriov1.DNSRecordStatus{}
		dnsRecord.Spec = cloudflareoperatoriov1.DNSRecordSpec{
			Name:    "dnstest.containeroo-test.org",
			Type:    "A",
			Proxied: new(bool),
			IPRef: cloudflareoperatoriov1.DNSRecordSpecIPRef{
				Name: "ip",
			},
		}

		_ = r.reconcileDNSRecord(context.TODO(), dnsRecord, zone)

		g.Expect(dnsRecord.Status.Conditions).To(conditions.MatchConditions([]metav1.Condition{
			*conditions.TrueCondition(cloudflareoperatoriov1.ConditionTypeReady, cloudflareoperatoriov1.ConditionReasonReady, "DNS record synced"),
		}))

		cfDnsRecord, err := cf.GetDNSRecord(context.TODO(), cloudflare.ZoneIdentifier(zone.Status.ID), dnsRecord.Status.RecordID)
		g.Expect(err).ToNot(HaveOccurred())

		g.Expect(dnsRecord.Status.RecordID).To(Equal(cfDnsRecord.ID))
		g.Expect(cfDnsRecord.Content).To(Equal(ip.Spec.Address))

		_ = r.reconcileDelete(context.TODO(), zone.Status.ID, dnsRecord)
		_, err = cf.GetDNSRecord(context.TODO(), cloudflare.ZoneIdentifier(zone.Status.ID), dnsRecord.Status.RecordID)
		g.Expect(err.Error()).To(ContainSubstring("Record does not exist"))
	})

	t.Run("adopt existing dns record", func(t *testing.T) {
		g := NewWithT(t)
		cfDnsRecord, err := cf.CreateDNSRecord(context.TODO(), cloudflare.ZoneIdentifier(zone.Status.ID), cloudflare.CreateDNSRecordParams{
			Name:    "adopt.containeroo-test.org",
			Type:    "A",
			Content: "1.1.1.1",
			Proxied: new(bool),
		})
		g.Expect(err).ToNot(HaveOccurred())

		dnsRecord.Status = cloudflareoperatoriov1.DNSRecordStatus{}
		dnsRecord.Spec = cloudflareoperatoriov1.DNSRecordSpec{
			Name:    cfDnsRecord.Name,
			Type:    cfDnsRecord.Type,
			Content: cfDnsRecord.Content,
			Proxied: cfDnsRecord.Proxied,
		}

		_ = r.reconcileDNSRecord(context.TODO(), dnsRecord, zone)

		g.Expect(dnsRecord.Status.Conditions).To(conditions.MatchConditions([]metav1.Condition{
			*conditions.TrueCondition(cloudflareoperatoriov1.ConditionTypeReady, cloudflareoperatoriov1.ConditionReasonReady, "DNS record synced"),
		}))

		g.Expect(dnsRecord.Status.RecordID).To(Equal(cfDnsRecord.ID))

		_ = r.reconcileDelete(context.TODO(), zone.Status.ID, dnsRecord)
		_, err = cf.GetDNSRecord(context.TODO(), cloudflare.ZoneIdentifier(zone.Status.ID), dnsRecord.Status.RecordID)
		g.Expect(err.Error()).To(ContainSubstring("Record does not exist"))
	})

	t.Run("compare dns record", func(t *testing.T) {
		g := NewWithT(t)

		dnsRecordSpec := cloudflareoperatoriov1.DNSRecordSpec{
			Name:     "dnstest.containeroo-test.org",
			Type:     "A",
			Content:  "1.1.1.1",
			Proxied:  &[]bool{true}[0],
			Priority: &[]uint16{10}[0],
			Data: &v1.JSON{
				Raw: []byte(`{"key":"value"}`),
			},
		}

		cfDnsRecord := cloudflare.DNSRecord{
			Name:     dnsRecordSpec.Name,
			Type:     dnsRecordSpec.Type,
			Content:  dnsRecordSpec.Content,
			Proxied:  dnsRecordSpec.Proxied,
			Priority: dnsRecordSpec.Priority,
			Data:     map[string]any{"key": "value"},
		}

		isEqual := r.compareDNSRecord(dnsRecordSpec, cfDnsRecord)
		g.Expect(isEqual).To(BeTrue())
	})
}
