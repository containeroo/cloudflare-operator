/*
Copyright 2022.

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
	cfv1alpha1 "github.com/containeroo/cloudflare-operator/api/v1alpha1"
)

// DNSRecordReconciler reconciles a DNSRecord object
type DNSRecordReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Cf     *cloudflare.API
}

//+kubebuilder:rbac:groups=cf.containeroo.ch,resources=dnsrecords,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=cf.containeroo.ch,resources=dnsrecords/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=cf.containeroo.ch,resources=dnsrecords/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the DNSRecord object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.10.0/pkg/reconcile
func (r *DNSRecordReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)

	// Fetch the DNSRecord instance
	instance := &cfv1alpha1.DNSRecord{}
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
		return ctrl.Result{RequeueAfter: 5 * time.Second}, err
	}

	// Get all Zone objects
	zones := &cfv1alpha1.ZoneList{}
	err = r.List(ctx, zones)
	if err != nil {
		log.Error(err, "Failed to list Zone resources")
		return ctrl.Result{}, err
	}

	// Get the zone for the DNSRecord
	var dnsRecordZone cfv1alpha1.Zone
	for _, zone := range zones.Items {
		if strings.HasSuffix(instance.Spec.Name, zone.Spec.Name) {
			dnsRecordZone = zone
			break
		}
	}

	if dnsRecordZone.Name == "" {
		instance.Status.Phase = "Failed"
		instance.Status.Message = "Zone not found"
		err := r.Status().Update(ctx, instance)
		if err != nil {
			log.Error(err, "Failed to update DNSRecord status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, err
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

	// Check if the DNS record already exists
	existingRecords, err := r.Cf.DNSRecords(ctx, dnsRecordZoneId, cloudflare.DNSRecord{Name: instance.Spec.Name})
	if err != nil {
		log.Error(err, "Failed to get DNS records from Cloudflare")
		return ctrl.Result{}, err
	}

	if instance.Spec.Content == "" && instance.Spec.IpRef.Name == "" {
		instance.Status.Phase = "Failed"
		instance.Status.Message = "DNSRecord content is empty. Either content or ipRef must be set"
		if err := r.Status().Update(ctx, instance); err != nil {
			log.Error(err, "Failed to update DNSRecord status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, err
	}

	// Check if there is an IP reference in the DNSRecord
	if instance.Spec.Type == "A" && instance.Spec.IpRef.Name != "" {
		ip := &cfv1alpha1.IP{}
		err := r.Get(ctx, client.ObjectKey{Name: instance.Spec.IpRef.Name}, ip)
		if err != nil {
			instance.Status.Phase = "Failed"
			instance.Status.Message = "IP object not found"
			err := r.Status().Update(ctx, instance)
			if err != nil {
				log.Error(err, "Failed to update DNSRecord status")
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, err
		}
		instance.Spec.Content = ip.Spec.Address
		err = r.Update(ctx, instance)
		if err != nil {
			log.Error(err, "Failed to update DNSRecord resource")
			return ctrl.Result{}, err
		}
	}

	// Check if DNS record proxied is true and ttl is not 1
	if *instance.Spec.Proxied == true && instance.Spec.TTL != 1 {
		instance.Status.Phase = "Failed"
		instance.Status.Message = "DNSRecord is proxied and ttl is not 1"
		err := r.Status().Update(ctx, instance)
		if err != nil {
			log.Error(err, "Failed to update DNSRecord status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, err
	}

	// Record doesn't exist, create it
	if existingRecords == nil {
		resp, err := r.Cf.CreateDNSRecord(ctx, dnsRecordZoneId, cloudflare.DNSRecord{
			Name:    instance.Spec.Name,
			Type:    instance.Spec.Type,
			Content: instance.Spec.Content,
			TTL:     instance.Spec.TTL,
			Proxied: instance.Spec.Proxied,
		})
		if err != nil {
			instance.Status.Phase = "Failed"
			instance.Status.Message = err.Error()
			err := r.Status().Update(ctx, instance)
			if err != nil {
				log.Error(err, "Failed to update DNS record status")
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, err
		}
		instance.Status.Phase = "Created"
		instance.Status.RecordID = resp.Result.ID
		instance.Status.Message = ""
		err = r.Status().Update(ctx, instance)
		if err != nil {
			log.Error(err, "Failed to update DNS record status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: instance.Spec.Interval.Duration}, nil
	}

	// Extract record from slice
	existingRecord := existingRecords[0]
	// Ensure the DNS record is the same as the spec
	if existingRecord.Name != instance.Spec.Name ||
		existingRecord.Type != instance.Spec.Type ||
		existingRecord.Content != instance.Spec.Content ||
		existingRecord.TTL != instance.Spec.TTL ||
		*existingRecord.Proxied != *instance.Spec.Proxied {
		// Update the DNS record
		err := r.Cf.UpdateDNSRecord(ctx, dnsRecordZoneId, existingRecord.ID, cloudflare.DNSRecord{
			Name:    instance.Spec.Name,
			Type:    instance.Spec.Type,
			Content: instance.Spec.Content,
			TTL:     instance.Spec.TTL,
			Proxied: instance.Spec.Proxied,
		})
		if err != nil {
			instance.Status.Phase = "Failed"
			instance.Status.Message = err.Error()
			err := r.Status().Update(ctx, instance)
			if err != nil {
				log.Error(err, "Failed to update DNS record status")
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, err
		}
		if !reflect.DeepEqual(instance.Status, cfv1alpha1.DNSRecordStatus{Phase: "Created", RecordID: existingRecord.ID, Message: ""}) {
			instance.Status.Phase = "Created"
			instance.Status.RecordID = existingRecord.ID
			instance.Status.Message = ""
		}
		err = r.Status().Update(ctx, instance)
		if err != nil {
			log.Error(err, "Failed to update DNS record status")
			return ctrl.Result{}, err
		}
		log.Info("DNS record updated in cloudflare", "name", existingRecord.Name, "id", existingRecord.ID)
		return ctrl.Result{RequeueAfter: instance.Spec.Interval.Duration}, nil
	}

	if !reflect.DeepEqual(instance.Status, cfv1alpha1.DNSRecordStatus{Phase: "Created", RecordID: existingRecord.ID, Message: ""}) {
		instance.Status.Phase = "Created"
		instance.Status.RecordID = existingRecord.ID
		instance.Status.Message = ""
		err := r.Status().Update(ctx, instance)
		if err != nil {
			log.Error(err, "Failed to update DNS record status")
			return ctrl.Result{}, err
		}
	}

	isDNSRecordMarkedToBeDeleted := instance.GetDeletionTimestamp() != nil
	if isDNSRecordMarkedToBeDeleted {
		if controllerutil.ContainsFinalizer(instance, cfv1alpha1.CfFinalizer) {
			if err := r.finalizeDNSRecord(ctx, dnsRecordZoneId, log, instance); err != nil {
				return ctrl.Result{}, err
			}
		}

		controllerutil.RemoveFinalizer(instance, cfv1alpha1.CfFinalizer)
		err := r.Update(ctx, instance)
		if err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(instance, cfv1alpha1.CfFinalizer) {
		controllerutil.AddFinalizer(instance, cfv1alpha1.CfFinalizer)
		err := r.Update(ctx, instance)
		if err != nil {
			log.Error(err, "Failed to update DNS record finalizer")
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{RequeueAfter: instance.Spec.Interval.Duration}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *DNSRecordReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&cfv1alpha1.DNSRecord{}).
		Complete(r)
}

// finalizeDNSRecord deletes the DNS record from cloudflare
func (r *DNSRecordReconciler) finalizeDNSRecord(ctx context.Context, dnsRecordZoneId string, log logr.Logger, d *cfv1alpha1.DNSRecord) error {
	err := r.Cf.DeleteDNSRecord(ctx, dnsRecordZoneId, d.Status.RecordID)
	if err != nil {
		log.Error(err, "Failed to delete DNS record in cloudflare")
		return err
	}
	return nil
}
