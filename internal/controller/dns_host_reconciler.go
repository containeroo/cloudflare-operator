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
	"fmt"
	"reflect"
	"strings"
	"time"

	cloudflareoperatoriov1 "github.com/containeroo/cloudflare-operator/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// DNSHostReconciler reconciles DNSRecords for a host-based resource (Ingress, HTTPRoute, ...)
type DNSHostReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	RetryInterval            time.Duration
	DefaultReconcileInterval time.Duration
}

// Reconcile drives DNSRecord reconciliation for the provided owner and host list.
func (r *DNSHostReconciler) Reconcile(ctx context.Context, owner client.Object, annotations map[string]string, hosts map[string]struct{}) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	dnsRecords := &cloudflareoperatoriov1.DNSRecordList{}
	if err := r.List(ctx, dnsRecords, client.InNamespace(owner.GetNamespace()), client.MatchingFields{cloudflareoperatoriov1.OwnerRefUIDIndexKey: string(owner.GetUID())}); err != nil {
		log.Error(err, "Failed to list DNSRecords")
		return ctrl.Result{RequeueAfter: r.RetryInterval}, nil
	}

	if annotations["cloudflare-operator.io/content"] == "" && annotations["cloudflare-operator.io/ip-ref"] == "" {
		for _, record := range dnsRecords.Items {
			if err := r.Delete(ctx, &record); err != nil {
				log.Error(err, "Failed to delete DNSRecord", "name", record.Name)
			}
		}

		return ctrl.Result{}, nil
	}

	dnsRecordSpec := parseDNSAnnotations(annotations, r.DefaultReconcileInterval)
	existingRecords := make(map[string]cloudflareoperatoriov1.DNSRecord)
	for _, record := range dnsRecords.Items {
		existingRecords[record.Spec.Name] = record
	}

	if err := r.reconcileDNSRecords(ctx, owner, dnsRecordSpec, existingRecords, hosts); err != nil {
		log.Error(err, "Failed to reconcile DNS records")
		return ctrl.Result{RequeueAfter: r.RetryInterval}, nil
	}

	return ctrl.Result{}, nil
}

func (r *DNSHostReconciler) reconcileDNSRecords(ctx context.Context, owner client.Object, dnsRecordSpec cloudflareoperatoriov1.DNSRecordSpec, existingRecords map[string]cloudflareoperatoriov1.DNSRecord, hosts map[string]struct{}) error {
	log := ctrl.LoggerFrom(ctx)

	for host := range hosts {
		record, exists := existingRecords[host]
		dnsRecordSpec.Name = host

		if !exists {
			if err := r.createDNSRecord(ctx, owner, dnsRecordSpec); err != nil {
				return fmt.Errorf("failed to create DNSRecord for %s: %w", host, err)
			}
			continue
		}

		if !reflect.DeepEqual(record.Spec, dnsRecordSpec) {
			record.Spec = dnsRecordSpec
			if err := r.Update(ctx, &record); err != nil {
				return fmt.Errorf("failed to update DNSRecord for %s: %w", host, err)
			}
		}
	}

	for host, record := range existingRecords {
		if _, exists := hosts[host]; !exists {
			if err := r.Delete(ctx, &record); err != nil {
				log.Error(err, "Failed to delete DNSRecord", "host", host)
			}
		}
	}

	return nil
}

func (r *DNSHostReconciler) createDNSRecord(ctx context.Context, owner client.Object, dnsRecordSpec cloudflareoperatoriov1.DNSRecordSpec) error {
	replacer := strings.NewReplacer(".", "-", "*", "wildcard")
	dnsRecord := &cloudflareoperatoriov1.DNSRecord{
		ObjectMeta: metav1.ObjectMeta{
			Name:      replacer.Replace(dnsRecordSpec.Name),
			Namespace: owner.GetNamespace(),
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "cloudflare-operator",
			},
		},
		Spec: dnsRecordSpec,
	}
	if err := controllerutil.SetControllerReference(owner, dnsRecord, r.Scheme); err != nil {
		return err
	}
	return r.Create(ctx, dnsRecord)
}
