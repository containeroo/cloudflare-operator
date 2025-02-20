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
	"time"

	"github.com/fluxcd/pkg/runtime/conditions"
	"github.com/fluxcd/pkg/runtime/patch"
	"golang.org/x/net/publicsuffix"
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
	"github.com/containeroo/cloudflare-operator/internal/metrics"
	intpredicates "github.com/containeroo/cloudflare-operator/internal/predicates"
)

// DNSRecordReconciler reconciles a DNSRecord object
type DNSRecordReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Cf     *cloudflare.API
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
	if err := mgr.GetFieldIndexer().IndexField(ctx, &cloudflareoperatoriov1.DNSRecord{}, cloudflareoperatoriov1.OwnerRefUIDIndexKey,
		func(o client.Object) []string {
			obj := o.(*cloudflareoperatoriov1.DNSRecord)
			ownerReferences := obj.GetOwnerReferences()
			var ownerReferencesUID string
			for _, ownerReference := range ownerReferences {
				if ownerReference.Kind != "Ingress" {
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

		if errors.Is(retErr, reconcile.TerminalError(nil)) || (retErr == nil && (result.IsZero() || !result.Requeue)) {
			patchOpts = append(patchOpts, patch.WithStatusObservedGeneration{})
		}

		if err := patchHelper.Patch(ctx, dnsrecord, patchOpts...); err != nil {
			if !dnsrecord.DeletionTimestamp.IsZero() {
				err = apierrutil.FilterOut(err, func(e error) bool { return apierrors.IsNotFound(e) })
			}
			retErr = apierrutil.Reduce(apierrutil.NewAggregate([]error{retErr, err}))
		}
	}()

	zoneName, _ := publicsuffix.EffectiveTLDPlusOne(dnsrecord.Spec.Name)

	zones := &cloudflareoperatoriov1.ZoneList{}
	if err := r.List(ctx, zones, client.MatchingFields{cloudflareoperatoriov1.ZoneNameIndexKey: zoneName}); err != nil {
		log.Error(err, "Failed to list zones")
		return ctrl.Result{RequeueAfter: time.Second * 30}, nil
	}

	if len(zones.Items) == 0 {
		intconditions.MarkFalse(dnsrecord, fmt.Errorf("Zone %q not found", zoneName))
		return ctrl.Result{}, nil
	}

	zone := &zones.Items[0]

	if !dnsrecord.DeletionTimestamp.IsZero() {
		if err := r.reconcileDelete(ctx, zone.Status.ID, dnsrecord); err != nil {
			log.Error(err, "Failed to delete DNS record in Cloudflare, record may still exist in Cloudflare")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(dnsrecord, cloudflareoperatoriov1.CloudflareOperatorFinalizer) {
		controllerutil.AddFinalizer(dnsrecord, cloudflareoperatoriov1.CloudflareOperatorFinalizer)
		return ctrl.Result{Requeue: true}, nil
	}

	return r.reconcileDNSRecord(ctx, dnsrecord, zone), nil
}

// reconcileDNSRecord reconciles the dnsrecord
func (r *DNSRecordReconciler) reconcileDNSRecord(ctx context.Context, dnsrecord *cloudflareoperatoriov1.DNSRecord, zone *cloudflareoperatoriov1.Zone) ctrl.Result {
	if r.Cf.APIToken == "" {
		intconditions.MarkUnknown(dnsrecord, "Cloudflare account is not ready")
		return ctrl.Result{RequeueAfter: time.Second * 5}
	}

	if !conditions.IsTrue(zone, cloudflareoperatoriov1.ConditionTypeReady) {
		intconditions.MarkUnknown(dnsrecord, "Zone is not ready")
		return ctrl.Result{RequeueAfter: time.Second * 5}
	}

	var existingRecord cloudflare.DNSRecord
	if dnsrecord.Status.RecordID != "" {
		var err error
		existingRecord, err = r.Cf.GetDNSRecord(ctx, cloudflare.ZoneIdentifier(zone.Status.ID), dnsrecord.Status.RecordID)
		if err != nil {
			intconditions.MarkFalse(dnsrecord, err)
			return ctrl.Result{}
		}
	} else {
		cfExistingRecords, _, err := r.Cf.ListDNSRecords(ctx, cloudflare.ZoneIdentifier(zone.Status.ID), cloudflare.ListDNSRecordsParams{
			Type:    dnsrecord.Spec.Type,
			Name:    dnsrecord.Spec.Name,
			Content: dnsrecord.Spec.Content,
		})
		if err != nil {
			intconditions.MarkFalse(dnsrecord, err)
			return ctrl.Result{}
		}
		if len(cfExistingRecords) > 0 {
			existingRecord = cfExistingRecords[0]
		}
		dnsrecord.Status.RecordID = existingRecord.ID
	}

	if (dnsrecord.Spec.Type == "A" || dnsrecord.Spec.Type == "AAAA") && dnsrecord.Spec.IPRef.Name != "" {
		ip := &cloudflareoperatoriov1.IP{}
		if err := r.Get(ctx, client.ObjectKey{Name: dnsrecord.Spec.IPRef.Name}, ip); err != nil {
			intconditions.MarkFalse(dnsrecord, err)
			return ctrl.Result{RequeueAfter: time.Second * 30}
		}
		dnsrecord.Spec.Content = ip.Spec.Address
	}

	if *dnsrecord.Spec.Proxied && dnsrecord.Spec.TTL != 1 {
		intconditions.MarkFalse(dnsrecord, errors.New("TTL must be 1 when proxied"))
		return ctrl.Result{}
	}

	if existingRecord.ID == "" {
		newDNSRecord, err := r.Cf.CreateDNSRecord(ctx, cloudflare.ZoneIdentifier(zone.Status.ID), cloudflare.CreateDNSRecordParams{
			Name:     dnsrecord.Spec.Name,
			Type:     dnsrecord.Spec.Type,
			Content:  dnsrecord.Spec.Content,
			TTL:      dnsrecord.Spec.TTL,
			Proxied:  dnsrecord.Spec.Proxied,
			Priority: dnsrecord.Spec.Priority,
			Data:     dnsrecord.Spec.Data,
		})
		if err != nil {
			intconditions.MarkFalse(dnsrecord, err)
			return ctrl.Result{RequeueAfter: time.Second * 30}
		}
		dnsrecord.Status.RecordID = newDNSRecord.ID
	} else if !r.compareDNSRecord(dnsrecord.Spec, existingRecord) {
		if _, err := r.Cf.UpdateDNSRecord(ctx, cloudflare.ZoneIdentifier(zone.Status.ID), cloudflare.UpdateDNSRecordParams{
			ID:       dnsrecord.Status.RecordID,
			Name:     dnsrecord.Spec.Name,
			Type:     dnsrecord.Spec.Type,
			Content:  dnsrecord.Spec.Content,
			TTL:      dnsrecord.Spec.TTL,
			Proxied:  dnsrecord.Spec.Proxied,
			Priority: dnsrecord.Spec.Priority,
			Data:     dnsrecord.Spec.Data,
		}); err != nil {
			intconditions.MarkFalse(dnsrecord, err)
			return ctrl.Result{RequeueAfter: time.Second * 30}
		}
	}

	intconditions.MarkTrue(dnsrecord, "DNS record synced")

	return ctrl.Result{RequeueAfter: dnsrecord.Spec.Interval.Duration}
}

// compareDNSRecord compares the DNS record to the DNSRecord object
func (r *DNSRecordReconciler) compareDNSRecord(dnsRecordSpec cloudflareoperatoriov1.DNSRecordSpec, existingRecord cloudflare.DNSRecord) bool {
	var isEqual bool = true

	if dnsRecordSpec.Name != existingRecord.Name {
		isEqual = false
	}
	if dnsRecordSpec.Type != existingRecord.Type {
		isEqual = false
	}
	if dnsRecordSpec.Type != "SRV" && dnsRecordSpec.Type != "LOC" && dnsRecordSpec.Type != "CAA" {
		if dnsRecordSpec.Content != existingRecord.Content {
			isEqual = false
		}
	}
	if dnsRecordSpec.TTL != existingRecord.TTL {
		isEqual = false
	}
	if *dnsRecordSpec.Proxied != *existingRecord.Proxied {
		isEqual = false
	}
	if !comparePriority(dnsRecordSpec.Priority, existingRecord.Priority) {
		isEqual = false
	}
	if !compareData(existingRecord.Data, dnsRecordSpec.Data) {
		isEqual = false
	}

	return isEqual
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
func compareData(a interface{}, b *apiextensionsv1.JSON) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	var bb interface{}
	if err := json.Unmarshal(b.Raw, &bb); err != nil {
		return false
	}

	return reflect.DeepEqual(a, bb)
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

// reconcileDelete reconciles the deletion of the dnsrecord
func (r *DNSRecordReconciler) reconcileDelete(ctx context.Context, zoneID string, dnsrecord *cloudflareoperatoriov1.DNSRecord) error {
	if err := r.Cf.DeleteDNSRecord(ctx, cloudflare.ZoneIdentifier(zoneID), dnsrecord.Status.RecordID); err != nil && err.Error() != "Record does not exist. (81044)" && dnsrecord.Status.RecordID != "" {
		return err
	}
	metrics.DnsRecordFailureCounter.DeleteLabelValues(dnsrecord.Namespace, dnsrecord.Name, dnsrecord.Spec.Name)
	controllerutil.RemoveFinalizer(dnsrecord, cloudflareoperatoriov1.CloudflareOperatorFinalizer)

	return nil
}
