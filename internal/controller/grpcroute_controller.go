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

// GRPCRouteReconciler reconciles a Gateway API GRPCRoute object
type GRPCRouteReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	RetryInterval            time.Duration
	DefaultReconcileInterval time.Duration
}

// SetupWithManager sets up the controller with the Manager.
func (r *GRPCRouteReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&gatewayv1.GRPCRoute{}, builder.WithPredicates(intpredicates.DNSFromGRPCRoutePredicate{})).
		Owns(&cloudflareoperatoriov1.DNSRecord{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Complete(r)
}

// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=grpcroutes,verbs=get;list;watch
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=grpcroutes/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *GRPCRouteReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	grpcRoute := &gatewayv1.GRPCRoute{}
	if err := r.Get(ctx, req.NamespacedName, grpcRoute); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	return r.reconcileGRPCRoute(ctx, grpcRoute)
}

// reconcileGRPCRoute reconciles the GRPCRoute
func (r *GRPCRouteReconciler) reconcileGRPCRoute(ctx context.Context, grpcRoute *gatewayv1.GRPCRoute) (ctrl.Result, error) {
	hostReconciler := DNSHostReconciler{
		Client:                   r.Client,
		Scheme:                   r.Scheme,
		RetryInterval:            r.RetryInterval,
		DefaultReconcileInterval: r.DefaultReconcileInterval,
	}
	return hostReconciler.Reconcile(ctx, grpcRoute, grpcRoute.GetAnnotations(), r.getRouteHosts(grpcRoute))
}

// getRouteHosts returns a map of hosts from the GRPCRoute hostnames
func (r *GRPCRouteReconciler) getRouteHosts(grpcRoute *gatewayv1.GRPCRoute) map[string]struct{} {
	return gatewayHostnamesToHosts(grpcRoute.Spec.Hostnames)
}
