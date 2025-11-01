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
	"errors"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/cloudflare/cloudflare-go"
	"github.com/fluxcd/pkg/runtime/patch"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	cloudflareoperatoriov1 "github.com/containeroo/cloudflare-operator/api/v1"
	intconditions "github.com/containeroo/cloudflare-operator/internal/conditions"
	"github.com/containeroo/cloudflare-operator/internal/metrics"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apierrutil "k8s.io/apimachinery/pkg/util/errors"
)

// AccountReconciler reconciles an Account object
type AccountReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	RetryInterval time.Duration

	AccountManager *AccountManager
}

var errWaitForAccount = errors.New("must wait for account")

// SetupWithManager sets up the controller with the Manager.
func (r *AccountReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&cloudflareoperatoriov1.Account{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Complete(r)
}

// +kubebuilder:rbac:groups=cloudflare-operator.io,resources=accounts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cloudflare-operator.io,resources=accounts/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=cloudflare-operator.io,resources=accounts/finalizers,verbs=update
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *AccountReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, retErr error) {
	account := &cloudflareoperatoriov1.Account{}
	if err := r.Get(ctx, req.NamespacedName, account); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	patchHelper := patch.NewSerialPatcher(account, r.Client)

	defer func() {
		patchOpts := []patch.Option{}

		if errors.Is(retErr, reconcile.TerminalError(nil)) || (retErr == nil && (result.IsZero() || !result.Requeue)) {
			patchOpts = append(patchOpts, patch.WithStatusObservedGeneration{})
		}

		if err := patchHelper.Patch(ctx, account, patchOpts...); err != nil {
			if !account.DeletionTimestamp.IsZero() {
				err = apierrutil.FilterOut(err, func(e error) bool { return apierrors.IsNotFound(e) })
			}
			retErr = apierrutil.Reduce(apierrutil.NewAggregate([]error{retErr, err}))
		}
	}()

	if !account.DeletionTimestamp.IsZero() {
		r.reconcileDelete(account)
		return ctrl.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(account, cloudflareoperatoriov1.CloudflareOperatorFinalizer) {
		controllerutil.AddFinalizer(account, cloudflareoperatoriov1.CloudflareOperatorFinalizer)
		return ctrl.Result{Requeue: true}, nil
	}

	return r.reconcileAccount(ctx, account)
}

// reconcileAccount reconciles the account
func (r *AccountReconciler) reconcileAccount(ctx context.Context, account *cloudflareoperatoriov1.Account) (ctrl.Result, error) {
	secret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{
		Namespace: account.Spec.ApiToken.SecretRef.Namespace,
		Name:      account.Spec.ApiToken.SecretRef.Name,
	}, secret); err != nil {
		intconditions.MarkFalse(account, err)
		if apierrors.IsNotFound(err) {
			return ctrl.Result{RequeueAfter: r.RetryInterval}, nil
		}
		return ctrl.Result{}, err
	}

	cloudflareAPIToken := string(secret.Data["apiToken"])
	if cloudflareAPIToken == "" {
		intconditions.MarkFalse(account, errors.New("secret has no key named \"apiToken\""))
		return ctrl.Result{RequeueAfter: r.RetryInterval}, nil
	}

	cloudflareAPI, err := cloudflare.NewWithAPIToken(cloudflareAPIToken)
	if err != nil {
		intconditions.MarkFalse(account, err)
		return ctrl.Result{}, err
	}

	r.AccountManager.UpsertAccount(account.Name, cloudflareAPI, cloudflareAPIToken, account.Spec.ManagedZones)

	intconditions.MarkTrue(account, "Account is ready")

	return ctrl.Result{RequeueAfter: account.Spec.Interval.Duration}, nil
}

// reconcileDelete reconciles the deletion of the account
func (r *AccountReconciler) reconcileDelete(account *cloudflareoperatoriov1.Account) {
	metrics.AccountFailureCounter.DeleteLabelValues(account.Name)
	controllerutil.RemoveFinalizer(account, cloudflareoperatoriov1.CloudflareOperatorFinalizer)
	r.AccountManager.RemoveAccount(account.Name)
}
