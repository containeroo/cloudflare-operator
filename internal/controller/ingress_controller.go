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
	"strconv"
	"strings"
	"time"

	cloudflareoperatoriov1 "github.com/containeroo/cloudflare-operator/api/v1"
	"github.com/containeroo/cloudflare-operator/internal/common"
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
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &cloudflareoperatoriov1.DNSRecord{}, "metadata.ownerReferences.uid", func(rawObj client.Object) []string {
		ownerReferences := rawObj.GetOwnerReferences()
		var ownerUIDs []string
		for _, ownerReference := range ownerReferences {
			ownerUIDs = append(ownerUIDs, string(ownerReference.UID))
		}

		return ownerUIDs
	}); err != nil {
		return err
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&networkingv1.Ingress{}).
		Complete(r)
}

// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=get;list;watch
// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *IngressReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	ingress := &networkingv1.Ingress{}
	if err := r.Get(ctx, req.NamespacedName, ingress); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if ingress.Annotations["cloudflare-operator.io/content"] == "" && ingress.Annotations["cloudflare-operator.io/ip-ref"] == "" {
		return ctrl.Result{}, nil
	}

	dnsRecords := &cloudflareoperatoriov1.DNSRecordList{}
	if err := r.List(ctx, dnsRecords, client.InNamespace(ingress.Namespace), client.MatchingFields{"metadata.ownerReferences.uid": string(ingress.UID)}); err != nil {
		log.Error(err, "Failed to fetch DNSRecord")
		return ctrl.Result{RequeueAfter: time.Second * 30}, err
	}

	dnsRecordSpec := cloudflareoperatoriov1.DNSRecordSpec{}

	if content, ok := ingress.Annotations["cloudflare-operator.io/content"]; ok {
		dnsRecordSpec.Content = content
	}

	if ipRef, ok := ingress.Annotations["cloudflare-operator.io/ip-ref"]; ok {
		dnsRecordSpec.IPRef.Name = ipRef
	}

	if proxied, ok := ingress.Annotations["cloudflare-operator.io/proxied"]; ok {
		switch proxied {
		case "true":
			dnsRecordSpec.Proxied = common.NewTrue()
		case "false":
			dnsRecordSpec.Proxied = common.NewFalse()
		default:
			dnsRecordSpec.Proxied = common.NewTrue()
			log.Error(nil, "Failed to parse proxied annotation, defaulting to true", "proxied", proxied, "ingress", ingress.Name)
		}
	}
	if dnsRecordSpec.Proxied == nil {
		dnsRecordSpec.Proxied = common.NewTrue()
	}

	if ttl, ok := ingress.Annotations["cloudflare-operator.io/ttl"]; ok {
		ttlInt, _ := strconv.Atoi(ttl)
		if *dnsRecordSpec.Proxied && ttlInt != 1 {
			log.Info("DNSRecord is proxied and ttl is not 1, skipping reconciliation", "ingress", ingress.Name)
			return ctrl.Result{}, nil
		}
		dnsRecordSpec.TTL = ttlInt
	}
	if dnsRecordSpec.TTL == 0 {
		dnsRecordSpec.TTL = 1
	}

	if recordType, ok := ingress.Annotations["cloudflare-operator.io/type"]; ok {
		dnsRecordSpec.Type = recordType
	}
	if dnsRecordSpec.Type == "" {
		dnsRecordSpec.Type = "A"
	}

	if interval, ok := ingress.Annotations["cloudflare-operator.io/interval"]; ok {
		intervalDuration, err := time.ParseDuration(interval)
		if err != nil {
			log.Error(err, "Failed to parse interval", "interval", interval, "ingress", ingress.Name)
			return ctrl.Result{}, err
		}
		dnsRecordSpec.Interval = metav1.Duration{Duration: intervalDuration}
	}
	if dnsRecordSpec.Interval.Duration == 0 {
		dnsRecordSpec.Interval.Duration = time.Minute * 5
	}

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
