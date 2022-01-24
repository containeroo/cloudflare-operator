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
	cfv1alpha1 "github.com/containeroo/cloudflare-operator/api/v1alpha1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	"strings"
)

// IngressReconciler reconciles a Ingress object
type IngressReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Ingress object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.10.0/pkg/reconcile
func (r *IngressReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)

	// Fetch the Ingress instance
	instance := &networkingv1.Ingress{}
	err := r.Get(ctx, req.NamespacedName, instance)
	if err != nil {
		log.Error(err, "unable to fetch Ingress")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Check if the Ingress has a skip annotation and if so, return early
	if instance.Annotations["cf.containeroo.ch/skip"] == "true" {
		log.Info("Ingress has skip annotation, skipping reconciliation", "ingress", instance.Name)
		return ctrl.Result{}, nil
	}

	// Fetch all DNSRecord instances in the same namespace
	dnsRecords := &cfv1alpha1.DNSRecordList{}
	err = r.List(ctx, dnsRecords, client.InNamespace(instance.Namespace))
	if err != nil {
		log.Error(err, "unable to fetch DNSRecord")
		return ctrl.Result{}, err
	}

	// Loop through all spec.rules and check if a dns record already exists for the ingress
rules:
	for _, rule := range instance.Spec.Rules {
		// TODO: Get the DNSRecordSpec from annotations on the Ingress if specified
		for _, dnsRecord := range dnsRecords.Items {
			if dnsRecord.Spec.Name == rule.Host {
				log.Info("DNS record already exists for ingress", "name", dnsRecord.Spec.Name)
				continue rules
			}
		}

		trueVar := true
		dnsRecord := &cfv1alpha1.DNSRecord{
			ObjectMeta: metav1.ObjectMeta{
				Name:      strings.ReplaceAll(rule.Host, ".", "-"),
				Namespace: instance.Namespace,
				Labels: map[string]string{
					"app.kubernetes.io/managed-by": "cloudflare-operator",
					"app.kubernetes.io/created-by": "cloudflare-operator",
				},
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion:         "networking.k8s.io/v1",
						Kind:               "Ingress",
						Name:               instance.Name,
						UID:                instance.UID,
						Controller:         &trueVar,
						BlockOwnerDeletion: &trueVar,
					},
				},
			},
			Spec: cfv1alpha1.DNSRecordSpec{
				Name: rule.Host,
				Type: "A",
				// TODO: Populate all the other fields from the Spec
			},
		}

		// TODO: Actually create the DNSRecord
		log.Info("Creating DNSRecord", "name", dnsRecord.Name)
		// err = r.Create(ctx, dnsRecord)
		// if err != nil {
		// 	log.Error(err, "unable to create DNSRecord")
		// 	return ctrl.Result{}, err
		// }
	}

	// TODO: Update DNSRecord spec to match Ingress annotations
	// TODO: Create a finalizer to remove the DNSRecord when the Ingress is deleted

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *IngressReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&networkingv1.Ingress{}).
		Complete(r)
}
