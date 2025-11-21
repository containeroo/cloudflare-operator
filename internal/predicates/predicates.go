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
	"slices"

	cloudflareoperatoriov1 "github.com/containeroo/cloudflare-operator/api/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// DNSFromIngressPredicate detects if an Ingress object has the required annotations,
// has changed annoations or has changed hosts.
type DNSFromIngressPredicate struct {
	predicate.Funcs
}

func hasRequiredDNSAnnotations(annotations map[string]string) bool {
	if annotations == nil {
		return false
	}
	return annotations["cloudflare-operator.io/content"] != "" || annotations["cloudflare-operator.io/ip-ref"] != ""
}

func (DNSFromIngressPredicate) Create(e event.CreateEvent) bool {
	return hasRequiredDNSAnnotations(e.Object.GetAnnotations())
}

func (DNSFromIngressPredicate) Update(e event.UpdateEvent) bool {
	oldAnnotations := e.ObjectOld.GetAnnotations()
	newAnnotations := e.ObjectNew.GetAnnotations()

	oldObjectHadRequiredAnnotations := hasRequiredDNSAnnotations(oldAnnotations)
	newObjectHasRequiredAnnotations := hasRequiredDNSAnnotations(newAnnotations)

	oldObj := e.ObjectOld.(*networkingv1.Ingress)
	newObj := e.ObjectNew.(*networkingv1.Ingress)

	annotationsChanged := !reflect.DeepEqual(oldAnnotations, newAnnotations)
	oldHosts := []string{}
	for _, rule := range oldObj.Spec.Rules {
		oldHosts = append(oldHosts, rule.Host)
	}
	newHosts := []string{}
	for _, rule := range newObj.Spec.Rules {
		newHosts = append(newHosts, rule.Host)
	}

	slices.Sort(oldHosts)
	slices.Sort(newHosts)

	hostsChanged := !reflect.DeepEqual(oldHosts, newHosts)

	return (newObjectHasRequiredAnnotations && (annotationsChanged || hostsChanged)) || (oldObjectHadRequiredAnnotations && !newObjectHasRequiredAnnotations)
}

func (DNSFromIngressPredicate) Delete(e event.DeleteEvent) bool {
	return false
}

// DNSFromHTTPRoutePredicate detects if an HTTPRoute object has the required annotations,
// has changed annotations or has changed hostnames.
type DNSFromHTTPRoutePredicate struct {
	predicate.Funcs
}

func (DNSFromHTTPRoutePredicate) Create(e event.CreateEvent) bool {
	return hasRequiredDNSAnnotations(e.Object.GetAnnotations())
}

func (DNSFromHTTPRoutePredicate) Update(e event.UpdateEvent) bool {
	oldAnnotations := e.ObjectOld.GetAnnotations()
	newAnnotations := e.ObjectNew.GetAnnotations()

	oldObjectHadRequiredAnnotations := hasRequiredDNSAnnotations(oldAnnotations)
	newObjectHasRequiredAnnotations := hasRequiredDNSAnnotations(newAnnotations)

	oldObj := e.ObjectOld.(*gatewayv1.HTTPRoute)
	newObj := e.ObjectNew.(*gatewayv1.HTTPRoute)

	annotationsChanged := !reflect.DeepEqual(oldAnnotations, newAnnotations)
	oldHosts := []string{}
	for _, hostname := range oldObj.Spec.Hostnames {
		oldHosts = append(oldHosts, string(hostname))
	}
	newHosts := []string{}
	for _, hostname := range newObj.Spec.Hostnames {
		newHosts = append(newHosts, string(hostname))
	}

	slices.Sort(oldHosts)
	slices.Sort(newHosts)

	hostsChanged := !reflect.DeepEqual(oldHosts, newHosts)

	return (newObjectHasRequiredAnnotations && (annotationsChanged || hostsChanged)) || (oldObjectHadRequiredAnnotations && !newObjectHasRequiredAnnotations)
}

func (DNSFromHTTPRoutePredicate) Delete(e event.DeleteEvent) bool {
	return false
}

// IPAddressChangedPredicate detects if an Ingress object has a change in the IP address.
type IPAddressChangedPredicate struct {
	predicate.Funcs
}

func (IPAddressChangedPredicate) Update(e event.UpdateEvent) bool {
	oldObj := e.ObjectOld.(*cloudflareoperatoriov1.IP)
	newObj := e.ObjectNew.(*cloudflareoperatoriov1.IP)

	return oldObj.Spec.Address != newObj.Spec.Address
}
