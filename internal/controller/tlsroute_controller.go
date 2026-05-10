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

	cloudflareoperatoriov1 "github.com/containeroo/cloudflare-operator/api/v1"
	intpredicates "github.com/containeroo/cloudflare-operator/internal/predicates"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// TLSRouteReconciler reconciles a Gateway API TLSRoute object
type TLSRouteReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	RetryInterval            time.Duration
	DefaultReconcileInterval time.Duration
}

// SetupWithManager sets up the controller with the Manager.
func (r *TLSRouteReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&gatewayv1.TLSRoute{}, builder.WithPredicates(intpredicates.DNSFromTLSRoutePredicate{})).
		Owns(&cloudflareoperatoriov1.DNSRecord{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Complete(r)
}

// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=tlsroutes,verbs=get;list;watch
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=tlsroutes/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *TLSRouteReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	tlsRoute := &gatewayv1.TLSRoute{}
	if err := r.Get(ctx, req.NamespacedName, tlsRoute); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	return r.reconcileTLSRoute(ctx, tlsRoute)
}

// reconcileTLSRoute reconciles the TLSRoute
func (r *TLSRouteReconciler) reconcileTLSRoute(ctx context.Context, tlsRoute *gatewayv1.TLSRoute) (ctrl.Result, error) {
	hostReconciler := DNSHostReconciler{
		Client:                   r.Client,
		Scheme:                   r.Scheme,
		RetryInterval:            r.RetryInterval,
		DefaultReconcileInterval: r.DefaultReconcileInterval,
	}
	return hostReconciler.Reconcile(ctx, tlsRoute, tlsRoute.GetAnnotations(), r.getRouteHosts(tlsRoute))
}

// getRouteHosts returns a map of hosts from the TLSRoute hostnames
func (r *TLSRouteReconciler) getRouteHosts(tlsRoute *gatewayv1.TLSRoute) map[string]struct{} {
	hosts := make(map[string]struct{})
	for _, hostname := range tlsRoute.Spec.Hostnames {
		if hostname != "" {
			hosts[string(hostname)] = struct{}{}
		}
	}
	return hosts
}
