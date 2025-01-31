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
	"fmt"
	"reflect"
	"time"

	"github.com/go-logr/logr"
	"golang.org/x/net/publicsuffix"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/cloudflare/cloudflare-go"
	cloudflareoperatoriov1 "github.com/containeroo/cloudflare-operator/api/v1"
	"github.com/containeroo/cloudflare-operator/internal/common"
	"github.com/containeroo/cloudflare-operator/internal/metrics"
)

// DNSRecordReconciler reconciles a DNSRecord object
type DNSRecordReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Cf     *cloudflare.API
}

// SetupWithManager sets up the controller with the Manager.
func (r *DNSRecordReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&cloudflareoperatoriov1.DNSRecord{}).
		Complete(r)
}

// +kubebuilder:rbac:groups=cloudflare-operator.io,resources=dnsrecords,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cloudflare-operator.io,resources=dnsrecords/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=cloudflare-operator.io,resources=dnsrecords/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *DNSRecordReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	dnsrecord := &cloudflareoperatoriov1.DNSRecord{}
	if err := r.Get(ctx, req.NamespacedName, dnsrecord); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if r.Cf.APIToken == "" {
		if err := r.setStatusCondition(ctx, dnsrecord, "Cloudflare account is not yet ready", "NotReady", metav1.ConditionFalse); err != nil {
			log.Error(err, "Failed to update DNSRecord status")
			return ctrl.Result{}, err
		}

		return ctrl.Result{RequeueAfter: time.Second * 5}, nil
	}

	zoneName, _ := publicsuffix.EffectiveTLDPlusOne(dnsrecord.Spec.Name)

	zones := &cloudflareoperatoriov1.ZoneList{}
	if err := r.List(ctx, zones); err != nil {
		if err := r.setStatusCondition(ctx, dnsrecord, "Failed to fetch Zones", "Failed", metav1.ConditionFalse); err != nil {
			log.Error(err, "Failed to update DNSRecord status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: time.Second * 30}, err
	}

	zone := cloudflareoperatoriov1.Zone{}
	for _, zoneItem := range zones.Items {
		if zoneItem.Spec.Name != zoneName {
			continue
		}

		zone = zoneItem
		break
	}

	if zone.Spec.Name == "" {
		if err := r.setStatusCondition(ctx, dnsrecord, "Zone not found", "Failed", metav1.ConditionFalse); err != nil {
			log.Error(err, "Failed to update DNSRecord status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	if condition := apimeta.FindStatusCondition(zone.Status.Conditions, "Ready"); condition == nil || condition.Status != "True" {
		if err := r.setStatusCondition(ctx, dnsrecord, "Zone is not yet ready", "NotReady", metav1.ConditionFalse); err != nil {
			log.Error(err, "Failed to update DNSRecord status")
			return ctrl.Result{}, err
		}

		return ctrl.Result{RequeueAfter: time.Second * 5}, nil
	}

	if !controllerutil.ContainsFinalizer(dnsrecord, common.CloudflareOperatorFinalizer) {
		controllerutil.AddFinalizer(dnsrecord, common.CloudflareOperatorFinalizer)
		if err := r.Update(ctx, dnsrecord); err != nil {
			log.Error(err, "Failed to update DNSRecord finalizer")
			return ctrl.Result{}, err
		}
	}

	if !dnsrecord.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(dnsrecord, common.CloudflareOperatorFinalizer) {
			if err := r.finalizeDNSRecord(ctx, zone.Status.ID, log, dnsrecord); err != nil && err.Error() != "Record does not exist. (81044)" {
				if err := r.setStatusCondition(ctx, dnsrecord, err.Error(), "Failed", metav1.ConditionFalse); err != nil {
					log.Error(err, "Failed to update DNSRecord status")
					return ctrl.Result{}, err
				}
				return ctrl.Result{RequeueAfter: time.Second * 30}, err
			}
			metrics.DnsRecordFailureCounter.DeleteLabelValues(dnsrecord.Namespace, dnsrecord.Name, dnsrecord.Spec.Name)
		}

		controllerutil.RemoveFinalizer(dnsrecord, common.CloudflareOperatorFinalizer)
		if err := r.Update(ctx, dnsrecord); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	if !zone.Spec.Prune {
		existingDNSRecordIfPruneFalse, _, _ := r.Cf.ListDNSRecords(ctx, cloudflare.ZoneIdentifier(zone.Status.ID), cloudflare.ListDNSRecordsParams{Name: dnsrecord.Spec.Name, Type: dnsrecord.Spec.Type, Content: dnsrecord.Spec.Content})

		if len(existingDNSRecordIfPruneFalse) == 1 {
			apimeta.SetStatusCondition(&dnsrecord.Status.Conditions, metav1.Condition{
				Type:               "Ready",
				Status:             "True",
				Reason:             "Ready",
				Message:            "DNS record synced",
				ObservedGeneration: dnsrecord.Generation,
			})
			dnsrecord.Status.RecordID = existingDNSRecordIfPruneFalse[0].ID
			if err := r.Status().Update(ctx, dnsrecord); err != nil {
				log.Error(err, "Failed to update DNSRecord status")
				return ctrl.Result{}, err
			}
			return ctrl.Result{RequeueAfter: dnsrecord.Spec.Interval.Duration}, nil
		} else if len(existingDNSRecordIfPruneFalse) > 1 {
			fmt.Println("something went very wrong")
		} else if len(existingDNSRecordIfPruneFalse) == 0 {
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
				if err := r.setStatusCondition(ctx, dnsrecord, err.Error(), "Failed", metav1.ConditionFalse); err != nil {
					log.Error(err, "Failed to update DNSRecord status")
					return ctrl.Result{}, err
				}
				return ctrl.Result{RequeueAfter: time.Second * 30}, err
			}
			apimeta.SetStatusCondition(&dnsrecord.Status.Conditions, metav1.Condition{
				Type:               "Ready",
				Status:             "True",
				Reason:             "Ready",
				Message:            "DNS record synced",
				ObservedGeneration: dnsrecord.Generation,
			})
			dnsrecord.Status.RecordID = newDNSRecord.ID
			if err := r.Status().Update(ctx, dnsrecord); err != nil {
				log.Error(err, "Failed to update DNSRecord status")
				return ctrl.Result{}, err
			}
			return ctrl.Result{RequeueAfter: dnsrecord.Spec.Interval.Duration}, nil
		}
	}

	var existingRecord cloudflare.DNSRecord

	if dnsrecord.Status.RecordID != "" {
		var err error
		existingRecord, err = r.Cf.GetDNSRecord(ctx, cloudflare.ZoneIdentifier(zone.Status.ID), dnsrecord.Status.RecordID)
		if err != nil && err.Error() != "Record does not exist. (81044)" {
			log.Error(err, "Failed to get DNS record from Cloudflare")
			return ctrl.Result{RequeueAfter: time.Second * 30}, err
		}
	}

	if (dnsrecord.Spec.Type == "A" || dnsrecord.Spec.Type == "AAAA") && dnsrecord.Spec.IPRef.Name != "" {
		ip := &cloudflareoperatoriov1.IP{}
		if err := r.Get(ctx, client.ObjectKey{Name: dnsrecord.Spec.IPRef.Name}, ip); err != nil {
			if err := r.setStatusCondition(ctx, dnsrecord, "IP object not found", "Failed", metav1.ConditionFalse); err != nil {
				log.Error(err, "Failed to update DNSRecord status")
				return ctrl.Result{}, err
			}
			return ctrl.Result{RequeueAfter: time.Second * 30}, err
		}
		if ip.Spec.Address != dnsrecord.Spec.Content {
			dnsrecord.Spec.Content = ip.Spec.Address
			if err := r.Update(ctx, dnsrecord); err != nil {
				log.Error(err, "Failed to update DNSRecord resource")
				return ctrl.Result{}, err
			}
		}
	}

	if *dnsrecord.Spec.Proxied && dnsrecord.Spec.TTL != 1 {
		if err := r.setStatusCondition(ctx, dnsrecord, "TTL must be 1 when proxied", "Failed", metav1.ConditionFalse); err != nil {
			log.Error(err, "Failed to update DNSRecord status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
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
			if err := r.setStatusCondition(ctx, dnsrecord, err.Error(), "Failed", metav1.ConditionFalse); err != nil {
				log.Error(err, "Failed to update DNSRecord status")
				return ctrl.Result{}, err
			}
			return ctrl.Result{RequeueAfter: time.Second * 30}, err
		}
		dnsrecord.Status.RecordID = newDNSRecord.ID
		if err := r.setStatusCondition(ctx, dnsrecord, "DNS record synced", "Ready", metav1.ConditionTrue); err != nil {
			log.Error(err, "Failed to update DNSRecord status")
			return ctrl.Result{}, err
		}

		return ctrl.Result{RequeueAfter: dnsrecord.Spec.Interval.Duration}, nil
	}

	if !compareDNSRecord(dnsrecord.Spec, existingRecord) {
		if _, err := r.Cf.UpdateDNSRecord(ctx, cloudflare.ZoneIdentifier(zone.Status.ID), cloudflare.UpdateDNSRecordParams{
			ID:       existingRecord.ID,
			Name:     dnsrecord.Spec.Name,
			Type:     dnsrecord.Spec.Type,
			Content:  dnsrecord.Spec.Content,
			TTL:      dnsrecord.Spec.TTL,
			Proxied:  dnsrecord.Spec.Proxied,
			Priority: dnsrecord.Spec.Priority,
			Data:     dnsrecord.Spec.Data,
		}); err != nil {
			if err := r.setStatusCondition(ctx, dnsrecord, err.Error(), "Failed", metav1.ConditionFalse); err != nil {
				log.Error(err, "Failed to update DNSRecord status")
				return ctrl.Result{}, err
			}
			return ctrl.Result{RequeueAfter: time.Second * 30}, err
		}
		dnsrecord.Status.RecordID = existingRecord.ID
		if err := r.setStatusCondition(ctx, dnsrecord, "DNS record synced", "Ready", metav1.ConditionTrue); err != nil {
			log.Error(err, "Failed to update DNSRecord status")
			return ctrl.Result{}, err
		}
		log.Info("DNS record updated in cloudflare", "name", existingRecord.Name, "id", existingRecord.ID)
		return ctrl.Result{RequeueAfter: dnsrecord.Spec.Interval.Duration}, nil
	}

	if err := r.setStatusCondition(ctx, dnsrecord, "DNS record synced", "Ready", metav1.ConditionTrue); err != nil {
		log.Error(err, "Failed to update DNSRecord status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: dnsrecord.Spec.Interval.Duration}, nil
}

// finalizeDNSRecord deletes the DNS record from cloudflare
func (r *DNSRecordReconciler) finalizeDNSRecord(ctx context.Context, dnsRecordZoneId string, log logr.Logger, d *cloudflareoperatoriov1.DNSRecord) error {
	if err := r.Cf.DeleteDNSRecord(ctx, cloudflare.ZoneIdentifier(dnsRecordZoneId), d.Status.RecordID); err != nil {
		log.Error(err, "Failed to delete DNS record in Cloudflare. Record may still exist in Cloudflare")
		return err
	}

	return nil
}

// setStatusCondition sets the status condition and updates the failure counter
func (r *DNSRecordReconciler) setStatusCondition(ctx context.Context, dnsrecord *cloudflareoperatoriov1.DNSRecord, message, reason string, status metav1.ConditionStatus) error {
	var gaugeValue float64
	switch status {
	case metav1.ConditionTrue:
		gaugeValue = 0
	case metav1.ConditionFalse:
		gaugeValue = 1
	}

	metrics.DnsRecordFailureCounter.WithLabelValues(dnsrecord.Namespace, dnsrecord.Name, dnsrecord.Spec.Name).Set(gaugeValue)

	apimeta.SetStatusCondition(&dnsrecord.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: dnsrecord.Generation,
	})
	if err := r.Status().Update(ctx, dnsrecord); err != nil {
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

// compareDNSRecord compares the DNS record to the DNSRecord object
func compareDNSRecord(dnsRecordSpec cloudflareoperatoriov1.DNSRecordSpec, existingRecord cloudflare.DNSRecord) bool {
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
