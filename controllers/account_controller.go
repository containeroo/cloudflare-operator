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

	cfv1alpha1 "github.com/containeroo/cloudflare-operator/api/v1alpha1"
)

// AccountReconciler reconciles an Account object
type AccountReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Cf     *cloudflare.API
}

//+kubebuilder:rbac:groups=cf.containeroo.ch,resources=accounts,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=cf.containeroo.ch,resources=accounts/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=cf.containeroo.ch,resources=accounts/finalizers,verbs=update
//+kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Account object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.10.0/pkg/reconcile
func (r *AccountReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)

	// Fetch the Account instance
	instance := &cfv1alpha1.Account{}
	err := r.Get(ctx, req.NamespacedName, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			log.Info("Account resource not found. Ignoring since object must be deleted.")
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get Account resource")
		return ctrl.Result{}, err
	}

	// Fetch the secret
	secret := &v1.Secret{}
	err = r.Get(ctx, client.ObjectKey{Namespace: instance.Spec.GlobalApiKey.SecretRef.Namespace, Name: instance.Spec.GlobalApiKey.SecretRef.Name}, secret)
	if err != nil {
		instance.Status.Phase = "Failed"
		instance.Status.Message = "Failed to get secret"
		err := r.Status().Update(ctx, instance)
		if err != nil {
			log.Error(err, "Failed to update Account status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, err
	}

	apiKey := string(secret.Data["apiKey"])
	if apiKey == "" {
		instance.Status.Phase = "Failed"
		instance.Status.Message = "Secret does not contain apiKey"
		err := r.Status().Update(ctx, instance)
		if err != nil {
			log.Error(err, "Failed to update Account status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, err
	}

	cf, err := cloudflare.New(apiKey, instance.Spec.Email)
	if err != nil {
		instance.Status.Phase = "Failed"
		instance.Status.Message = err.Error()
		err := r.Status().Update(ctx, instance)
		if err != nil {
			log.Error(err, "Failed to update Account status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, err
	}
	*r.Cf = *cf

	zones, err := r.Cf.ListZones(ctx)
	if err != nil {
		log.Error(err, "Failed to create Cloudflare client. Retrying in 30 seconds")
		instance.Status.Phase = "Failed"
		instance.Status.Message = err.Error()
		err := r.Status().Update(ctx, instance)
		if err != nil {
			log.Error(err, "Failed to update Account status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 30 * time.Second}, err
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

	// Fetch all Zone objects
	zonesList := &cfv1alpha1.ZoneList{}
	err = r.List(ctx, zonesList, client.InNamespace(instance.Namespace))
	if err != nil {
		instance.Status.Phase = "Failed"
		instance.Status.Message = "Failed to list zones"
		err := r.Status().Update(ctx, instance)
		if err != nil {
			log.Error(err, "Failed to update Account status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, err
	}

	// Check if all zones are present
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
			z := &cfv1alpha1.Zone{
				ObjectMeta: metav1.ObjectMeta{
					Name: strings.ReplaceAll(zone.Name, ".", "-"),
					Labels: map[string]string{
						"app.kubernetes.io/managed-by": "cloudflare-operator",
						"app.kubernetes.io/created-by": "cloudflare-operator",
					},
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion:         "cf.containeroo.ch/v1alpha1",
							Kind:               "Account",
							Name:               instance.Name,
							UID:                instance.UID,
							Controller:         &trueVar,
							BlockOwnerDeletion: &trueVar,
						},
					},
				},
				Spec: cfv1alpha1.ZoneSpec{
					Name:     zone.Name,
					ID:       zone.ID,
					Interval: instance.Spec.Interval,
				},
				Status: cfv1alpha1.ZoneStatus{
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
			instance.Status.Zones = append(instance.Status.Zones, cfv1alpha1.AccountStatusZones{
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

	// Check if there are any zones that are not present in Cloudflare
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
		For(&cfv1alpha1.Account{}).
		Complete(r)
}
