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
	"github.com/go-logr/logr"
	"io/ioutil"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"math/rand"
	"net"
	"net/http"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	cfv1alpha1 "github.com/containeroo/cloudflare-operator/api/v1alpha1"
)

// IPReconciler reconciles a IP object
type IPReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=cf.containeroo.ch,resources=ips,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=cf.containeroo.ch,resources=ips/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=cf.containeroo.ch,resources=ips/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the IP object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.10.0/pkg/reconcile
func (r *IPReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)

	// Fetch the IP instance
	instance := &cfv1alpha1.IP{}
	err := r.Get(ctx, req.NamespacedName, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			log.Info("IP resource not found. Ignoring since object must be deleted.")
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get IP resource")
		return ctrl.Result{}, err
	}

	if instance.Spec.Type == "dynamic" {
		if instance.Spec.Interval == nil {
			instance.Spec.Interval = &metav1.Duration{Duration: time.Minute * 5}
			err := r.Update(ctx, instance)
			if err != nil {
				log.Error(err, "Failed to update IP resource")
				return ctrl.Result{}, err
			}
		}
		if instance.Spec.DynamicIpSources == nil {
			instance.Spec.DynamicIpSources = append(instance.Spec.DynamicIpSources, "https://ifconfig.me/ip", "https://ipecho.net/plain", "https://myip.is/ip/", "https://checkip.amazonaws.com", "https://api.ipify.org")
			err := r.Update(ctx, instance)
			if err != nil {
				log.Error(err, "Failed to update IP resource")
				return ctrl.Result{}, err
			}
		}
		currentIP := getCurrentIP(instance.Spec.DynamicIpSources, log)
		if currentIP == "" {
			log.Info("No IP found")
			return ctrl.Result{}, nil
		}

		instance.Spec.Address = currentIP
		err = r.Update(ctx, instance)
		if err != nil {
			log.Error(err, "Failed to update IP resource")
			return ctrl.Result{}, err
		}
	}

	if instance.Spec.Address != instance.Status.LastObservedIP {
		log.Info("IP has changed. Updating IP resource")
		instance.Status.LastObservedIP = instance.Spec.Address
		err = r.Status().Update(ctx, instance)
		if err != nil {
			log.Error(err, "Failed to update IP resource")
			return ctrl.Result{}, err
		}
	}

	dnsRecords := &cfv1alpha1.DNSRecordList{}
	err = r.List(ctx, dnsRecords, client.InNamespace(instance.Namespace))
	if err != nil {
		log.Error(err, "Failed to list DNS records")
	}

	for _, dnsRecord := range dnsRecords.Items {
		if dnsRecord.Spec.IpRef.Name != instance.ObjectMeta.Name {
			continue
		}
		if dnsRecord.Spec.Content == instance.Spec.Address {
			continue
		}
		dnsRecord.Spec.Content = instance.Spec.Address
		err = r.Update(ctx, &dnsRecord)
		if err != nil {
			log.Error(err, "Failed to update DNS record")
		}
	}

	if instance.Spec.Type == "dynamic" {
		return ctrl.Result{RequeueAfter: instance.Spec.Interval.Duration}, nil
	}
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *IPReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&cfv1alpha1.IP{}).
		Complete(r)
}

// getCurrentIP returns the current public IP
func getCurrentIP(sources []string, log logr.Logger) string {
	rand.Seed(time.Now().UnixNano())
	rand.Shuffle(len(sources), func(i, j int) { sources[i], sources[j] = sources[j], sources[i] })

	var success bool
	var currentIP string
	for _, provider := range sources {
		success = false
		resp, err := http.Get(provider)
		if err != nil {
			log.Error(err, "Unable to get public ip from %s: %s")
			continue
		}
		if resp.StatusCode != 200 {
			log.Info("Unable to get public ip from %s: http status code %d")
			continue
		}
		ip, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			log.Error(err, "Unable to get public ip from %s")
			continue
		}
		currentIP = strings.TrimSpace(string(ip))
		if net.ParseIP(currentIP) == nil {
			log.Info("Public ip is invalid")
			continue
		}
		success = true
		break
	}
	if !success {
		log.Info("Unable to get public ip from any configured provider")
		return ""
	}

	return currentIP
}
