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
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"reflect"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	"strings"
	"time"

	"github.com/cloudflare/cloudflare-go"
	cfv1beta1 "github.com/containeroo/cloudflare-operator/api/v1beta1"
)

// DNSRecordReconciler reconciles a DNSRecord object
type DNSRecordReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Cf     *cloudflare.API
}

// +kubebuilder:rbac:groups=cf.containeroo.ch,resources=dnsrecords,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cf.containeroo.ch,resources=dnsrecords/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=cf.containeroo.ch,resources=dnsrecords/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *DNSRecordReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)

	instance := &cfv1beta1.DNSRecord{}
	err := r.Get(ctx, req.NamespacedName, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			log.Info("DNSRecord resource not found. Ignoring since object must be deleted.")
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get DNSRecord resource")
		return ctrl.Result{}, err
	}

	if r.Cf.APIKey == "" {
		instance.Status.Phase = "Pending"
		instance.Status.Message = "Cloudflare account not ready"
		err := r.Status().Update(ctx, instance)
		if err != nil {
			log.Error(err, "Failed to update DNSRecord status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: time.Second * 5}, err
	}

	zones := &cfv1beta1.ZoneList{}
	err = r.List(ctx, zones)
	if err != nil {
		log.Error(err, "Failed to list Zone resources")
		return ctrl.Result{RequeueAfter: time.Second * 30}, err
	}

	var dnsRecordZone cfv1beta1.Zone
	for _, zone := range zones.Items {
		if strings.HasSuffix(instance.Spec.Name, zone.Spec.Name) {
			dnsRecordZone = zone
			break
		}
	}

	if dnsRecordZone.Name == "" {
		err := r.markFailed(instance, ctx, "Zone not found")
		if err != nil {
			log.Error(err, "Failed to update DNSRecord status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: time.Second * 30}, err
	}

	if dnsRecordZone.Status.Phase != "Active" {
		instance.Status.Phase = "Pending"
		instance.Status.Message = "Zone not ready"
		err := r.Status().Update(ctx, instance)
		if err != nil {
			log.Error(err, "Failed to update DNSRecord status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: time.Second * 5}, nil
	}

	dnsRecordZoneId := dnsRecordZone.Spec.ID

	if !controllerutil.ContainsFinalizer(instance, cloudflareOperatorFinalizer) {
		controllerutil.AddFinalizer(instance, cloudflareOperatorFinalizer)
		err := r.Update(ctx, instance)
		if err != nil {
			log.Error(err, "Failed to update DNSRecord finalizer")
			return ctrl.Result{}, err
		}
	}

	if instance.GetDeletionTimestamp() != nil {
		if controllerutil.ContainsFinalizer(instance, cloudflareOperatorFinalizer) {
			r.finalizeDNSRecord(ctx, dnsRecordZoneId, log, instance)
			dnsRecordFailureCounter.DeleteLabelValues(instance.Namespace, instance.Name, instance.Spec.Name)
		}

		controllerutil.RemoveFinalizer(instance, cloudflareOperatorFinalizer)
		err := r.Update(ctx, instance)
		if err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	dnsRecordFailureCounter.WithLabelValues(instance.Namespace, instance.Name, instance.Spec.Name).Set(0)

	existingRecords, err := r.Cf.DNSRecords(ctx, dnsRecordZoneId, cloudflare.DNSRecord{})
	if err != nil {
		log.Error(err, "Failed to get DNS records from Cloudflare")
		return ctrl.Result{RequeueAfter: time.Second * 30}, err
	}

	var existingRecord cloudflare.DNSRecord
	for _, record := range existingRecords {
		if record.ID != instance.Status.RecordID {
			continue
		}
		existingRecord = record
	}

	if instance.Spec.Content == "" && instance.Spec.IPRef.Name == "" {
		err := r.markFailed(instance, ctx, "No content or IP reference provided")
		if err != nil {
			log.Error(err, "Failed to update DNSRecord status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	if (instance.Spec.Type == "A" || instance.Spec.Type == "AAAA") && instance.Spec.IPRef.Name != "" {
		ip := &cfv1beta1.IP{}
		err := r.Get(ctx, client.ObjectKey{Name: instance.Spec.IPRef.Name}, ip)
		if err != nil {
			err := r.markFailed(instance, ctx, "IP object not found")
			if err != nil {
				log.Error(err, "Failed to update DNSRecord status")
				return ctrl.Result{}, err
			}
			return ctrl.Result{RequeueAfter: time.Second * 30}, err
		}
		instance.Spec.Content = ip.Spec.Address
		err = r.Update(ctx, instance)
		if err != nil {
			log.Error(err, "Failed to update DNSRecord resource")
			return ctrl.Result{}, err
		}
	}

	if *instance.Spec.Proxied && instance.Spec.TTL != 1 {
		err := r.markFailed(instance, ctx, "TTL must be 1 when proxied")
		if err != nil {
			log.Error(err, "Failed to update DNSRecord status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	if existingRecord.ID == "" {
		resp, err := r.Cf.CreateDNSRecord(ctx, dnsRecordZoneId, cloudflare.DNSRecord{
			Name:     instance.Spec.Name,
			Type:     instance.Spec.Type,
			Content:  instance.Spec.Content,
			TTL:      instance.Spec.TTL,
			Proxied:  instance.Spec.Proxied,
			Priority: instance.Spec.Priority,
			Data:     instance.Spec.Data,
		})
		if err != nil {
			err := r.markFailed(instance, ctx, err.Error())
			if err != nil {
				log.Error(err, "Failed to update DNSRecord status")
				return ctrl.Result{}, err
			}
			return ctrl.Result{RequeueAfter: time.Second * 30}, err
		}
		instance.Status.Phase = "Created"
		instance.Status.RecordID = resp.Result.ID
		instance.Status.Message = ""
		err = r.Status().Update(ctx, instance)
		if err != nil {
			log.Error(err, "Failed to update DNSRecord status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: instance.Spec.Interval.Duration}, nil
	}

	if existingRecord.Name != instance.Spec.Name ||
		existingRecord.Type != instance.Spec.Type ||
		existingRecord.Content != instance.Spec.Content ||
		existingRecord.TTL != instance.Spec.TTL ||
		*existingRecord.Proxied != *instance.Spec.Proxied ||
		existingRecord.Priority != instance.Spec.Priority || // this doesn't work
		!reflect.DeepEqual(existingRecord.Data, instance.Spec.Data) { // this doesn't work
		err := r.Cf.UpdateDNSRecord(ctx, dnsRecordZoneId, existingRecord.ID, cloudflare.DNSRecord{
			Name:     instance.Spec.Name,
			Type:     instance.Spec.Type,
			Content:  instance.Spec.Content,
			TTL:      instance.Spec.TTL,
			Proxied:  instance.Spec.Proxied,
			Priority: instance.Spec.Priority,
			Data:     instance.Spec.Data,
		})
		if err != nil {
			err := r.markFailed(instance, ctx, err.Error())
			if err != nil {
				log.Error(err, "Failed to update DNSRecord status")
				return ctrl.Result{}, err
			}
			return ctrl.Result{RequeueAfter: time.Second * 30}, err
		}
		if !reflect.DeepEqual(instance.Status, cfv1beta1.DNSRecordStatus{Phase: "Created", RecordID: existingRecord.ID, Message: ""}) {
			instance.Status.Phase = "Created"
			instance.Status.RecordID = existingRecord.ID
			instance.Status.Message = ""
		}
		err = r.Status().Update(ctx, instance)
		if err != nil {
			log.Error(err, "Failed to update DNSRecord status")
			return ctrl.Result{}, err
		}
		log.Info("DNS record updated in cloudflare", "name", existingRecord.Name, "id", existingRecord.ID)
		return ctrl.Result{RequeueAfter: instance.Spec.Interval.Duration}, nil
	}

	if !reflect.DeepEqual(instance.Status, cfv1beta1.DNSRecordStatus{Phase: "Created", RecordID: existingRecord.ID, Message: ""}) {
		instance.Status.Phase = "Created"
		instance.Status.RecordID = existingRecord.ID
		instance.Status.Message = ""
		err := r.Status().Update(ctx, instance)
		if err != nil {
			log.Error(err, "Failed to update DNSRecord status")
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{RequeueAfter: instance.Spec.Interval.Duration}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *DNSRecordReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&cfv1beta1.DNSRecord{}).
		Complete(r)
}

// finalizeDNSRecord deletes the DNS record from cloudflare
func (r *DNSRecordReconciler) finalizeDNSRecord(ctx context.Context, dnsRecordZoneId string, log logr.Logger, d *cfv1beta1.DNSRecord) {
	err := r.Cf.DeleteDNSRecord(ctx, dnsRecordZoneId, d.Status.RecordID)
	if err != nil {
		log.Error(err, "Failed to delete DNS record in Cloudflare. Record may still exist in Cloudflare")
	}
}

// markFailed marks the reconciled object as failed
func (r *DNSRecordReconciler) markFailed(instance *cfv1beta1.DNSRecord, ctx context.Context, message string) error {
	dnsRecordFailureCounter.WithLabelValues(instance.Namespace, instance.Name, instance.Spec.Name).Set(1)
	instance.Status.Phase = "Failed"
	instance.Status.Message = message
	if err := r.Status().Update(ctx, instance); err != nil {
		return err
	}
	return nil
}
