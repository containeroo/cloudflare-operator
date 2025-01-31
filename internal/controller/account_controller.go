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
	"time"

	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

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
	log := log.FromContext(ctx)

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

	secret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: account.Spec.ApiToken.SecretRef.Namespace, Name: account.Spec.ApiToken.SecretRef.Name}, secret); err != nil {
		if err := r.setStatusCondition(ctx, account, "Failed to get secret", "Failed", metav1.ConditionFalse); err != nil {
			log.Error(err, "Failed to update Account status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: time.Second * 30}, err
	}

	cfApiToken := string(secret.Data["apiToken"])
	if cfApiToken == "" {
		if err := r.setStatusCondition(ctx, account, "Secret has no 'apiToken' key", "Failed", metav1.ConditionFalse); err != nil {
			log.Error(err, "Failed to update Account status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: time.Second * 30}, nil
	}

	if r.Cf.APIToken != cfApiToken {
		cf, err := cloudflare.NewWithAPIToken(cfApiToken)
		if err != nil {
			if err := r.setStatusCondition(ctx, account, err.Error(), "Failed", metav1.ConditionFalse); err != nil {
				log.Error(err, "Failed to update Account status")
				return ctrl.Result{}, err
			}
			return ctrl.Result{RequeueAfter: time.Second * 30}, err
		}
		*r.Cf = *cf
	}

	if err := r.setStatusCondition(ctx, account, "Account is ready", "Ready", metav1.ConditionTrue); err != nil {
		log.Error(err, "Failed to update Account status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: account.Spec.Interval.Duration}, nil
}

// setStatusCondition sets the status condition and updates the failure counter
func (r *AccountReconciler) setStatusCondition(ctx context.Context, account *cloudflareoperatoriov1.Account, message, reason string, status metav1.ConditionStatus) error {
	var gaugeValue float64
	switch status {
	case metav1.ConditionTrue:
		gaugeValue = 0
	case metav1.ConditionFalse:
		gaugeValue = 1
	}

	metrics.AccountFailureCounter.WithLabelValues(account.Name).Set(gaugeValue)

	apimeta.SetStatusCondition(&account.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: account.Generation,
	})
	if err := r.Status().Update(ctx, account); err != nil {
		return err
	}

	return nil
}
