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
	"reflect"
	"strconv"
	"strings"
	"time"

	cloudflareoperatoriov1 "github.com/containeroo/cloudflare-operator/api/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// IngressReconciler reconciles an Ingress object
type IngressReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// SetupWithManager sets up the controller with the Manager.
func (r *IngressReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(ctx, &cloudflareoperatoriov1.DNSRecord{}, ".metadata.ownerReferences.uid",
		func(o client.Object) []string {
			obj := o.(*cloudflareoperatoriov1.DNSRecord)
			ownerReferences := obj.GetOwnerReferences()
			var ownerReferencesUID string
			for _, ownerReference := range ownerReferences {
				if ownerReference.Kind != "Ingress" {
					continue
				}
				ownerReferencesUID = string(ownerReference.UID)
			}

			return []string{ownerReferencesUID}
		},
	); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&networkingv1.Ingress{}).
		Owns(&cloudflareoperatoriov1.DNSRecord{}).
		Complete(r)
}

// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=get;list;watch
// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *IngressReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	ingress := &networkingv1.Ingress{}
	if err := r.Get(ctx, req.NamespacedName, ingress); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	annotations := ingress.GetAnnotations()
	if annotations["cloudflare-operator.io/content"] == "" && annotations["cloudflare-operator.io/ip-ref"] == "" {
		return ctrl.Result{}, nil
	}

	return r.reconcileIngress(ctx, ingress, annotations)
}

// reconcileIngress reconciles the ingress
func (r *IngressReconciler) reconcileIngress(ctx context.Context, ingress *networkingv1.Ingress, annotations map[string]string) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	dnsRecords := &cloudflareoperatoriov1.DNSRecordList{}
	if err := r.List(ctx, dnsRecords, client.InNamespace(ingress.Namespace), client.MatchingFields{".metadata.ownerReferences.uid": string(ingress.UID)}); err != nil {
		log.Error(err, "Failed to list DNSRecords")
		return ctrl.Result{RequeueAfter: time.Second * 30}, nil
	}

	dnsRecordSpec := r.parseAnnotations(annotations)

	dnsRecordMap := make(map[string]cloudflareoperatoriov1.DNSRecord)
	for _, dnsRecord := range dnsRecords.Items {
		dnsRecordMap[dnsRecord.Spec.Name] = dnsRecord
	}

	ingressRuleMap := make(map[string]struct{})
	for _, rule := range ingress.Spec.Rules {
		if rule.Host == "" {
			continue
		}
		ingressRuleMap[rule.Host] = struct{}{}
		if _, found := dnsRecordMap[rule.Host]; found {
			continue
		}

		dnsRecordSpec.Name = rule.Host

		dnsRecord := &cloudflareoperatoriov1.DNSRecord{
			ObjectMeta: metav1.ObjectMeta{
				Name:      strings.ReplaceAll(rule.Host, ".", "-"),
				Namespace: ingress.Namespace,
				Labels: map[string]string{
					"app.kubernetes.io/managed-by": "cloudflare-operator",
				},
			},
			Spec: dnsRecordSpec,
		}

		if err := controllerutil.SetControllerReference(ingress, dnsRecord, r.Scheme); err != nil {
			log.Error(err, "Failed to set controller reference")
			return ctrl.Result{}, err
		}

		log.Info("Creating DNSRecord", "name", dnsRecord.Name)
		if err := r.Create(ctx, dnsRecord); err != nil {
			log.Error(err, "Failed to create DNSRecord")
			return ctrl.Result{}, err
		}
	}

	for _, rule := range ingress.Spec.Rules {
		dnsRecord, exists := dnsRecordMap[rule.Host]
		if !exists {
			continue
		}

		dnsRecordSpec.Name = rule.Host
		if reflect.DeepEqual(dnsRecord.Spec, dnsRecordSpec) {
			continue
		}

		log.Info("Updating DNSRecord", "name", dnsRecord.Name)
		dnsRecord.Spec = dnsRecordSpec
		if err := r.Update(ctx, &dnsRecord); err != nil {
			log.Error(err, "Failed to update DNSRecord")
			return ctrl.Result{}, err
		}
	}

	for _, dnsRecord := range dnsRecords.Items {
		if _, found := ingressRuleMap[dnsRecord.Spec.Name]; found {
			continue
		}
		log.Info("Deleting DNSRecord", "name", dnsRecord.Name)
		if err := r.Delete(ctx, &dnsRecord); err != nil {
			log.Error(err, "Failed to delete DNSRecord")
		}
	}

	return ctrl.Result{}, nil
}

// parseAnnotations parses ingress annotations and returns a DNSRecordSpec
func (r *IngressReconciler) parseAnnotations(annotations map[string]string) cloudflareoperatoriov1.DNSRecordSpec {
	dnsRecordSpec := cloudflareoperatoriov1.DNSRecordSpec{}

	dnsRecordSpec.Content = annotations["cloudflare-operator.io/content"]
	dnsRecordSpec.IPRef.Name = annotations["cloudflare-operator.io/ip-ref"]

	proxied, err := strconv.ParseBool(annotations["cloudflare-operator.io/proxied"])
	if err != nil {
		proxied = true
	}

	dnsRecordSpec.Proxied = &proxied
	ttl, err := strconv.Atoi(annotations["cloudflare-operator.io/ttl"])
	if err != nil {
		ttl = 1
	}
	if *dnsRecordSpec.Proxied && ttl != 1 {
		ttl = 1
	}
	dnsRecordSpec.TTL = ttl

	dnsRecordSpec.Type = annotations["cloudflare-operator.io/type"]
	if dnsRecordSpec.Type == "" {
		dnsRecordSpec.Type = "A"
	}

	intervalDuration, err := time.ParseDuration(annotations["cloudflare-operator.io/interval"])
	if err != nil {
		intervalDuration = 5 * time.Minute
	}
	dnsRecordSpec.Interval = metav1.Duration{Duration: intervalDuration}

	return dnsRecordSpec
}
