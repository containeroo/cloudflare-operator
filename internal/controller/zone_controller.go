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
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/cloudflare/cloudflare-go/v7/dns"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apierrutil "k8s.io/apimachinery/pkg/util/errors"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	cloudflareoperatoriov1 "github.com/containeroo/cloudflare-operator/api/v1"
	intconditions "github.com/containeroo/cloudflare-operator/internal/conditions"
	interrors "github.com/containeroo/cloudflare-operator/internal/errors"
	"github.com/containeroo/cloudflare-operator/internal/metrics"
	intpredicates "github.com/containeroo/cloudflare-operator/internal/predicates"
	"github.com/fluxcd/pkg/runtime/patch"
)

// ZoneReconciler reconciles a Zone object
type ZoneReconciler struct {
	client.Client
	APIReader client.Reader

	RetryInterval time.Duration
}

var errWaitForZone = errors.New("must wait for zone")

// SetupWithManager sets up the controller with the Manager.
func (r *ZoneReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.APIReader = mgr.GetAPIReader()
	return ctrl.NewControllerManagedBy(mgr).
		For(&cloudflareoperatoriov1.Zone{}, builder.WithPredicates(intpredicates.ResourceChanged{})).
		Watches(&cloudflareoperatoriov1.Account{}, handler.EnqueueRequestsFromMapFunc(r.requestsForAccountChange)).
		Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(r.requestsForAccountSecretChange)).
		Complete(r)
}

// +kubebuilder:rbac:groups=cloudflare-operator.io,resources=zones,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cloudflare-operator.io,resources=zones/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=cloudflare-operator.io,resources=zones/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *ZoneReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, retErr error) {
	zone := &cloudflareoperatoriov1.Zone{}
	if err := r.Get(ctx, req.NamespacedName, zone); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	patchHelper := patch.NewSerialPatcher(zone, r.Client)

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

		if err := patchHelper.Patch(ctx, zone, patchOpts...); err != nil {
			if !zone.DeletionTimestamp.IsZero() {
				err = apierrutil.FilterOut(err, func(e error) bool { return apierrors.IsNotFound(e) })
			}
			retErr = apierrutil.Reduce(apierrutil.NewAggregate([]error{retErr, err}))
		}
	}()

	if !zone.DeletionTimestamp.IsZero() {
		r.reconcileDelete(zone)
		return ctrl.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(zone, cloudflareoperatoriov1.CloudflareOperatorFinalizer) {
		controllerutil.AddFinalizer(zone, cloudflareoperatoriov1.CloudflareOperatorFinalizer)
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	return r.reconcileZone(ctx, zone)
}

// reconcileZone reconciles the zone
func (r *ZoneReconciler) reconcileZone(ctx context.Context, zone *cloudflareoperatoriov1.Zone) (ctrl.Result, error) {
	cloudflareAPI, err := cloudflareAPIForAccountName(ctx, r.Client, zone.Spec.AccountRef.Name)
	if err != nil {
		if errors.Is(err, errWaitForAccount) {
			intconditions.MarkUnknown(zone, "Cloudflare account is not ready")
			return ctrl.Result{RequeueAfter: r.RetryInterval}, errWaitForAccount
		}
		intconditions.MarkFalse(zone, err)
		return ctrl.Result{RequeueAfter: r.RetryInterval}, nil
	}

	zoneID, err := cloudflareZoneIDByName(ctx, cloudflareAPI, canonicalDNSName(zone.Spec.Name))
	if err != nil {
		intconditions.MarkFalse(zone, err)
		return ctrl.Result{RequeueAfter: r.RetryInterval}, errWaitForZone
	}

	zone.Status.ID = zoneID

	if zone.Spec.Prune {
		if err := r.handlePrune(ctx, cloudflareAPI, zone); err != nil {
			intconditions.MarkFalse(zone, fmt.Errorf("failed to prune DNS records: %v", err))
			return ctrl.Result{RequeueAfter: r.RetryInterval}, nil
		}
	}

	intconditions.MarkTrue(zone, "Zone is ready")

	return ctrl.Result{RequeueAfter: positiveInterval(zone.Spec.Interval.Duration)}, nil
}

// handlePrune deletes DNS records that are not managed by the operator if enabled
func (r *ZoneReconciler) handlePrune(ctx context.Context, cloudflareAPI *cloudflareClient, zone *cloudflareoperatoriov1.Zone) error {
	log := ctrl.LoggerFrom(ctx)
	ignored := make(map[string][]*regexp.Regexp)
	for recordType, patterns := range zone.Spec.IgnoredRecords {
		for _, pattern := range patterns {
			if !strings.HasPrefix(pattern, "^") {
				pattern = "^" + regexp.QuoteMeta(pattern)
			}
			re, err := regexp.Compile(pattern)
			if err != nil {
				return fmt.Errorf("invalid %s pruning exclusion: %w", recordType, err)
			}
			ignored[recordType] = append(ignored[recordType], re)
		}
	}

	cloudflareDNSRecords, err := listCloudflareDNSRecords(ctx, cloudflareAPI, zone.Status.ID, dns.RecordListParams{})
	if err != nil {
		return err
	}
	// Read ownership after the remote snapshot, bypassing informer lag. A record
	// created during recovery is either still pending or already bound to its new ID.
	reader := r.APIReader
	if reader == nil {
		reader = r.Client
	}
	zones := &cloudflareoperatoriov1.ZoneList{}
	if err := reader.List(ctx, zones); err != nil {
		return err
	}
	dnsRecords := &cloudflareoperatoriov1.DNSRecordList{}
	if err := reader.List(ctx, dnsRecords); err != nil {
		return err
	}

	dnsRecordMap, dnsRecordSpecMap := managedDNSRecordKeysForZone(zone, dnsRecords.Items, zones.Items)

	for _, cloudflareDNSRecord := range cloudflareDNSRecords {
		recordType := string(cloudflareDNSRecord.Type)
		if matchesIgnored(canonicalDNSName(cloudflareDNSRecord.Name), ignored[recordType]) {
			continue
		}

		if _, found := dnsRecordMap[cloudflareDNSRecord.ID]; found {
			continue
		}
		if _, found := dnsRecordSpecMap[dnsRecordKey(recordType, cloudflareDNSRecord.Name)]; found {
			continue
		}

		if err := deleteCloudflareDNSRecord(ctx, cloudflareAPI, zone.Status.ID, cloudflareDNSRecord.ID); err != nil && !isCloudflareDNSRecordNotFound(err) {
			return err
		}
		log.Info("Deleted DNS record on Cloudflare", "name", cloudflareDNSRecord.Name)
	}
	return nil
}

func managedDNSRecordKeysForZone(zone *cloudflareoperatoriov1.Zone, dnsRecords []cloudflareoperatoriov1.DNSRecord, zones []cloudflareoperatoriov1.Zone) (map[string]struct{}, map[string]struct{}) {
	dnsRecordMap := make(map[string]struct{})
	dnsRecordSpecMap := make(map[string]struct{})

	for _, dnsRecord := range dnsRecords {
		if dnsRecord.Status.RecordID != "" && dnsRecord.Status.ZoneID != "" {
			if dnsRecord.Status.ZoneID == zone.Status.ID {
				dnsRecordMap[dnsRecord.Status.RecordID] = struct{}{}
			}
			continue
		}
		if matchedZone := findZoneForDNSRecord(dnsRecord.Spec.Name, zones); matchedZone == nil || matchedZone.Name != zone.Name {
			continue
		}

		if dnsRecord.Status.RecordID != "" {
			dnsRecordMap[dnsRecord.Status.RecordID] = struct{}{}
			continue
		}
		// Pending records reserve their name/type until their remote ID has been persisted.
		dnsRecordSpecMap[dnsRecordKey(dnsRecord.Spec.Type, dnsRecord.Spec.Name)] = struct{}{}
	}

	return dnsRecordMap, dnsRecordSpecMap
}

func dnsRecordKey(recordType, name string) string {
	return recordType + "/" + canonicalDNSName(name)
}

func (r *ZoneReconciler) requestsForAccountChange(ctx context.Context, o client.Object) []reconcile.Request {
	account, ok := o.(*cloudflareoperatoriov1.Account)
	if !ok {
		err := fmt.Errorf("expected an Account, got %T", o)
		ctrl.LoggerFrom(ctx).Error(err, "failed to get requests for account change")
		return nil
	}

	return r.requestsForAccountNames(ctx, map[string]struct{}{account.Name: {}})
}

func (r *ZoneReconciler) requestsForAccountSecretChange(ctx context.Context, o client.Object) []reconcile.Request {
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

func (r *ZoneReconciler) requestsForAccountNames(ctx context.Context, accountNames map[string]struct{}) []reconcile.Request {
	if len(accountNames) == 0 {
		return nil
	}

	var zones cloudflareoperatoriov1.ZoneList
	if err := r.List(ctx, &zones); err != nil {
		ctrl.LoggerFrom(ctx).Error(err, "failed to list Zones for account change")
		return nil
	}

	var accounts cloudflareoperatoriov1.AccountList
	if err := r.List(ctx, &accounts); err != nil {
		ctrl.LoggerFrom(ctx).Error(err, "failed to list Accounts for account change fallback")
		return nil
	}

	singleAccountFallback := len(accounts.Items) == 1
	reqs := make([]reconcile.Request, 0, len(zones.Items))
	for i := range zones.Items {
		zone := &zones.Items[i]
		switch {
		case zone.Spec.AccountRef.Name != "":
			if _, found := accountNames[zone.Spec.AccountRef.Name]; found {
				reqs = append(reqs, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(zone)})
			}
		case singleAccountFallback:
			if _, found := accountNames[accounts.Items[0].Name]; found {
				reqs = append(reqs, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(zone)})
			}
		}
	}

	return reqs
}

// reconcileDelete reconciles the deletion of the zone
func (r *ZoneReconciler) reconcileDelete(zone *cloudflareoperatoriov1.Zone) {
	metrics.ZoneFailureCounter.DeleteLabelValues(zone.Name, zone.Spec.Name)
	controllerutil.RemoveFinalizer(zone, cloudflareoperatoriov1.CloudflareOperatorFinalizer)
}

func matchesIgnored(name string, patterns []*regexp.Regexp) bool {
	for _, pattern := range patterns {
		if pattern.MatchString(name) {
			return true
		}
	}
	return false
}
