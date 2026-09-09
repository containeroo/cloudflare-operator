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
	apierrutil "k8s.io/apimachinery/pkg/util/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"

	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/cloudflare/cloudflare-go/v7/dns"
	cloudflareoperatoriov1 "github.com/containeroo/cloudflare-operator/api/v1"
	intconditions "github.com/containeroo/cloudflare-operator/internal/conditions"
	interrors "github.com/containeroo/cloudflare-operator/internal/errors"
	"github.com/containeroo/cloudflare-operator/internal/metrics"
	intpredicates "github.com/containeroo/cloudflare-operator/internal/predicates"
)

// DNSRecordReconciler reconciles a DNSRecord object
type DNSRecordReconciler struct {
	client.Client
	APIReader client.Reader

	RetryInterval time.Duration
}

// SetupWithManager sets up the controller with the Manager.
func (r *DNSRecordReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	r.APIReader = mgr.GetAPIReader()
	if err := mgr.GetFieldIndexer().IndexField(ctx, &cloudflareoperatoriov1.DNSRecord{}, cloudflareoperatoriov1.IPRefIndexKey,
		func(o client.Object) []string {
			dnsRecord := o.(*cloudflareoperatoriov1.DNSRecord)
			return []string{dnsRecord.Spec.IPRef.Name}
		}); err != nil {
		return err
	}
	if err := mgr.GetFieldIndexer().IndexField(ctx, &cloudflareoperatoriov1.DNSRecord{}, cloudflareoperatoriov1.OwnerRefUIDIndexKey,
		ownerUIDs,
	); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&cloudflareoperatoriov1.DNSRecord{}, builder.WithPredicates(intpredicates.ResourceChanged{})).
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

	if !dnsrecord.DeletionTimestamp.IsZero() && (dnsrecord.Status.RecordID == "" || dnsrecord.Status.ZoneID != "") {
		return ctrl.Result{}, r.reconcileDelete(ctx, nil, dnsrecord)
	}

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
		if err := r.reconcileDelete(ctx, zone, dnsrecord); err != nil {
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
	desiredRecord.Name = canonicalDNSName(desiredRecord.Name)
	if (dnsrecord.Spec.Type == "A" || dnsrecord.Spec.Type == "AAAA") && dnsrecord.Spec.IPRef.Name != "" {
		ip := &cloudflareoperatoriov1.IP{}
		if err := r.Get(ctx, client.ObjectKey{Name: dnsrecord.Spec.IPRef.Name}, ip); err != nil {
			intconditions.MarkFalse(dnsrecord, err)
			return ctrl.Result{RequeueAfter: r.RetryInterval}, nil
		}
		desiredRecord.Content = resolvedIPAddress(ip)
	}

	accountName, err := accountNameForDNSRecord(ctx, r.Client, dnsrecord, zone)
	var cloudflareAPI *cloudflareClient
	if err == nil {
		cloudflareAPI, err = cloudflareAPIForAccountName(ctx, r.Client, accountName)
	}
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

	claimed, err := r.claimedRecordIDs(ctx, dnsrecord)
	if err != nil {
		return ctrl.Result{}, err
	}
	if _, owned := claimed[dnsrecord.Status.RecordID]; dnsrecord.Status.RecordID != "" && owned {
		intconditions.MarkFalse(dnsrecord, errors.New("remote record is also claimed by another DNSRecord; remove the duplicate resource"))
		return ctrl.Result{RequeueAfter: r.RetryInterval}, nil
	}
	if dnsrecord.Status.RecordID != "" && dnsrecord.Status.ZoneID != "" &&
		(dnsrecord.Status.ZoneID != zone.Status.ID || dnsrecord.Status.AccountName != accountName) {
		if err := r.deleteBoundRecord(ctx, nil, dnsrecord); err != nil {
			intconditions.MarkFalse(dnsrecord, err)
			return ctrl.Result{}, err
		}
		dnsrecord.Status.RecordID = ""
		dnsrecord.Status.ZoneID = ""
		dnsrecord.Status.AccountName = ""
		intconditions.MarkUnknown(dnsrecord, "Moving DNS record to a different zone or account")
		// Persist removal of the old binding before creating in the new zone.
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	var existingRecord dns.RecordResponse
	if dnsrecord.Status.RecordID != "" {
		existingRecord, err = getCloudflareDNSRecord(ctx, cloudflareAPI, zone.Status.ID, dnsrecord.Status.RecordID)
		if isCloudflareDNSRecordNotFound(err) {
			dnsrecord.Status.RecordID = ""
			intconditions.MarkUnknown(dnsrecord, "Remote record is missing; recreating")
			// Persist the pending state before creation so pruning protects the replacement.
			return ctrl.Result{RequeueAfter: time.Second}, nil
		} else if err != nil {
			intconditions.MarkFalse(dnsrecord, err)
			return ctrl.Result{RequeueAfter: r.RetryInterval}, nil
		}
	}
	if dnsrecord.Status.RecordID == "" {
		params := dns.RecordListParams{}
		params.Type.Value = dns.RecordListParamsType(dnsrecord.Spec.Type)
		params.Type.Present = true
		params.Name.Value.Exact.Value = desiredRecord.Name
		params.Name.Value.Exact.Present = true
		params.Name.Present = true
		cloudflareExistingRecord, err := listCloudflareDNSRecords(ctx, cloudflareAPI, zone.Status.ID, params)
		if err != nil {
			intconditions.MarkFalse(dnsrecord, err)
			return ctrl.Result{RequeueAfter: r.RetryInterval}, nil
		}
		existingRecord, err = findExistingRecordForAdoption(desiredRecord, cloudflareExistingRecord, claimed)
		if err != nil {
			intconditions.MarkFalse(dnsrecord, err)
			return ctrl.Result{RequeueAfter: r.RetryInterval}, nil
		}
		dnsrecord.Status.RecordID = existingRecord.ID
	}

	dnsrecord.Status.ZoneID = zone.Status.ID
	dnsrecord.Status.AccountName = accountName
	if proxiedEnabled(desiredRecord.Proxied) && desiredRecord.TTL != 1 {
		intconditions.MarkFalse(dnsrecord, errors.New("TTL must be 1 when proxied"))
		return ctrl.Result{}, nil
	}

	if existingRecord.ID == "" {
		newDNSRecord, err := createCloudflareDNSRecord(ctx, cloudflareAPI, zone.Status.ID, desiredRecord)
		if err != nil {
			intconditions.MarkFalse(dnsrecord, err)
			return ctrl.Result{RequeueAfter: r.RetryInterval}, nil
		}
		dnsrecord.Status.RecordID = newDNSRecord.ID
	} else if !r.compareDNSRecord(desiredRecord, existingRecord) {
		if err := editCloudflareDNSRecord(ctx, cloudflareAPI, zone.Status.ID, dnsrecord.Status.RecordID, desiredRecord); err != nil {
			intconditions.MarkFalse(dnsrecord, err)
			return ctrl.Result{RequeueAfter: r.RetryInterval}, nil
		}
	}

	intconditions.MarkTrue(dnsrecord, "DNS record synced")

	return ctrl.Result{RequeueAfter: positiveInterval(dnsrecord.Spec.Interval.Duration)}, nil
}

// compareDNSRecord compares the DNS record to the DNSRecord object
func (r *DNSRecordReconciler) compareDNSRecord(dnsRecordSpec cloudflareoperatoriov1.DNSRecordSpec, existingRecord dns.RecordResponse) bool {
	if canonicalDNSName(dnsRecordSpec.Name) != canonicalDNSName(existingRecord.Name) {
		return false
	}
	if dnsRecordSpec.Type != string(existingRecord.Type) {
		return false
	}
	if dnsRecordSpec.Type != "SRV" && dnsRecordSpec.Type != "LOC" && dnsRecordSpec.Type != "CAA" {
		if dnsRecordSpec.Content != existingRecord.Content {
			return false
		}
	}
	if normalizedTTL(dnsRecordSpec.TTL) != float64(existingRecord.TTL) {
		return false
	}
	if proxiedEnabled(dnsRecordSpec.Proxied) != existingRecord.Proxied {
		return false
	}
	if !comparePriority(dnsRecordSpec.Type, dnsRecordSpec.Priority, existingRecord.Priority) {
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
func comparePriority(recordType string, a *uint16, b float64) bool {
	// Cloudflare ignores top-level priority outside MX and URI; SRV uses data.priority.
	if recordType != "MX" && recordType != dnsRecordTypeURI {
		return true
	}
	if a == nil {
		return b == 0
	}

	return float64(*a) == b
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

	var aa any
	aBytes, err := json.Marshal(a)
	if err != nil {
		return false
	}
	if err := json.Unmarshal(aBytes, &aa); err != nil {
		return false
	}

	return reflect.DeepEqual(aa, bb)
}

func proxiedEnabled(proxied *bool) bool {
	if proxied == nil {
		return true
	}
	return *proxied
}

func findExistingRecordForAdoption(desired cloudflareoperatoriov1.DNSRecordSpec, records []dns.RecordResponse, claimed map[string]struct{}) (dns.RecordResponse, error) {
	var match dns.RecordResponse
	for _, record := range records {
		if _, owned := claimed[record.ID]; owned {
			continue
		}
		if canonicalDNSName(desired.Name) != canonicalDNSName(record.Name) || desired.Type != string(record.Type) {
			continue
		}
		if desired.Type == "SRV" || desired.Type == "LOC" || desired.Type == "CAA" {
			if !compareData(record.Data, desired.Data) {
				continue
			}
		} else if desired.Content != record.Content {
			continue
		}
		if !comparePriority(desired.Type, desired.Priority, record.Priority) {
			continue
		}
		if match.ID != "" {
			return dns.RecordResponse{}, fmt.Errorf("multiple Cloudflare records matched %s %s; resolve duplicate records before adoption", desired.Type, desired.Name)
		}
		match = record
	}
	return match, nil
}

func canonicalDNSName(name string) string {
	return strings.ToLower(strings.TrimSuffix(name, "."))
}

func resolvedIPAddress(ip *cloudflareoperatoriov1.IP) string {
	if ip.Status.Address != "" {
		return ip.Status.Address
	}
	return ip.Spec.Address
}

// findZoneForDNSRecord returns the longest matching zone for a DNS record name.
func findZoneForDNSRecord(dnsRecordName string, zones []cloudflareoperatoriov1.Zone) *cloudflareoperatoriov1.Zone {
	dnsRecordName = canonicalDNSName(dnsRecordName)
	var matchedZone *cloudflareoperatoriov1.Zone
	for i := range zones {
		zone := &zones[i]
		if dnsRecordName == canonicalDNSName(zone.Spec.Name) || strings.HasSuffix(dnsRecordName, "."+canonicalDNSName(zone.Spec.Name)) {
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
func (r *DNSRecordReconciler) reconcileDelete(ctx context.Context, zone *cloudflareoperatoriov1.Zone, dnsrecord *cloudflareoperatoriov1.DNSRecord) error {
	if dnsrecord.Status.RecordID != "" {
		claimed, err := r.claimedRecordIDs(ctx, dnsrecord)
		if err != nil {
			return err
		}
		if _, owned := claimed[dnsrecord.Status.RecordID]; !owned {
			if err := r.deleteBoundRecord(ctx, zone, dnsrecord); err != nil {
				return err
			}
		}
	}
	metrics.DnsRecordFailureCounter.DeleteLabelValues(dnsrecord.Namespace, dnsrecord.Name, dnsrecord.Spec.Name)
	controllerutil.RemoveFinalizer(dnsrecord, cloudflareoperatoriov1.CloudflareOperatorFinalizer)
	return nil
}

func (r *DNSRecordReconciler) deleteBoundRecord(ctx context.Context, zone *cloudflareoperatoriov1.Zone, record *cloudflareoperatoriov1.DNSRecord) error {
	zoneID, accountName := record.Status.ZoneID, record.Status.AccountName
	if zoneID == "" {
		// Upgrade legacy status only when its original Zone is still available.
		if zone == nil {
			return errors.New("remote zone identity is missing; restore the original Zone before deleting this legacy DNSRecord")
		}
		zoneID = zone.Status.ID
		var err error
		accountName, err = accountNameForDNSRecord(ctx, r.Client, record, zone)
		if err != nil {
			return err
		}
	}
	if zoneID == "" || accountName == "" {
		return errors.New("remote zone and account identity are required for deletion")
	}
	api, err := cloudflareAPIForAccountName(ctx, r.Client, accountName)
	if err != nil {
		return err
	}
	if record.Status.ZoneID == "" {
		if _, err := getCloudflareDNSRecord(ctx, api, zoneID, record.Status.RecordID); err != nil {
			return fmt.Errorf("cannot verify legacy record identity; restore its original Zone or set status.zoneID and status.accountName: %w", err)
		}
	}
	err = deleteCloudflareDNSRecord(ctx, api, zoneID, record.Status.RecordID)
	if isCloudflareDNSRecordNotFound(err) {
		return nil
	}
	return err
}

func (r *DNSRecordReconciler) claimedRecordIDs(ctx context.Context, record *cloudflareoperatoriov1.DNSRecord) (map[string]struct{}, error) {
	reader := r.APIReader
	if reader == nil {
		reader = r.Client
	}
	var records cloudflareoperatoriov1.DNSRecordList
	if err := reader.List(ctx, &records); err != nil {
		return nil, err
	}
	claimed := make(map[string]struct{})
	for _, other := range records.Items {
		if client.ObjectKeyFromObject(&other) != client.ObjectKeyFromObject(record) && other.Status.RecordID != "" {
			claimed[other.Status.RecordID] = struct{}{}
		}
	}
	return claimed, nil
}
