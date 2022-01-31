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
	"reflect"
	"strconv"
	"strings"
	"time"

	cfv1alpha1 "github.com/containeroo/cloudflare-operator/api/v1alpha1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
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

	// Fetch all DNSRecord instances in the same namespace
	dnsRecords := &cfv1alpha1.DNSRecordList{}
	err = r.List(ctx, dnsRecords, client.InNamespace(instance.Namespace))
	if err != nil {
		log.Error(err, "unable to fetch DNSRecord")
		return ctrl.Result{}, err
	}

	// Check if Ingress has ignore annotation and if so, delete dependent DNSRecords
	if instance.Annotations["cf.containeroo.ch/ignore"] == "true" {
		for _, dnsRecord := range dnsRecords.Items {
			for _, ownerRef := range dnsRecord.OwnerReferences {
				if ownerRef.UID != instance.UID {
					continue
				}
				err := r.Delete(ctx, &dnsRecord)
				if err != nil {
					log.Error(err, "unable to delete DNSRecord")
					return ctrl.Result{}, err
				}
				log.Info("Deleted DNSRecord, because it was owned by an Ingress that is being ignored", "DNSRecord", dnsRecord.Name)
				return ctrl.Result{}, err
			}
		}
		log.Info("Ingress has ignore annotation, skipping reconciliation", "ingress", instance.Name)
		return ctrl.Result{}, nil
	}

	trueVar := true

	// Create a dnsRecordSpec based on annotations
	dnsRecordSpec := cfv1alpha1.DNSRecordSpec{}

	if content, ok := instance.Annotations["cf.containeroo.ch/content"]; ok {
		dnsRecordSpec.Content = content
	}

	if ipRef, ok := instance.Annotations["cf.containeroo.ch/ip-ref"]; ok {
		dnsRecordSpec.IpRef.Name = ipRef
	}

	if dnsRecordSpec.Content == "" && dnsRecordSpec.IpRef.Name == "" {
		log.Info("Ingress has no content or ip-ref annotation, skipping reconciliation", "ingress", instance.Name)
		return ctrl.Result{}, nil
	}

	if proxied, ok := instance.Annotations["cf.containeroo.ch/proxied"]; ok {
		switch proxied {
		case "true":
			dnsRecordSpec.Proxied = &trueVar
		case "false":
			falseVar := false
			dnsRecordSpec.Proxied = &falseVar
		}
	}
	if dnsRecordSpec.Proxied == nil {
		dnsRecordSpec.Proxied = &trueVar
	}

	if ttl, ok := instance.Annotations["cf.containeroo.ch/ttl"]; ok {
		ttlInt, _ := strconv.Atoi(ttl)
		if *dnsRecordSpec.Proxied && ttlInt != 1 {
			log.Info("DNSRecord is proxied and ttl is not 1, skipping reconciliation", "ingress", instance.Name)
			return ctrl.Result{}, nil
		}
		dnsRecordSpec.TTL = ttlInt
	}
	if dnsRecordSpec.TTL == 0 {
		dnsRecordSpec.TTL = 1
	}

	if type_, ok := instance.Annotations["cf.containeroo.ch/type"]; ok {
		dnsRecordSpec.Type = type_
	}
	if dnsRecordSpec.Type == "" {
		dnsRecordSpec.Type = "A"
	}

	if interval, ok := instance.Annotations["cf.containeroo.ch/interval"]; ok {
		intervalDuration, _ := time.ParseDuration(interval)
		dnsRecordSpec.Interval = metav1.Duration{Duration: intervalDuration}
	}
	if dnsRecordSpec.Interval.Duration == 0 {
		dnsRecordSpec.Interval.Duration = time.Minute * 5
	}

	// Loop through all spec.rules and check if a dns record already exists for the ingress
rules:
	for _, rule := range instance.Spec.Rules {
		for _, dnsRecord := range dnsRecords.Items {
			if dnsRecord.Spec.Name == rule.Host {
				continue rules
			}
		}

		dnsRecordSpec.Name = rule.Host

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
			Spec: dnsRecordSpec,
		}

		log.Info("Creating DNSRecord", "name", dnsRecord.Name)
		err = r.Create(ctx, dnsRecord)
		if err != nil {
			log.Error(err, "unable to create DNSRecord")
			return ctrl.Result{}, err
		}
	}

	// Update DNSRecord if dnsRecordSpec has changed
	for _, rule := range instance.Spec.Rules {
		for _, dnsRecord := range dnsRecords.Items {
			if dnsRecord.Spec.Name != rule.Host {
				continue
			}
			dnsRecordSpec.Name = rule.Host
			if reflect.DeepEqual(dnsRecord.Spec, dnsRecordSpec) {
				continue
			}
			// Update the DNSRecord with the new dnsRecordSpec
			log.Info("Updating DNSRecord", "name", dnsRecord.Name)
			dnsRecord.Spec = dnsRecordSpec
			err := r.Update(ctx, &dnsRecord)
			if err != nil {
				log.Error(err, "unable to update DNSRecord")
				return ctrl.Result{}, err
			}
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *IngressReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&networkingv1.Ingress{}).
		Complete(r)
}
