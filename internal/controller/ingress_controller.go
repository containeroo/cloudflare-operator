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
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	cloudflareoperatoriov1 "github.com/containeroo/cloudflare-operator/api/v1"
	intpredicates "github.com/containeroo/cloudflare-operator/internal/predicates"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// IngressReconciler reconciles an Ingress object
type IngressReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// SetupWithManager sets up the controller with the Manager.
func (r *IngressReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&networkingv1.Ingress{}, builder.WithPredicates(intpredicates.DNSFromIngressPredicate{})).
		Owns(&cloudflareoperatoriov1.DNSRecord{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
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

	return r.reconcileIngress(ctx, ingress)
}

// reconcileIngress reconciles the ingress
func (r *IngressReconciler) reconcileIngress(ctx context.Context, ingress *networkingv1.Ingress) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	dnsRecords, err := r.getDNSRecords(ctx, ingress)
	if err != nil {
		log.Error(err, "Failed to list DNSRecords")
		return ctrl.Result{RequeueAfter: time.Second * 30}, nil
	}

	annotations := ingress.GetAnnotations()

	dnsRecordSpec := r.parseAnnotations(annotations)
	existingRecords := make(map[string]cloudflareoperatoriov1.DNSRecord)
	for _, record := range dnsRecords.Items {
		existingRecords[record.Spec.Name] = record
	}

	ingressHosts := r.getIngressHosts(ingress)

	if err := r.reconcileDNSRecords(ctx, ingress, dnsRecordSpec, existingRecords, ingressHosts); err != nil {
		log.Error(err, "Failed to reconcile DNS records")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// getDNSRecords returns a list of DNSRecords
func (r *IngressReconciler) getDNSRecords(ctx context.Context, ingress *networkingv1.Ingress) (*cloudflareoperatoriov1.DNSRecordList, error) {
	dnsRecords := &cloudflareoperatoriov1.DNSRecordList{}
	err := r.List(ctx, dnsRecords, client.InNamespace(ingress.Namespace), client.MatchingFields{cloudflareoperatoriov1.OwnerRefUIDIndexKey: string(ingress.UID)})
	return dnsRecords, err
}

// getIngressHosts returns a map of hosts from the ingress rules
func (r *IngressReconciler) getIngressHosts(ingress *networkingv1.Ingress) map[string]struct{} {
	hosts := make(map[string]struct{})
	for _, rule := range ingress.Spec.Rules {
		if rule.Host != "" {
			hosts[rule.Host] = struct{}{}
		}
	}
	return hosts
}

// reconcileDNSRecords reconciles the DNSRecords
func (r *IngressReconciler) reconcileDNSRecords(ctx context.Context, ingress *networkingv1.Ingress, dnsRecordSpec cloudflareoperatoriov1.DNSRecordSpec, existingRecords map[string]cloudflareoperatoriov1.DNSRecord, ingressHosts map[string]struct{}) error {
	log := ctrl.LoggerFrom(ctx)

	for host := range ingressHosts {
		record, exists := existingRecords[host]
		dnsRecordSpec.Name = host

		if !exists {
			if err := r.createDNSRecord(ctx, ingress, dnsRecordSpec); err != nil {
				return fmt.Errorf("failed to create DNSRecord for %s: %w", host, err)
			}
			continue
		}

		if !reflect.DeepEqual(record.Spec, dnsRecordSpec) {
			record.Spec = dnsRecordSpec
			if err := r.Update(ctx, &record); err != nil {
				return fmt.Errorf("failed to update DNSRecord for %s: %w", host, err)
			}
		}
	}

	for host, record := range existingRecords {
		if _, exists := ingressHosts[host]; !exists {
			if err := r.Delete(ctx, &record); err != nil {
				log.Error(err, "Failed to delete DNSRecord", "host", host)
			}
		}
	}

	return nil
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
	replacer := strings.NewReplacer(".", "-", "*", "wildcard")
	dnsRecord := &cloudflareoperatoriov1.DNSRecord{
		ObjectMeta: metav1.ObjectMeta{
			Name:      replacer.Replace(dnsRecordSpec.Name),
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
