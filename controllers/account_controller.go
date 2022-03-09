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
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"strings"
	"time"

	"github.com/cloudflare/cloudflare-go"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	cfv1beta1 "github.com/containeroo/cloudflare-operator/api/v1beta1"
)

// AccountReconciler reconciles an Account object
type AccountReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Cf     *cloudflare.API
}

// +kubebuilder:rbac:groups=cf.containeroo.ch,resources=accounts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cf.containeroo.ch,resources=accounts/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=cf.containeroo.ch,resources=accounts/finalizers,verbs=update
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *AccountReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)

	instance := &cfv1beta1.Account{}
	err := r.Get(ctx, req.NamespacedName, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			log.Info("Account resource not found. Ignoring since object must be deleted.")
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get Account resource")
		return ctrl.Result{}, err
	}

	if !controllerutil.ContainsFinalizer(instance, cloudflareOperatorFinalizer) {
		controllerutil.AddFinalizer(instance, cloudflareOperatorFinalizer)
		err := r.Update(ctx, instance)
		if err != nil {
			log.Error(err, "Failed to update Account finalizer")
			return ctrl.Result{}, err
		}
	}

	if instance.GetDeletionTimestamp() != nil {
		if controllerutil.ContainsFinalizer(instance, cloudflareOperatorFinalizer) {
			accountFailureCounter.DeleteLabelValues(instance.Name)
		}

		controllerutil.RemoveFinalizer(instance, cloudflareOperatorFinalizer)
		err := r.Update(ctx, instance)
		if err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	accountFailureCounter.WithLabelValues(instance.Name).Set(0)

	secret := &v1.Secret{}
	err = r.Get(ctx, client.ObjectKey{Namespace: instance.Spec.GlobalAPIKey.SecretRef.Namespace, Name: instance.Spec.GlobalAPIKey.SecretRef.Name}, secret)
	if err != nil {
		err := r.markFailed(instance, ctx, "Failed to get secret")
		if err != nil {
			log.Error(err, "Failed to update Account status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: time.Second * 30}, err
	}

	apiKey := string(secret.Data["apiKey"])
	if apiKey == "" {
		err := r.markFailed(instance, ctx, "Secret does not contain apiKey")
		if err != nil {
			log.Error(err, "Failed to update Account status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: time.Second * 30}, err
	}

	cf, err := cloudflare.New(apiKey, instance.Spec.Email)
	if err != nil {
		err := r.markFailed(instance, ctx, err.Error())
		if err != nil {
			log.Error(err, "Failed to update Account status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: time.Second * 30}, err
	}
	*r.Cf = *cf

	zones, err := r.Cf.ListZones(ctx)
	if err != nil {
		err := r.markFailed(instance, ctx, err.Error())
		if err != nil {
			log.Error(err, "Failed to update Account status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: time.Second * 30}, err
	}

	var managedZones []cloudflare.Zone
	if len(instance.Spec.ManagedZones) != 0 {
		for _, zone := range zones {
			for _, managedZone := range instance.Spec.ManagedZones {
				if !strings.EqualFold(zone.Name, managedZone) {
					continue
				}
				managedZones = append(managedZones, cloudflare.Zone{Name: zone.Name, ID: zone.ID})
			}
		}
	} else {
		managedZones = zones
	}

	zonesList := &cfv1beta1.ZoneList{}
	err = r.List(ctx, zonesList, client.InNamespace(instance.Namespace))
	if err != nil {
		err := r.markFailed(instance, ctx, "Failed to list zones")
		if err != nil {
			log.Error(err, "Failed to update Account status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: time.Second * 30}, err
	}

	for _, zone := range managedZones {
		found := false
		for _, z := range zonesList.Items {
			if z.Spec.ID == zone.ID {
				found = true
				break
			}
		}
		if !found {
			trueVar := true
			z := &cfv1beta1.Zone{
				ObjectMeta: metav1.ObjectMeta{
					Name: strings.ReplaceAll(zone.Name, ".", "-"),
					Labels: map[string]string{
						"app.kubernetes.io/managed-by": "cloudflare-operator",
						"app.kubernetes.io/created-by": "cloudflare-operator",
					},
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion:         "cf.containeroo.ch/v1beta1",
							Kind:               "Account",
							Name:               instance.Name,
							UID:                instance.UID,
							Controller:         &trueVar,
							BlockOwnerDeletion: &trueVar,
						},
					},
				},
				Spec: cfv1beta1.ZoneSpec{
					Name:     zone.Name,
					ID:       zone.ID,
					Interval: instance.Spec.Interval,
				},
				Status: cfv1beta1.ZoneStatus{
					Phase: "Pending",
				},
			}
			err = r.Create(ctx, z)
			if err != nil {
				log.Error(err, "Failed to create Zone resource", "Zone.Name", zone.Name, "Zone.ID", zone.ID)
				continue
			}
			log.Info("Created Zone resource", "Zone.Name", zone.Name, "Zone.ID", zone.ID)
		}
	}

	if instance.Status.Phase != "Active" || instance.Status.Message != "" {
		instance.Status.Phase = "Active"
		instance.Status.Message = ""
		err := r.Status().Update(ctx, instance)
		if err != nil {
			log.Error(err, "Failed to update Account status")
			return ctrl.Result{}, err
		}
	}

	statusChanged := false
	for _, zone := range managedZones {
		found := false
		for _, z := range instance.Status.Zones {
			if z.ID == zone.ID {
				found = true
				break
			}
		}
		if !found {
			statusChanged = true
			instance.Status.Zones = append(instance.Status.Zones, cfv1beta1.AccountStatusZones{
				ID:   zone.ID,
				Name: zone.Name,
			})
		}
	}
	if statusChanged {
		err := r.Status().Update(ctx, instance)
		if err != nil {
			log.Error(err, "Failed to update Account status")
			return ctrl.Result{}, err
		}
	}

	for _, z := range zonesList.Items {
		found := false
		for _, zone := range managedZones {
			if z.Spec.ID == zone.ID {
				found = true
				break
			}
		}
		if !found {
			err = r.Delete(ctx, &z)
			if err != nil {
				log.Error(err, "Failed to delete Zone resource", "Zone.Name", z.Name)
				return ctrl.Result{}, err
			}
			log.Info("Deleted Zone resource", "Zone.Name", z.Spec.Name, "Zone.ID", z.Spec.ID)
		}
	}

	return ctrl.Result{RequeueAfter: instance.Spec.Interval.Duration}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *AccountReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&cfv1beta1.Account{}).
		Complete(r)
}

// markFailed marks the reconciled object as failed
func (r *AccountReconciler) markFailed(instance *cfv1beta1.Account, ctx context.Context, message string) error {
	accountFailureCounter.WithLabelValues(instance.Name).Set(1)
	instance.Status.Phase = "Failed"
	instance.Status.Message = message
	if err := r.Status().Update(ctx, instance); err != nil {
		return err
	}
	return nil
}
