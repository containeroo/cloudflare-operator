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
	"strings"
	"time"

	"github.com/cloudflare/cloudflare-go"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	cloudflareoperatoriov1 "github.com/containeroo/cloudflare-operator/api/v1"
	"github.com/containeroo/cloudflare-operator/internal/common"
	"github.com/containeroo/cloudflare-operator/internal/metrics"
)

// ZoneReconciler reconciles a Zone object
type ZoneReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Cf     *cloudflare.API
}

// SetupWithManager sets up the controller with the Manager.
func (r *ZoneReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&cloudflareoperatoriov1.Zone{}).
		Complete(r)
}

// +kubebuilder:rbac:groups=cloudflare-operator.io,resources=zones,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cloudflare-operator.io,resources=zones/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=cloudflare-operator.io,resources=zones/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *ZoneReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	zone := &cloudflareoperatoriov1.Zone{}
	if err := r.Get(ctx, req.NamespacedName, zone); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !controllerutil.ContainsFinalizer(zone, common.CloudflareOperatorFinalizer) {
		controllerutil.AddFinalizer(zone, common.CloudflareOperatorFinalizer)
		if err := r.Update(ctx, zone); err != nil {
			log.Error(err, "Failed to update Zone finalizer")
			return ctrl.Result{}, err
		}
	}

	if !zone.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(zone, common.CloudflareOperatorFinalizer) {
			metrics.ZoneFailureCounter.DeleteLabelValues(zone.Name, zone.Spec.Name)
		}

		controllerutil.RemoveFinalizer(zone, common.CloudflareOperatorFinalizer)
		if err := r.Update(ctx, zone); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	metrics.ZoneFailureCounter.WithLabelValues(zone.Name, zone.Spec.Name).Set(0)

	if r.Cf.APIToken == "" {
		apimeta.SetStatusCondition(&zone.Status.Conditions, metav1.Condition{
			Type:               "Ready",
			Status:             "False",
			Reason:             "NotReady",
			Message:            "Cloudflare account is not yet ready",
			ObservedGeneration: zone.Generation,
		})
		if err := r.Status().Update(ctx, zone); err != nil {
			log.Error(err, "Failed to update Zone status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: time.Second * 5}, nil
	}

	zoneID, err := r.Cf.ZoneIDByName(zone.Spec.Name)
	if err != nil {
		if err := r.markFailed(ctx, zone, err.Error()); err != nil {
			log.Error(err, "Failed to update Zone status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, err
	}

	if zone.Status.ID != zoneID {
		zone.Status.ID = zoneID
		if err := r.Status().Update(ctx, zone); err != nil {
			log.Error(err, "Failed to update Zone status")
			return ctrl.Result{}, err
		}
	}

	if zone.Spec.Prune {
		dnsRecords := &cloudflareoperatoriov1.DNSRecordList{}
		if err := r.List(ctx, dnsRecords); err != nil {
			log.Error(err, "Failed to list DNSRecord resources")
			return ctrl.Result{RequeueAfter: time.Second * 30}, nil
		}

		cfDnsRecords, _, err := r.Cf.ListDNSRecords(ctx, cloudflare.ZoneIdentifier(zone.Status.ID), cloudflare.ListDNSRecordsParams{})
		if err != nil {
			if err := r.markFailed(ctx, zone, err.Error()); err != nil {
				log.Error(err, "Failed to update Zone status")
				return ctrl.Result{}, err
			}
			return ctrl.Result{RequeueAfter: time.Second * 30}, nil
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
					if err := r.markFailed(ctx, zone, err.Error()); err != nil {
						log.Error(err, "Failed to update Zone status")
					}
				}
				log.Info("Deleted DNS record on Cloudflare", "name", cfDnsRecord.Name)
			}
		}
	}

	apimeta.SetStatusCondition(&zone.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             "True",
		Reason:             "Ready",
		Message:            "Zone is ready",
		ObservedGeneration: zone.Generation,
	})
	if err := r.Status().Update(ctx, zone); err != nil {
		log.Error(err, "Failed to update Zone status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: zone.Spec.Interval.Duration}, nil
}

// markFailed marks the reconciled object as failed
func (r *ZoneReconciler) markFailed(ctx context.Context, zone *cloudflareoperatoriov1.Zone, message string) error {
	metrics.ZoneFailureCounter.WithLabelValues(zone.Name, zone.Spec.Name).Set(1)
	apimeta.SetStatusCondition(&zone.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             "False",
		Reason:             "Failed",
		Message:            message,
		ObservedGeneration: zone.Generation,
	})
	if err := r.Status().Update(ctx, zone); err != nil {
		return err
	}

	return nil
}
