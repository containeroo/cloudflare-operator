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

package predicates

import (
	"reflect"

	cloudflareoperatorv1 "github.com/containeroo/cloudflare-operator/api/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// DNSFromIngressPredicate detects if an Ingress object has the required annotations,
// has changed annoations or has changed hosts.
type DNSFromIngressPredicate struct {
	predicate.Funcs
}

func ingressHasRequiredAnnotations(annotations map[string]string) bool {
	if annotations == nil {
		return false
	}
	return annotations["cloudflare-operator.io/content"] != "" || annotations["cloudflare-operator.io/ip-ref"] != ""
}

func (DNSFromIngressPredicate) Create(e event.CreateEvent) bool {
	return ingressHasRequiredAnnotations(e.Object.GetAnnotations())
}

func (DNSFromIngressPredicate) Update(e event.UpdateEvent) bool {
	oldAnnotations := e.ObjectOld.GetAnnotations()
	newAnnotations := e.ObjectNew.GetAnnotations()

	oldObj := e.ObjectOld.(*networkingv1.Ingress)
	newObj := e.ObjectNew.(*networkingv1.Ingress)

	annotationsChanged := !reflect.DeepEqual(oldAnnotations, newAnnotations)
	rulesChanged := !reflect.DeepEqual(oldObj.Spec.Rules, newObj.Spec.Rules)

	return ingressHasRequiredAnnotations(newAnnotations) && (annotationsChanged || rulesChanged)
}

func (DNSFromIngressPredicate) Delete(e event.DeleteEvent) bool {
	return false
}

// IPAddressChangedPredicate detects if an Ingress object has a change in the IP address.
type IPAddressChangedPredicate struct {
	predicate.Funcs
}

func (IPAddressChangedPredicate) Update(e event.UpdateEvent) bool {
	oldObj := e.ObjectOld.(*cloudflareoperatorv1.IP)
	newObj := e.ObjectNew.(*cloudflareoperatorv1.IP)

	return oldObj.Spec.Address != newObj.Spec.Address
}
