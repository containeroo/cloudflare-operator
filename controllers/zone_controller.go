/*
Copyright 2022 containeroo

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

package controllers

import (
	"context"
	"github.com/cloudflare/cloudflare-go"
	"k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	cfv1beta1 "github.com/containeroo/cloudflare-operator/api/v1beta1"
)

// ZoneReconciler reconciles a Zone object
type ZoneReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Cf     *cloudflare.API
}

// +kubebuilder:rbac:groups=cf.containeroo.ch,resources=zones,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cf.containeroo.ch,resources=zones/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=cf.containeroo.ch,resources=zones/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *ZoneReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)

	instance := &cfv1beta1.Zone{}
	err := r.Get(ctx, req.NamespacedName, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			log.Info("Zone resource not found. Ignoring since object must be deleted.")
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get Zone resource")
		return ctrl.Result{}, err
	}

	if !controllerutil.ContainsFinalizer(instance, cloudflareOperatorFinalizer) {
		controllerutil.AddFinalizer(instance, cloudflareOperatorFinalizer)
		err := r.Update(ctx, instance)
		if err != nil {
			log.Error(err, "Failed to update Zone finalizer")
			return ctrl.Result{}, err
		}
	}

	if instance.GetDeletionTimestamp() != nil {
		if controllerutil.ContainsFinalizer(instance, cloudflareOperatorFinalizer) {
			zoneFailureCounter.DeleteLabelValues(instance.Name, instance.Spec.Name)
		}

		controllerutil.RemoveFinalizer(instance, cloudflareOperatorFinalizer)
		err := r.Update(ctx, instance)
		if err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	zoneFailureCounter.WithLabelValues(instance.Name, instance.Spec.Name).Set(0)

	if r.Cf.APIKey == "" {
		instance.Status.Phase = "Pending"
		instance.Status.Message = "Cloudflare account not ready"
		err := r.Status().Update(ctx, instance)
		if err != nil {
			log.Error(err, "Failed to update Zone status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: time.Second * 5}, err
	}

	_, err = r.Cf.ZoneDetails(ctx, instance.Spec.ID)
	if err != nil {
		err := r.markFailed(instance, ctx, err.Error())
		if err != nil {
			log.Error(err, "Failed to update Zone status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, err
	}

	if instance.Status.Phase != "Active" {
		instance.Status.Phase = "Active"
		instance.Status.Message = ""
		err := r.Status().Update(ctx, instance)
		if err != nil {
			log.Error(err, "Failed to update Zone status")
			return ctrl.Result{}, err
		}
	}

	dnsRecords := &cfv1beta1.DNSRecordList{}
	err = r.List(ctx, dnsRecords, client.InNamespace(instance.Namespace))
	if err != nil {
		log.Error(err, "Failed to list DNSRecord resources")
		return ctrl.Result{}, err
	}

	cfDnsRecords, err := r.Cf.DNSRecords(ctx, instance.Spec.ID, cloudflare.DNSRecord{})
	if err != nil {
		err := r.markFailed(instance, ctx, err.Error())
		if err != nil {
			log.Error(err, "Failed to update Zone status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, err
	}

	for _, cfDnsRecord := range cfDnsRecords {
		if !strings.HasSuffix(cfDnsRecord.Name, instance.Spec.Name) {
			continue
		}
		if cfDnsRecord.Type != "A" && cfDnsRecord.Type != "CNAME" {
			continue
		}

		found := false
		for _, dnsRecord := range dnsRecords.Items {
			if dnsRecord.Spec.Name == cfDnsRecord.Name {
				found = true
				break
			}
		}
		if !found {
			err = r.Cf.DeleteDNSRecord(ctx, instance.Spec.ID, cfDnsRecord.ID)
			if err != nil {
				err := r.markFailed(instance, ctx, err.Error())
				if err != nil {
					log.Error(err, "Failed to update Zone status")
				}
			}
			log.Info("Deleted DNS record on Cloudflare " + cfDnsRecord.Name)
		}
	}

	return ctrl.Result{RequeueAfter: instance.Spec.Interval.Duration}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ZoneReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&cfv1beta1.Zone{}).
		Complete(r)
}

// markFailed marks the reconciled object as failed
func (r *ZoneReconciler) markFailed(instance *cfv1beta1.Zone, ctx context.Context, message string) error {
	zoneFailureCounter.WithLabelValues(instance.Name, instance.Spec.Name).Set(1)
	instance.Status.Phase = "Failed"
	instance.Status.Message = message
	if err := r.Status().Update(ctx, instance); err != nil {
		return err
	}
	return nil
}
