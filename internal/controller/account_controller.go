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
	"reflect"
	"strings"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/cloudflare/cloudflare-go"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	cloudflareoperatoriov1 "github.com/containeroo/cloudflare-operator/api/v1"
	"github.com/containeroo/cloudflare-operator/internal/common"
	"github.com/containeroo/cloudflare-operator/internal/metrics"
)

// AccountReconciler reconciles an Account object
type AccountReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Cf     *cloudflare.API
}

// SetupWithManager sets up the controller with the Manager.
func (r *AccountReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&cloudflareoperatoriov1.Account{}).
		Complete(r)
}

// +kubebuilder:rbac:groups=cloudflare-operator.io,resources=accounts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cloudflare-operator.io,resources=accounts/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=cloudflare-operator.io,resources=accounts/finalizers,verbs=update
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *AccountReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	account := &cloudflareoperatoriov1.Account{}
	if err := r.Get(ctx, req.NamespacedName, account); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !controllerutil.ContainsFinalizer(account, common.CloudflareOperatorFinalizer) {
		controllerutil.AddFinalizer(account, common.CloudflareOperatorFinalizer)
		if err := r.Update(ctx, account); err != nil {
			log.Error(err, "Failed to update Account finalizer")
			return ctrl.Result{}, err
		}
	}

	if !account.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(account, common.CloudflareOperatorFinalizer) {
			metrics.AccountFailureCounter.DeleteLabelValues(account.Name)
		}

		controllerutil.RemoveFinalizer(account, common.CloudflareOperatorFinalizer)
		if err := r.Update(ctx, account); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	metrics.AccountFailureCounter.WithLabelValues(account.Name).Set(0)

	secret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: account.Spec.ApiToken.SecretRef.Namespace, Name: account.Spec.ApiToken.SecretRef.Name}, secret); err != nil {
		if err := r.markFailed(account, ctx, "Failed to get secret"); err != nil {
			log.Error(err, "Failed to update Account status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: time.Second * 30}, err
	}

	cfApiToken := string(secret.Data["apiToken"])
	if cfApiToken == "" {
		if err := r.markFailed(account, ctx, "Secret has no 'apiToken' key"); err != nil {
			log.Error(err, "Failed to update Account status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: time.Second * 30}, nil
	}

	if r.Cf.APIToken != cfApiToken {
		cf, err := cloudflare.NewWithAPIToken(cfApiToken)
		if err != nil {
			if err := r.markFailed(account, ctx, err.Error()); err != nil {
				log.Error(err, "Failed to update Account status")
				return ctrl.Result{}, err
			}
			return ctrl.Result{RequeueAfter: time.Second * 30}, err
		}
		*r.Cf = *cf
	}

	cfZones, err := r.Cf.ListZones(ctx)
	if err != nil {
		if err := r.markFailed(account, ctx, err.Error()); err != nil {
			log.Error(err, "Failed to update Account status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: time.Second * 30}, err
	}

	userDefinedManagedZoneMap := make(map[string]struct{})
	if len(account.Spec.ManagedZones) != 0 {
		for _, managedZone := range account.Spec.ManagedZones {
			userDefinedManagedZoneMap[strings.ToLower(managedZone)] = struct{}{}
		}
	}

	operatorManagedZones := []cloudflareoperatoriov1.AccountStatusZones{}
	for _, cfZone := range cfZones {
		_, found := userDefinedManagedZoneMap[strings.ToLower(cfZone.Name)]
		if len(userDefinedManagedZoneMap) == 0 || found {
			operatorManagedZones = append(operatorManagedZones, cloudflareoperatoriov1.AccountStatusZones{Name: cfZone.Name, ID: cfZone.ID})
		}
	}

	zones := &cloudflareoperatoriov1.ZoneList{}
	if err := r.List(ctx, zones); err != nil {
		if err := r.markFailed(account, ctx, "Failed to list zones"); err != nil {
			log.Error(err, "Failed to update Account status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: time.Second * 30}, err
	}

	zoneMap := make(map[string]struct{})
	for _, zone := range zones.Items {
		zoneMap[zone.Spec.ID] = struct{}{}
	}

	operatorManagedZoneMap := make(map[string]struct{})
	for _, operatorManagedZone := range operatorManagedZones {
		operatorManagedZoneMap[operatorManagedZone.ID] = struct{}{}
		if _, found := zoneMap[operatorManagedZone.ID]; !found {
			z := &cloudflareoperatoriov1.Zone{
				ObjectMeta: metav1.ObjectMeta{
					Name: strings.ReplaceAll(operatorManagedZone.Name, ".", "-"),
					Labels: map[string]string{
						"app.kubernetes.io/managed-by": "cloudflare-operator",
					},
				},
				Spec: cloudflareoperatoriov1.ZoneSpec{
					Name:     operatorManagedZone.Name,
					ID:       operatorManagedZone.ID,
					Interval: account.Spec.Interval,
				},
			}

			if err := controllerutil.SetControllerReference(account, z, r.Scheme); err != nil {
				log.Error(err, "Failed to set controller reference")
				return ctrl.Result{}, err
			}

			if err := r.Create(ctx, z); err != nil {
				log.Error(err, "Failed to create Zone resource", "Zone.Name", operatorManagedZone.Name, "Zone.ID", operatorManagedZone.ID)
				continue
			}
			log.Info("Created Zone resource", "Zone.Name", operatorManagedZone.Name, "Zone.ID", operatorManagedZone.ID)
		}
	}

	if !reflect.DeepEqual(account.Status.Zones, operatorManagedZones) {
		account.Status.Zones = operatorManagedZones
		if err := r.Status().Update(ctx, account); err != nil {
			log.Error(err, "Failed to update Account status")
			return ctrl.Result{}, err
		}
	}

	for _, z := range zones.Items {
		if _, found := operatorManagedZoneMap[z.Spec.ID]; !found {
			if err := r.Delete(ctx, &z); err != nil {
				log.Error(err, "Failed to delete Zone resource", "Zone.Name", z.Name)
				return ctrl.Result{}, err
			}
			log.Info("Deleted Zone resource", "Zone.Name", z.Spec.Name, "Zone.ID", z.Spec.ID)
		}
	}

	apimeta.SetStatusCondition(&account.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             "True",
		Reason:             "Ready",
		Message:            "Account is ready",
		ObservedGeneration: account.Generation,
	})
	if err := r.Status().Update(ctx, account); err != nil {
		log.Error(err, "Failed to update Account status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: account.Spec.Interval.Duration}, nil
}

// markFailed marks the reconciled object as failed
func (r *AccountReconciler) markFailed(account *cloudflareoperatoriov1.Account, ctx context.Context, message string) error {
	metrics.AccountFailureCounter.WithLabelValues(account.Name).Set(1)
	apimeta.SetStatusCondition(&account.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             "False",
		Reason:             "Failed",
		Message:            message,
		ObservedGeneration: account.Generation,
	})
	if err := r.Status().Update(ctx, account); err != nil {
		return err
	}

	return nil
}
