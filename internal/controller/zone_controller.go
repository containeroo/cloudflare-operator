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
	"strings"
	"time"

	"github.com/cloudflare/cloudflare-go"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apierrutil "k8s.io/apimachinery/pkg/util/errors"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	cloudflareoperatoriov1 "github.com/containeroo/cloudflare-operator/api/v1"
	"github.com/containeroo/cloudflare-operator/internal/common"
	"github.com/containeroo/cloudflare-operator/internal/metrics"
	"github.com/fluxcd/pkg/runtime/patch"
)

// ZoneReconciler reconciles a Zone object
type ZoneReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Cf     *cloudflare.API
}

// SetupWithManager sets up the controller with the Manager.
func (r *ZoneReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(ctx, &cloudflareoperatoriov1.Zone{}, cloudflareoperatoriov1.ZoneNameIndexKey,
		func(rawObj client.Object) []string {
			zone := rawObj.(*cloudflareoperatoriov1.Zone)
			return []string{zone.Spec.Name}
		}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&cloudflareoperatoriov1.Zone{}).
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

		if errors.Is(retErr, reconcile.TerminalError(nil)) || (retErr == nil && (result.IsZero() || !result.Requeue)) {
			patchOpts = append(patchOpts, patch.WithStatusObservedGeneration{})
		}

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

	if !controllerutil.ContainsFinalizer(zone, common.CloudflareOperatorFinalizer) {
		controllerutil.AddFinalizer(zone, common.CloudflareOperatorFinalizer)
		return ctrl.Result{Requeue: true}, nil
	}

	return r.reconcileZone(ctx, zone), nil
}

// reconcileZone reconciles the zone
func (r *ZoneReconciler) reconcileZone(ctx context.Context, zone *cloudflareoperatoriov1.Zone) ctrl.Result {
	if r.Cf.APIToken == "" {
		common.MarkUnknown(zone, "Cloudflare account is not ready")
		return ctrl.Result{RequeueAfter: time.Second * 5}
	}

	zoneID, err := r.Cf.ZoneIDByName(zone.Spec.Name)
	if err != nil {
		common.MarkFalse(zone, err)
		return ctrl.Result{}
	}

	zone.Status.ID = zoneID

	if zone.Spec.Prune {
		if err := r.handlePrune(ctx, zone); err != nil {
			return ctrl.Result{RequeueAfter: time.Second * 30}
		}
	}

	common.MarkTrue(zone, "Zone is ready")

	return ctrl.Result{RequeueAfter: zone.Spec.Interval.Duration}
}

// handlePrune deletes DNS records that are not managed by the operator if enabled
func (r *ZoneReconciler) handlePrune(ctx context.Context, zone *cloudflareoperatoriov1.Zone) error {
	log := ctrl.LoggerFrom(ctx)

	dnsRecords := &cloudflareoperatoriov1.DNSRecordList{}
	if err := r.List(ctx, dnsRecords); err != nil {
		log.Error(err, "Failed to list DNSRecords")
		return err
	}

	cfDnsRecords, _, err := r.Cf.ListDNSRecords(ctx, cloudflare.ZoneIdentifier(zone.Status.ID), cloudflare.ListDNSRecordsParams{})
	if err != nil {
		common.MarkFalse(zone, err)
		return err
	}

	dnsRecordMap := make(map[string]struct{})
	for _, dnsRecord := range dnsRecords.Items {
		dnsRecordMap[dnsRecord.Status.RecordID] = struct{}{}
	}

	for _, cfDnsRecord := range cfDnsRecords {
		if cfDnsRecord.Type == "TXT" && strings.HasPrefix(cfDnsRecord.Name, "_acme-challenge") {
			continue
		}

		if _, found := dnsRecordMap[cfDnsRecord.ID]; !found {
			if err := r.Cf.DeleteDNSRecord(ctx, cloudflare.ZoneIdentifier(zone.Status.ID), cfDnsRecord.ID); err != nil {
				common.MarkFalse(zone, err)
			}
			log.Info("Deleted DNS record on Cloudflare", "name", cfDnsRecord.Name)
		}
	}
	return nil
}

// reconcileDelete reconciles the deletion of the zone
func (r *ZoneReconciler) reconcileDelete(zone *cloudflareoperatoriov1.Zone) {
	metrics.ZoneFailureCounter.DeleteLabelValues(zone.Name, zone.Spec.Name)
	controllerutil.RemoveFinalizer(zone, common.CloudflareOperatorFinalizer)
}
