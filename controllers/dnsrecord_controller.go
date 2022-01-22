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
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"os"
	"reflect"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

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

	// Get the zone id for the specific DNSRecord
	zoneID, err := r.Cf.ZoneIDByName(os.Getenv("CLOUDFLARE_ZONE_NAME"))
	// Check if the DNS record already exists
	existingRecords, err := r.Cf.DNSRecords(ctx, zoneID, cloudflare.DNSRecord{Name: instance.Spec.Name})
	if err != nil {
		log.Error(err, "Failed to get DNS record from cloudflare")
		return ctrl.Result{}, err
	}
	// Record doesn't exist, create it
	if existingRecords == nil {
		resp, err := r.Cf.CreateDNSRecord(ctx, zoneID, cloudflare.DNSRecord{
			Name:    instance.Spec.Name,
			Type:    instance.Spec.Type,
			Content: instance.Spec.Content,
			TTL:     instance.Spec.TTL,
			Proxied: instance.Spec.Proxied,
		})
		if err != nil {
			log.Error(err, "Failed to create DNS record in cloudflare")
			instance.Status.Phase = "Failed"
			instance.Status.Message = err.Error()
			err = r.Status().Update(ctx, instance)
			if err != nil {
				log.Error(err, "Failed to update DNS record status")
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, err
		}
		log.Info("DNS record created in cloudflare", "name", resp.Result.Name, "id", resp.Result.ID)
		instance.Status.Phase = "Created"
		instance.Status.RecordID = resp.Result.ID
		instance.Status.Message = ""
		err = r.Status().Update(ctx, instance)
		if err != nil {
			log.Error(err, "Failed to update DNS record status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
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
		err := r.Cf.UpdateDNSRecord(ctx, zoneID, existingRecord.ID, cloudflare.DNSRecord{
			Name:    instance.Spec.Name,
			Type:    instance.Spec.Type,
			Content: instance.Spec.Content,
			TTL:     instance.Spec.TTL,
			Proxied: instance.Spec.Proxied,
		})
		if err != nil {
			log.Error(err, "Failed to update DNS record in cloudflare")
			instance.Status.Phase = "Failed"
			instance.Status.Message = err.Error()
			err = r.Status().Update(ctx, instance)
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
		return ctrl.Result{}, nil
	}
	log.Info("DNS record is up to date", "name", existingRecord.Name, "id", existingRecord.ID)

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

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *DNSRecordReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&cfv1alpha1.DNSRecord{}).
		Complete(r)
}
