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
	"crypto/sha256"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	cloudflareoperatoriov1 "github.com/containeroo/cloudflare-operator/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// DNSHostReconciler reconciles DNSRecords for a host-based resource (Ingress, HTTPRoute, TLSRoute, GRPCRoute, ...)
type DNSHostReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	RetryInterval            time.Duration
	DefaultReconcileInterval time.Duration
}

// Reconcile drives DNSRecord reconciliation for the provided owner and host list.
func (r *DNSHostReconciler) Reconcile(ctx context.Context, owner client.Object, annotations map[string]string, hosts map[string]struct{}) (ctrl.Result, error) {
	if deletionTimestamp := owner.GetDeletionTimestamp(); deletionTimestamp != nil && !deletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	log := ctrl.LoggerFrom(ctx)

	dnsRecords := &cloudflareoperatoriov1.DNSRecordList{}
	if err := r.List(ctx, dnsRecords, client.InNamespace(owner.GetNamespace()), client.MatchingFields{cloudflareoperatoriov1.OwnerRefUIDIndexKey: string(owner.GetUID())}); err != nil {
		log.Error(err, "Failed to list DNSRecords")
		return ctrl.Result{RequeueAfter: r.RetryInterval}, nil
	}

	if annotations["cloudflare-operator.io/content"] == "" && annotations["cloudflare-operator.io/ip-ref"] == "" {
		for _, record := range dnsRecords.Items {
			if err := r.releaseDNSRecord(ctx, owner, &record); err != nil {
				return ctrl.Result{}, err
			}
		}

		return ctrl.Result{}, nil
	}

	normalizedHosts := make(map[string]struct{}, len(hosts))
	for host := range hosts {
		normalizedHosts[canonicalDNSName(host)] = struct{}{}
	}
	hosts = normalizedHosts
	dnsRecordSpec := parseDNSAnnotations(annotations, r.DefaultReconcileInterval)
	existingRecords := make(map[string]cloudflareoperatoriov1.DNSRecord)
	for _, record := range dnsRecords.Items {
		existingRecords[canonicalDNSName(record.Spec.Name)] = record
	}

	if err := r.reconcileDNSRecords(ctx, owner, dnsRecordSpec, existingRecords, hosts); err != nil {
		log.Error(err, "Failed to reconcile DNS records")
		return ctrl.Result{RequeueAfter: r.RetryInterval}, nil
	}

	return ctrl.Result{}, nil
}

func (r *DNSHostReconciler) reconcileDNSRecords(ctx context.Context, owner client.Object, dnsRecordSpec cloudflareoperatoriov1.DNSRecordSpec, existingRecords map[string]cloudflareoperatoriov1.DNSRecord, hosts map[string]struct{}) error {
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
			if err := r.checkSharedConfiguration(ctx, owner, &record, dnsRecordSpec); err != nil {
				return err
			}
			record.Spec = dnsRecordSpec
			if err := r.Update(ctx, &record); err != nil {
				return fmt.Errorf("failed to update DNSRecord for %s: %w", host, err)
			}
		}
	}

	for host, record := range existingRecords {
		if _, exists := hosts[host]; !exists {
			if err := r.releaseDNSRecord(ctx, owner, &record); err != nil {
				return err
			}
		}
	}

	return nil
}

func (r *DNSHostReconciler) createDNSRecord(ctx context.Context, owner client.Object, dnsRecordSpec cloudflareoperatoriov1.DNSRecordSpec) error {
	var records cloudflareoperatoriov1.DNSRecordList
	if err := r.List(ctx, &records, client.InNamespace(owner.GetNamespace())); err != nil {
		return err
	}
	for _, existing := range records.Items {
		if canonicalDNSName(existing.Spec.Name) != dnsRecordSpec.Name || existing.Labels["app.kubernetes.io/managed-by"] != "cloudflare-operator" {
			continue
		}
		if !existing.DeletionTimestamp.IsZero() {
			return fmt.Errorf("DNSRecord %s is still deleting", existing.Name)
		}
		normalizedSpec := existing.Spec
		normalizedSpec.Name = canonicalDNSName(normalizedSpec.Name)
		if !reflect.DeepEqual(normalizedSpec, dnsRecordSpec) {
			return fmt.Errorf("conflicting DNS configuration for shared host %s", dnsRecordSpec.Name)
		}
		// Convert legacy controller ownership to ordinary references so garbage collection
		// retains the DNSRecord until every requesting owner is gone.
		for i := range existing.OwnerReferences {
			existing.OwnerReferences[i].Controller = nil
			existing.OwnerReferences[i].BlockOwnerDeletion = nil
		}
		if err := controllerutil.SetOwnerReference(owner, &existing, r.Scheme); err != nil {
			return err
		}
		return r.Update(ctx, &existing)
	}
	dnsRecord := &cloudflareoperatoriov1.DNSRecord{
		ObjectMeta: metav1.ObjectMeta{
			Name:      dnsRecordResourceName(dnsRecordSpec.Name),
			Namespace: owner.GetNamespace(),
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "cloudflare-operator",
			},
		},
		Spec: dnsRecordSpec,
	}
	if err := controllerutil.SetOwnerReference(owner, dnsRecord, r.Scheme); err != nil {
		return err
	}
	return r.Create(ctx, dnsRecord)
}

// The hash distinguishes names whose readable forms collide, including wildcard names.
func dnsRecordResourceName(host string) string {
	host = canonicalDNSName(host)
	prefix := strings.NewReplacer(".", "-", "*", "wildcard", "_", "-").Replace(host)
	if len(prefix) > 230 {
		prefix = prefix[:230]
	}
	prefix = strings.Trim(prefix, "-")
	if prefix == "" {
		prefix = "dns"
	}
	hash := sha256.Sum256([]byte(host))
	return fmt.Sprintf("%s-%x", prefix, hash[:8])
}

func ownerUIDs(obj client.Object) []string {
	owners := make([]string, 0, len(obj.GetOwnerReferences()))
	for _, owner := range obj.GetOwnerReferences() {
		owners = append(owners, string(owner.UID))
	}
	return owners
}

func (r *DNSHostReconciler) releaseDNSRecord(ctx context.Context, owner client.Object, record *cloudflareoperatoriov1.DNSRecord) error {
	if len(record.OwnerReferences) <= 1 {
		return client.IgnoreNotFound(r.Delete(ctx, record, client.Preconditions{UID: &record.UID, ResourceVersion: &record.ResourceVersion}))
	}
	record.OwnerReferences = slices.DeleteFunc(record.OwnerReferences, func(ref metav1.OwnerReference) bool { return ref.UID == owner.GetUID() })
	if err := r.Update(ctx, record); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

// A shared record may change only once all remaining owners request the same spec.
func (r *DNSHostReconciler) checkSharedConfiguration(ctx context.Context, owner client.Object, record *cloudflareoperatoriov1.DNSRecord, desired cloudflareoperatoriov1.DNSRecordSpec) error {
	for _, ref := range record.OwnerReferences {
		if ref.UID == owner.GetUID() {
			continue
		}
		object, err := r.Scheme.New(schema.FromAPIVersionAndKind(ref.APIVersion, ref.Kind))
		if err != nil {
			return err
		}
		other, ok := object.(client.Object)
		if !ok {
			return fmt.Errorf("unsupported DNS owner kind %s", ref.Kind)
		}
		if err := r.Get(ctx, client.ObjectKey{Namespace: record.Namespace, Name: ref.Name}, other); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return err
		}
		if other.GetUID() != ref.UID || !other.GetDeletionTimestamp().IsZero() {
			continue
		}
		spec := parseDNSAnnotations(other.GetAnnotations(), r.DefaultReconcileInterval)
		spec.Name = desired.Name
		if !reflect.DeepEqual(spec, desired) {
			return fmt.Errorf("conflicting DNS configuration for shared host %s; all owners must agree", desired.Name)
		}
	}
	return nil
}
