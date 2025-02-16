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
func (r *IngressReconciler) SetupWithManager(mgr ctrl.Manager) error {
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
	if err := r.List(ctx, dnsRecords, client.InNamespace(ingress.Namespace), client.MatchingFields{cloudflareoperatoriov1.OwnerRefUIDIndexKey: string(ingress.UID)}); err != nil {
		log.Error(err, "Failed to list DNSRecords")
		return ctrl.Result{RequeueAfter: time.Second * 30}, nil
	}

	dnsRecordSpec := r.parseAnnotations(annotations)

	dnsRecordMap := make(map[string]cloudflareoperatoriov1.DNSRecord)
	for _, dnsRecord := range dnsRecords.Items {
		dnsRecordMap[dnsRecord.Spec.Name] = dnsRecord
	}

	ingressRules := make(map[string]struct{})
	for _, rule := range ingress.Spec.Rules {
		if rule.Host == "" {
			continue
		}
		ingressRules[rule.Host] = struct{}{}
		dnsRecordSpec.Name = rule.Host
		if dnsRecord, found := dnsRecordMap[rule.Host]; !found {
			if err := r.createDNSRecord(ctx, ingress, dnsRecordSpec); err != nil {
				log.Error(err, "Failed to create DNSRecord")
				return ctrl.Result{}, err
			}
		} else if !reflect.DeepEqual(dnsRecord.Spec, dnsRecordSpec) {
			dnsRecord.Spec = dnsRecordSpec
			if err := r.Update(ctx, &dnsRecord); err != nil {
				log.Error(err, "Failed to update DNSRecord")
				return ctrl.Result{}, err
			}
		}
	}

	for _, dnsRecord := range dnsRecords.Items {
		if _, found := ingressRules[dnsRecord.Spec.Name]; found {
			continue
		}
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

// createDNSRecord creates a DNSRecord object
func (r *IngressReconciler) createDNSRecord(ctx context.Context, ingress *networkingv1.Ingress, dnsRecordSpec cloudflareoperatoriov1.DNSRecordSpec) error {
	dnsRecord := &cloudflareoperatoriov1.DNSRecord{
		ObjectMeta: metav1.ObjectMeta{
			Name:      strings.ReplaceAll(dnsRecordSpec.Name, ".", "-"),
			Namespace: ingress.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "cloudflare-operator",
			},
		},
		Spec: dnsRecordSpec,
	}
	if err := controllerutil.SetControllerReference(ingress, dnsRecord, r.Scheme); err != nil {
		return err
	}
	return r.Create(ctx, dnsRecord)
}
