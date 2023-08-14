/*
Copyright 2023 containeroo

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
	"encoding/json"
	"reflect"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"golang.org/x/net/publicsuffix"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/cloudflare/cloudflare-go"
	cfv1 "github.com/containeroo/cloudflare-operator/api/v1"
)

// DNSRecordReconciler reconciles a DNSRecord object
type DNSRecordReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Cf     *cloudflare.API
}

// +kubebuilder:rbac:groups=cloudflare-operator.io,resources=dnsrecords,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cloudflare-operator.io,resources=dnsrecords/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=cloudflare-operator.io,resources=dnsrecords/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *DNSRecordReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)

	instance := &cfv1.DNSRecord{}
	if err := r.Get(ctx, req.NamespacedName, instance); err != nil {
		if errors.IsNotFound(err) {
			log.Info("DNSRecord resource not found. Ignoring since object must be deleted.")
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get DNSRecord resource")
		return ctrl.Result{}, err
	}

	if r.Cf.APIToken == "" {
		apimeta.SetStatusCondition(&instance.Status.Conditions, metav1.Condition{
			Type:               "Ready",
			Status:             "False",
			Reason:             "NotReady",
			Message:            "Cloudflare account is not yet ready",
			ObservedGeneration: instance.Generation,
		})
		if err := r.Status().Update(ctx, instance); err != nil {
			log.Error(err, "Failed to update DNSRecord status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: time.Second * 5}, nil
	}

	zoneName, _ := publicsuffix.EffectiveTLDPlusOne(instance.Spec.Name)
	zoneName = strings.ReplaceAll(zoneName, ".", "-")

	zone := &cfv1.Zone{}
	if err := r.Get(ctx, client.ObjectKey{Name: zoneName}, zone); err != nil {
		if errors.IsNotFound(err) {
			if err := r.markFailed(instance, ctx, "Zone not found"); err != nil {
				log.Error(err, "Failed to update DNSRecord status")
				return ctrl.Result{}, err
			}
			return ctrl.Result{RequeueAfter: time.Second * 30}, err
		}
		log.Error(err, "Failed to get Zone resource")
		return ctrl.Result{RequeueAfter: time.Second * 30}, err
	}

	if condition := apimeta.FindStatusCondition(zone.Status.Conditions, "Ready"); condition == nil || condition.Status != "True" {
		apimeta.SetStatusCondition(&instance.Status.Conditions, metav1.Condition{
			Type:               "Ready",
			Status:             "False",
			Reason:             "NotReady",
			Message:            "Zone is not yet ready",
			ObservedGeneration: instance.Generation,
		})
		if err := r.Status().Update(ctx, instance); err != nil {
			log.Error(err, "Failed to update DNSRecord status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: time.Second * 5}, nil
	}

	if !controllerutil.ContainsFinalizer(instance, cloudflareOperatorFinalizer) {
		controllerutil.AddFinalizer(instance, cloudflareOperatorFinalizer)
		if err := r.Update(ctx, instance); err != nil {
			log.Error(err, "Failed to update DNSRecord finalizer")
			return ctrl.Result{}, err
		}
	}

	if instance.GetDeletionTimestamp() != nil {
		if controllerutil.ContainsFinalizer(instance, cloudflareOperatorFinalizer) {
			if err := r.finalizeDNSRecord(ctx, zone.Spec.ID, log, instance); err != nil && err.Error() != "Record does not exist. (81044)" {
				if err := r.markFailed(instance, ctx, err.Error()); err != nil {
					log.Error(err, "Failed to update DNSRecord status")
					return ctrl.Result{}, err
				}
				return ctrl.Result{RequeueAfter: time.Second * 30}, err
			}
			dnsRecordFailureCounter.DeleteLabelValues(instance.Namespace, instance.Name, instance.Spec.Name)
		}

		controllerutil.RemoveFinalizer(instance, cloudflareOperatorFinalizer)
		if err := r.Update(ctx, instance); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	dnsRecordFailureCounter.WithLabelValues(instance.Namespace, instance.Name, instance.Spec.Name).Set(0)

	var existingRecord cloudflare.DNSRecord

	if instance.Status.RecordID != "" {
		var err error
		existingRecord, err = r.Cf.GetDNSRecord(ctx, cloudflare.ZoneIdentifier(zone.Spec.ID), instance.Status.RecordID)
		if err != nil && err.Error() != "Record does not exist. (81044)" {
			log.Error(err, "Failed to get DNS record from Cloudflare")
			return ctrl.Result{RequeueAfter: time.Second * 30}, err
		}
	}

	if (instance.Spec.Type == "A" || instance.Spec.Type == "AAAA") && instance.Spec.IPRef.Name != "" {
		ip := &cfv1.IP{}
		if err := r.Get(ctx, client.ObjectKey{Name: instance.Spec.IPRef.Name}, ip); err != nil {
			if err := r.markFailed(instance, ctx, "IP object not found"); err != nil {
				log.Error(err, "Failed to update DNSRecord status")
				return ctrl.Result{}, err
			}
			return ctrl.Result{RequeueAfter: time.Second * 30}, err
		}
		if ip.Spec.Address != instance.Spec.Content {
			instance.Spec.Content = ip.Spec.Address
			if err := r.Update(ctx, instance); err != nil {
				log.Error(err, "Failed to update DNSRecord resource")
				return ctrl.Result{}, err
			}
		}
	}

	if *instance.Spec.Proxied && instance.Spec.TTL != 1 {
		if err := r.markFailed(instance, ctx, "TTL must be 1 when proxied"); err != nil {
			log.Error(err, "Failed to update DNSRecord status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	if existingRecord.ID == "" {
		newDNSRecord, err := r.Cf.CreateDNSRecord(ctx, cloudflare.ZoneIdentifier(zone.Spec.ID), cloudflare.CreateDNSRecordParams{
			Name:     instance.Spec.Name,
			Type:     instance.Spec.Type,
			Content:  instance.Spec.Content,
			TTL:      instance.Spec.TTL,
			Proxied:  instance.Spec.Proxied,
			Priority: instance.Spec.Priority,
			Data:     instance.Spec.Data,
		})
		if err != nil {
			if err := r.markFailed(instance, ctx, err.Error()); err != nil {
				log.Error(err, "Failed to update DNSRecord status")
				return ctrl.Result{}, err
			}
			return ctrl.Result{RequeueAfter: time.Second * 30}, err
		}
		apimeta.SetStatusCondition(&instance.Status.Conditions, metav1.Condition{
			Type:               "Ready",
			Status:             "True",
			Reason:             "Ready",
			Message:            "DNS record synced",
			ObservedGeneration: instance.Generation,
		})
		instance.Status.RecordID = newDNSRecord.ID
		if err := r.Status().Update(ctx, instance); err != nil {
			log.Error(err, "Failed to update DNSRecord status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: instance.Spec.Interval.Duration}, nil
	}

	if !compareDNSRecord(instance.Spec, existingRecord) {
		if _, err := r.Cf.UpdateDNSRecord(ctx, cloudflare.ZoneIdentifier(zone.Spec.ID), cloudflare.UpdateDNSRecordParams{
			ID:       existingRecord.ID,
			Name:     instance.Spec.Name,
			Type:     instance.Spec.Type,
			Content:  instance.Spec.Content,
			TTL:      instance.Spec.TTL,
			Proxied:  instance.Spec.Proxied,
			Priority: instance.Spec.Priority,
			Data:     instance.Spec.Data,
		}); err != nil {
			if err := r.markFailed(instance, ctx, err.Error()); err != nil {
				log.Error(err, "Failed to update DNSRecord status")
				return ctrl.Result{}, err
			}
			return ctrl.Result{RequeueAfter: time.Second * 30}, err
		}
		apimeta.SetStatusCondition(&instance.Status.Conditions, metav1.Condition{
			Type:               "Ready",
			Status:             "True",
			Reason:             "Ready",
			Message:            "DNS record synced",
			ObservedGeneration: instance.Generation,
		})
		instance.Status.RecordID = existingRecord.ID
		if err := r.Status().Update(ctx, instance); err != nil {
			log.Error(err, "Failed to update DNSRecord status")
			return ctrl.Result{}, err
		}
		log.Info("DNS record updated in cloudflare", "name", existingRecord.Name, "id", existingRecord.ID)
		return ctrl.Result{RequeueAfter: instance.Spec.Interval.Duration}, nil
	}

	apimeta.SetStatusCondition(&instance.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             "True",
		Reason:             "Ready",
		Message:            "DNS record synced",
		ObservedGeneration: instance.Generation,
	})
	instance.Status.RecordID = existingRecord.ID
	if err := r.Status().Update(ctx, instance); err != nil {
		log.Error(err, "Failed to update DNSRecord status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: instance.Spec.Interval.Duration}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *DNSRecordReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&cfv1.DNSRecord{}).
		Complete(r)
}

// finalizeDNSRecord deletes the DNS record from cloudflare
func (r *DNSRecordReconciler) finalizeDNSRecord(ctx context.Context, dnsRecordZoneId string, log logr.Logger, d *cfv1.DNSRecord) error {
	if err := r.Cf.DeleteDNSRecord(ctx, cloudflare.ZoneIdentifier(dnsRecordZoneId), d.Status.RecordID); err != nil {
		log.Error(err, "Failed to delete DNS record in Cloudflare. Record may still exist in Cloudflare")
		return err
	}
	return nil
}

// markFailed marks the reconciled object as failed
func (r *DNSRecordReconciler) markFailed(instance *cfv1.DNSRecord, ctx context.Context, message string) error {
	dnsRecordFailureCounter.WithLabelValues(instance.Namespace, instance.Name, instance.Spec.Name).Set(1)
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

// compareDNSRecord compares the DNS record to the instance
func compareDNSRecord(instance cfv1.DNSRecordSpec, existingRecord cloudflare.DNSRecord) bool {
	var isEqual bool = true

	if instance.Name != existingRecord.Name {
		isEqual = false
	}
	if instance.Type != existingRecord.Type {
		isEqual = false
	}
	if instance.Type != "SRV" && instance.Type != "LOC" && instance.Type != "CAA" {
		if instance.Content != existingRecord.Content {
			isEqual = false
		}
	}
	if instance.TTL != existingRecord.TTL {
		isEqual = false
	}
	if *instance.Proxied != *existingRecord.Proxied {
		isEqual = false
	}
	if !comparePriority(instance.Priority, existingRecord.Priority) {
		isEqual = false
	}
	if !compareData(existingRecord.Data, instance.Data) {
		isEqual = false
	}
	return isEqual
}
