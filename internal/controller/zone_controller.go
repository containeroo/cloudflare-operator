/*
Copyright 2024 containeroo

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

	instance := &cloudflareoperatoriov1.Zone{}
	if err := r.Get(ctx, req.NamespacedName, instance); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !controllerutil.ContainsFinalizer(instance, common.CloudflareOperatorFinalizer) {
		controllerutil.AddFinalizer(instance, common.CloudflareOperatorFinalizer)
		if err := r.Update(ctx, instance); err != nil {
			log.Error(err, "Failed to update Zone finalizer")
			return ctrl.Result{}, err
		}
	}

	if !instance.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(instance, common.CloudflareOperatorFinalizer) {
			metrics.ZoneFailureCounter.DeleteLabelValues(instance.Name, instance.Spec.Name)
		}

		controllerutil.RemoveFinalizer(instance, common.CloudflareOperatorFinalizer)
		if err := r.Update(ctx, instance); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	metrics.ZoneFailureCounter.WithLabelValues(instance.Name, instance.Spec.Name).Set(0)

	if r.Cf.APIToken == "" {
		apimeta.SetStatusCondition(&instance.Status.Conditions, metav1.Condition{
			Type:               "Ready",
			Status:             "False",
			Reason:             "NotReady",
			Message:            "Cloudflare account is not yet ready",
			ObservedGeneration: instance.Generation,
		})
		if err := r.Status().Update(ctx, instance); err != nil {
			log.Error(err, "Failed to update Zone status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: time.Second * 5}, nil
	}

	if _, err := r.Cf.ZoneDetails(ctx, instance.Spec.ID); err != nil {
		if err := r.markFailed(instance, ctx, err.Error()); err != nil {
			log.Error(err, "Failed to update Zone status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: time.Second * 30}, err
	}

	dnsRecords := &cloudflareoperatoriov1.DNSRecordList{}
	if err := r.List(ctx, dnsRecords); err != nil {
		log.Error(err, "Failed to list DNSRecord resources")
		return ctrl.Result{RequeueAfter: time.Second * 30}, err
	}

	cfDnsRecords, _, err := r.Cf.ListDNSRecords(ctx, cloudflare.ZoneIdentifier(instance.Spec.ID), cloudflare.ListDNSRecordsParams{})
	if err != nil {
		if err := r.markFailed(instance, ctx, err.Error()); err != nil {
			log.Error(err, "Failed to update Zone status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: time.Second * 30}, err
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
			if err := r.Cf.DeleteDNSRecord(ctx, cloudflare.ZoneIdentifier(instance.Spec.ID), cfDnsRecord.ID); err != nil {
				if err := r.markFailed(instance, ctx, err.Error()); err != nil {
					log.Error(err, "Failed to update Zone status")
				}
			}
			log.Info("Deleted DNS record on Cloudflare " + cfDnsRecord.Name)
		}
	}

	apimeta.SetStatusCondition(&instance.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             "True",
		Reason:             "Ready",
		Message:            "Zone is ready",
		ObservedGeneration: instance.Generation,
	})
	if err := r.Status().Update(ctx, instance); err != nil {
		log.Error(err, "Failed to update Zone status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: instance.Spec.Interval.Duration}, nil
}

// markFailed marks the reconciled object as failed
func (r *ZoneReconciler) markFailed(instance *cloudflareoperatoriov1.Zone, ctx context.Context, message string) error {
	metrics.ZoneFailureCounter.WithLabelValues(instance.Name, instance.Spec.Name).Set(1)
	apimeta.SetStatusCondition(&instance.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             "False",
		Reason:             "Failed",
		Message:            message,
		ObservedGeneration: instance.Generation,
	})
	if err := r.Status().Update(ctx, instance); err != nil {
		return err
	}
	return nil
}
