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
	"reflect"
	"strings"
	"time"

	"github.com/fluxcd/pkg/runtime/conditions"
	"github.com/fluxcd/pkg/runtime/patch"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	apierrutil "k8s.io/apimachinery/pkg/util/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/cloudflare/cloudflare-go"
	cloudflareoperatoriov1 "github.com/containeroo/cloudflare-operator/api/v1"
	intconditions "github.com/containeroo/cloudflare-operator/internal/conditions"
	interrors "github.com/containeroo/cloudflare-operator/internal/errors"
	"github.com/containeroo/cloudflare-operator/internal/metrics"
	intpredicates "github.com/containeroo/cloudflare-operator/internal/predicates"
)

// DNSRecordReconciler reconciles a DNSRecord object
type DNSRecordReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	RetryInterval time.Duration
}

// SetupWithManager sets up the controller with the Manager.
func (r *DNSRecordReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(ctx, &cloudflareoperatoriov1.DNSRecord{}, cloudflareoperatoriov1.IPRefIndexKey,
		func(o client.Object) []string {
			dnsRecord := o.(*cloudflareoperatoriov1.DNSRecord)
			return []string{dnsRecord.Spec.IPRef.Name}
		}); err != nil {
		return err
	}
	if err := mgr.GetFieldIndexer().IndexField(ctx, &cloudflareoperatoriov1.DNSRecord{}, cloudflareoperatoriov1.DNSRecordAccountRefIndexKey,
		func(o client.Object) []string {
			dnsRecord := o.(*cloudflareoperatoriov1.DNSRecord)
			if dnsRecord.Spec.AccountRef.Name == "" {
				return nil
			}
			return []string{dnsRecord.Spec.AccountRef.Name}
		}); err != nil {
		return err
	}
	if err := mgr.GetFieldIndexer().IndexField(ctx, &cloudflareoperatoriov1.DNSRecord{}, cloudflareoperatoriov1.OwnerRefUIDIndexKey,
		func(o client.Object) []string {
			obj := o.(*cloudflareoperatoriov1.DNSRecord)
			ownerReferences := obj.GetOwnerReferences()
			var ownerReferencesUID string
			for _, ownerReference := range ownerReferences {
				if ownerReference.Kind != "Ingress" && ownerReference.Kind != "HTTPRoute" {
					continue
				}
				ownerReferencesUID = string(ownerReference.UID)
			}

			return []string{ownerReferencesUID}
		},
	); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&cloudflareoperatoriov1.DNSRecord{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&cloudflareoperatoriov1.IP{}, handler.EnqueueRequestsFromMapFunc(r.requestsForIPChange), builder.WithPredicates(intpredicates.IPAddressChangedPredicate{})).
		Watches(&cloudflareoperatoriov1.Account{}, handler.EnqueueRequestsFromMapFunc(r.requestsForAccountChange)).
		Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(r.requestsForAccountSecretChange)).
		Complete(r)
}

// +kubebuilder:rbac:groups=cloudflare-operator.io,resources=dnsrecords,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cloudflare-operator.io,resources=dnsrecords/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=cloudflare-operator.io,resources=dnsrecords/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *DNSRecordReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, retErr error) {
	log := ctrl.LoggerFrom(ctx)

	dnsrecord := &cloudflareoperatoriov1.DNSRecord{}
	if err := r.Get(ctx, req.NamespacedName, dnsrecord); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	patchHelper := patch.NewSerialPatcher(dnsrecord, r.Client)

	defer func() {
		patchOpts := []patch.Option{}

		if errors.Is(retErr, reconcile.TerminalError(nil)) || (retErr == nil && result.RequeueAfter <= 0) {
			patchOpts = append(patchOpts, patch.WithStatusObservedGeneration{})
		}

		// We do not want to return these errors, but rather wait for the
		// designated RequeueAfter to expire and try again.
		// However, not returning an error will cause the patch helper to
		// patch the observed generation, which we do not want. So we ignore
		// these errors here after patching.
		retErr = interrors.Ignore(retErr, errWaitForAccount, errWaitForZone)

		if err := patchHelper.Patch(ctx, dnsrecord, patchOpts...); err != nil {
			if !dnsrecord.DeletionTimestamp.IsZero() {
				err = apierrutil.FilterOut(err, func(e error) bool { return apierrors.IsNotFound(e) })
			}
			retErr = apierrutil.Reduce(apierrutil.NewAggregate([]error{retErr, err}))
		}
	}()

	zones := &cloudflareoperatoriov1.ZoneList{}
	if err := r.List(ctx, zones); err != nil {
		log.Error(err, "Failed to list zones")
		return ctrl.Result{RequeueAfter: r.RetryInterval}, nil
	}

	zone := findZoneForDNSRecord(dnsrecord.Spec.Name, zones.Items)
	if zone == nil {
		intconditions.MarkFalse(dnsrecord, fmt.Errorf("zone for %q not found", dnsrecord.Spec.Name))
		return ctrl.Result{RequeueAfter: r.RetryInterval}, nil
	}

	if !dnsrecord.DeletionTimestamp.IsZero() {
		if err := r.reconcileDelete(ctx, zone.Status.ID, dnsrecord); err != nil {
			log.Error(err, "Failed to delete DNS record in Cloudflare, record may still exist in Cloudflare")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(dnsrecord, cloudflareoperatoriov1.CloudflareOperatorFinalizer) {
		controllerutil.AddFinalizer(dnsrecord, cloudflareoperatoriov1.CloudflareOperatorFinalizer)
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	return r.reconcileDNSRecord(ctx, dnsrecord, zone)
}

// reconcileDNSRecord reconciles the dnsrecord
func (r *DNSRecordReconciler) reconcileDNSRecord(ctx context.Context, dnsrecord *cloudflareoperatoriov1.DNSRecord, zone *cloudflareoperatoriov1.Zone) (ctrl.Result, error) {
	desiredRecord := dnsrecord.Spec
	if (dnsrecord.Spec.Type == "A" || dnsrecord.Spec.Type == "AAAA") && dnsrecord.Spec.IPRef.Name != "" {
		ip := &cloudflareoperatoriov1.IP{}
		if err := r.Get(ctx, client.ObjectKey{Name: dnsrecord.Spec.IPRef.Name}, ip); err != nil {
			intconditions.MarkFalse(dnsrecord, err)
			return ctrl.Result{RequeueAfter: r.RetryInterval}, nil
		}
		desiredRecord.Content = resolvedIPAddress(ip)
	}

	cloudflareAPI, err := cloudflareAPIFromDNSRecord(ctx, r.Client, dnsrecord, zone)
	if err != nil {
		if errors.Is(err, errWaitForAccount) {
			intconditions.MarkUnknown(dnsrecord, "Cloudflare account is not ready")
			return ctrl.Result{RequeueAfter: r.RetryInterval}, errWaitForAccount
		}
		intconditions.MarkFalse(dnsrecord, err)
		return ctrl.Result{RequeueAfter: r.RetryInterval}, nil
	}

	if !conditions.IsTrue(zone, cloudflareoperatoriov1.ConditionTypeReady) {
		intconditions.MarkUnknown(dnsrecord, "Zone is not ready")
		return ctrl.Result{RequeueAfter: r.RetryInterval}, errWaitForZone
	}

	var existingRecord cloudflare.DNSRecord
	if dnsrecord.Status.RecordID != "" {
		existingRecord, err = cloudflareAPI.GetDNSRecord(ctx, cloudflare.ZoneIdentifier(zone.Status.ID), dnsrecord.Status.RecordID)
		if err != nil {
			intconditions.MarkFalse(dnsrecord, err)
			return ctrl.Result{RequeueAfter: r.RetryInterval}, nil
		}
	} else {
		cloudflareExistingRecord, _, err := cloudflareAPI.ListDNSRecords(ctx, cloudflare.ZoneIdentifier(zone.Status.ID), cloudflare.ListDNSRecordsParams{
			Type: dnsrecord.Spec.Type,
			Name: dnsrecord.Spec.Name,
		})
		if err != nil {
			intconditions.MarkFalse(dnsrecord, err)
			return ctrl.Result{RequeueAfter: r.RetryInterval}, nil
		}
		existingRecord, err = findExistingRecordForAdoption(desiredRecord, cloudflareExistingRecord)
		if err != nil {
			intconditions.MarkFalse(dnsrecord, err)
			return ctrl.Result{RequeueAfter: r.RetryInterval}, nil
		}
		dnsrecord.Status.RecordID = existingRecord.ID
	}

	if proxiedEnabled(desiredRecord.Proxied) && desiredRecord.TTL != 1 {
		intconditions.MarkFalse(dnsrecord, errors.New("TTL must be 1 when proxied"))
		return ctrl.Result{}, nil
	}

	if existingRecord.ID == "" {
		proxied := proxiedPtr(proxiedEnabled(desiredRecord.Proxied))
		newDNSRecord, err := cloudflareAPI.CreateDNSRecord(ctx, cloudflare.ZoneIdentifier(zone.Status.ID), cloudflare.CreateDNSRecordParams{
			Name:     desiredRecord.Name,
			Type:     desiredRecord.Type,
			Content:  desiredRecord.Content,
			TTL:      desiredRecord.TTL,
			Proxied:  proxied,
			Priority: desiredRecord.Priority,
			Data:     desiredRecord.Data,
			Comment:  desiredRecord.Comment,
		})
		if err != nil {
			intconditions.MarkFalse(dnsrecord, err)
			return ctrl.Result{RequeueAfter: r.RetryInterval}, nil
		}
		dnsrecord.Status.RecordID = newDNSRecord.ID
	} else if !r.compareDNSRecord(desiredRecord, existingRecord) {
		proxied := proxiedPtr(proxiedEnabled(desiredRecord.Proxied))
		if _, err := cloudflareAPI.UpdateDNSRecord(ctx, cloudflare.ZoneIdentifier(zone.Status.ID), cloudflare.UpdateDNSRecordParams{
			ID:       dnsrecord.Status.RecordID,
			Name:     desiredRecord.Name,
			Type:     desiredRecord.Type,
			Content:  desiredRecord.Content,
			TTL:      desiredRecord.TTL,
			Proxied:  proxied,
			Priority: desiredRecord.Priority,
			Data:     desiredRecord.Data,
			Comment:  cloudflare.StringPtr(desiredRecord.Comment),
		}); err != nil {
			intconditions.MarkFalse(dnsrecord, err)
			return ctrl.Result{RequeueAfter: r.RetryInterval}, nil
		}
	}

	intconditions.MarkTrue(dnsrecord, "DNS record synced")

	return ctrl.Result{RequeueAfter: dnsrecord.Spec.Interval.Duration}, nil
}

// compareDNSRecord compares the DNS record to the DNSRecord object
func (r *DNSRecordReconciler) compareDNSRecord(dnsRecordSpec cloudflareoperatoriov1.DNSRecordSpec, existingRecord cloudflare.DNSRecord) bool {
	if dnsRecordSpec.Name != existingRecord.Name {
		return false
	}
	if dnsRecordSpec.Type != existingRecord.Type {
		return false
	}
	if dnsRecordSpec.Type != "SRV" && dnsRecordSpec.Type != "LOC" && dnsRecordSpec.Type != "CAA" {
		if dnsRecordSpec.Content != existingRecord.Content {
			return false
		}
	}
	if dnsRecordSpec.TTL != existingRecord.TTL {
		return false
	}
	if proxiedEnabled(dnsRecordSpec.Proxied) != proxiedEnabled(existingRecord.Proxied) {
		return false
	}
	if !comparePriority(dnsRecordSpec.Priority, existingRecord.Priority) {
		return false
	}
	if !compareData(existingRecord.Data, dnsRecordSpec.Data) {
		return false
	}
	if dnsRecordSpec.Comment != existingRecord.Comment {
		return false
	}

	return true
}

// comparePriority compares the priority nil safe
func comparePriority(a, b *uint16) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}

	return *a == *b
}

// compareData compares the data nil safe
func compareData(a any, b *apiextensionsv1.JSON) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	var bb any
	if err := json.Unmarshal(b.Raw, &bb); err != nil {
		return false
	}

	return reflect.DeepEqual(a, bb)
}

func proxiedEnabled(proxied *bool) bool {
	if proxied == nil {
		return true
	}
	return *proxied
}

func proxiedPtr(proxied bool) *bool {
	return &proxied
}

func findExistingRecordForAdoption(desiredRecord cloudflareoperatoriov1.DNSRecordSpec, existingRecords []cloudflare.DNSRecord) (cloudflare.DNSRecord, error) {
	switch len(existingRecords) {
	case 0:
		return cloudflare.DNSRecord{}, nil
	case 1:
		return existingRecords[0], nil
	}

	for _, record := range existingRecords {
		if desiredRecord.Name != record.Name || desiredRecord.Type != record.Type {
			continue
		}
		if desiredRecord.Type != "SRV" && desiredRecord.Type != "LOC" && desiredRecord.Type != "CAA" && desiredRecord.Content != record.Content {
			continue
		}
		return record, nil
	}

	return cloudflare.DNSRecord{}, fmt.Errorf("multiple Cloudflare records matched %s %s; set status.recordID manually or remove duplicates", desiredRecord.Type, desiredRecord.Name)
}

func resolvedIPAddress(ip *cloudflareoperatoriov1.IP) string {
	if ip.Status.Address != "" {
		return ip.Status.Address
	}
	return ip.Spec.Address
}

// findZoneForDNSRecord returns the longest matching zone for a DNS record name.
func findZoneForDNSRecord(dnsRecordName string, zones []cloudflareoperatoriov1.Zone) *cloudflareoperatoriov1.Zone {
	var matchedZone *cloudflareoperatoriov1.Zone
	for i := range zones {
		zone := &zones[i]
		if dnsRecordName == zone.Spec.Name || strings.HasSuffix(dnsRecordName, "."+zone.Spec.Name) {
			if matchedZone == nil || len(zone.Spec.Name) > len(matchedZone.Spec.Name) {
				matchedZone = zone
			}
		}
	}
	return matchedZone
}

// requestsForIPChange returns a list of reconcile.Requests for DNSRecords that need to be reconciled if the IP changes
func (r *DNSRecordReconciler) requestsForIPChange(ctx context.Context, o client.Object) []reconcile.Request {
	ip, ok := o.(*cloudflareoperatoriov1.IP)
	if !ok {
		err := fmt.Errorf("expected an IP, got %T", o)
		ctrl.LoggerFrom(ctx).Error(err, "failed to get requests for IP change")
		return nil
	}

	var dnsRecords cloudflareoperatoriov1.DNSRecordList
	if err := r.List(ctx, &dnsRecords, client.MatchingFields{
		cloudflareoperatoriov1.IPRefIndexKey: client.ObjectKeyFromObject(ip).Name,
	}); err != nil {
		ctrl.LoggerFrom(ctx).Error(err, "failed to list DNSRecords for IP change")
		return nil
	}

	reqs := make([]reconcile.Request, 0, len(dnsRecords.Items))
	for i := range dnsRecords.Items {
		reqs = append(reqs, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&dnsRecords.Items[i])})
	}
	return reqs
}

func (r *DNSRecordReconciler) requestsForAccountChange(ctx context.Context, o client.Object) []reconcile.Request {
	account, ok := o.(*cloudflareoperatoriov1.Account)
	if !ok {
		err := fmt.Errorf("expected an Account, got %T", o)
		ctrl.LoggerFrom(ctx).Error(err, "failed to get requests for account change")
		return nil
	}

	return r.requestsForAccountNames(ctx, map[string]struct{}{account.Name: {}})
}

func (r *DNSRecordReconciler) requestsForAccountSecretChange(ctx context.Context, o client.Object) []reconcile.Request {
	secret := client.ObjectKeyFromObject(o)

	var accounts cloudflareoperatoriov1.AccountList
	if err := r.List(ctx, &accounts); err != nil {
		ctrl.LoggerFrom(ctx).Error(err, "failed to list Accounts for account secret change")
		return nil
	}

	accountNames := make(map[string]struct{})
	for i := range accounts.Items {
		if accountMatchesSecret(&accounts.Items[i], secret) {
			accountNames[accounts.Items[i].Name] = struct{}{}
		}
	}

	return r.requestsForAccountNames(ctx, accountNames)
}

func (r *DNSRecordReconciler) requestsForAccountNames(ctx context.Context, accountNames map[string]struct{}) []reconcile.Request {
	if len(accountNames) == 0 {
		return nil
	}

	var (
		dnsRecords cloudflareoperatoriov1.DNSRecordList
		zones      cloudflareoperatoriov1.ZoneList
		accounts   cloudflareoperatoriov1.AccountList
	)

	if err := r.List(ctx, &dnsRecords); err != nil {
		ctrl.LoggerFrom(ctx).Error(err, "failed to list DNSRecords for account change")
		return nil
	}
	if err := r.List(ctx, &zones); err != nil {
		ctrl.LoggerFrom(ctx).Error(err, "failed to list Zones for account change")
		return nil
	}
	if err := r.List(ctx, &accounts); err != nil {
		ctrl.LoggerFrom(ctx).Error(err, "failed to list Accounts for account change fallback")
		return nil
	}

	singleAccountFallback := len(accounts.Items) == 1
	singleAccountName := ""
	if singleAccountFallback {
		singleAccountName = accounts.Items[0].Name
	}

	reqs := make([]reconcile.Request, 0, len(dnsRecords.Items))
	for i := range dnsRecords.Items {
		dnsRecord := &dnsRecords.Items[i]
		accountName := dnsRecord.Spec.AccountRef.Name
		if accountName == "" {
			if zone := findZoneForDNSRecord(dnsRecord.Spec.Name, zones.Items); zone != nil {
				accountName = zone.Spec.AccountRef.Name
			}
		}
		if accountName == "" && singleAccountFallback {
			accountName = singleAccountName
		}
		if _, found := accountNames[accountName]; found {
			reqs = append(reqs, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(dnsRecord)})
		}
	}

	return reqs
}

// reconcileDelete reconciles the deletion of the dnsrecord
func (r *DNSRecordReconciler) reconcileDelete(ctx context.Context, zoneID string, dnsrecord *cloudflareoperatoriov1.DNSRecord) error {
	zones := &cloudflareoperatoriov1.ZoneList{}
	if err := r.List(ctx, zones); err != nil {
		return err
	}

	zone := findZoneForDNSRecord(dnsrecord.Spec.Name, zones.Items)
	cloudflareAPI, err := cloudflareAPIFromDNSRecord(ctx, r.Client, dnsrecord, zone)
	if err != nil {
		return err
	}

	if err := cloudflareAPI.DeleteDNSRecord(ctx, cloudflare.ZoneIdentifier(zoneID), dnsrecord.Status.RecordID); err != nil && err.Error() != "Record does not exist. (81044)" && dnsrecord.Status.RecordID != "" {
		return err
	}
	metrics.DnsRecordFailureCounter.DeleteLabelValues(dnsrecord.Namespace, dnsrecord.Name, dnsrecord.Spec.Name)
	controllerutil.RemoveFinalizer(dnsrecord, cloudflareoperatoriov1.CloudflareOperatorFinalizer)

	return nil
}
