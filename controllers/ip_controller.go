/*
Copyright 2022 containeroo

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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	cfv1beta1 "github.com/containeroo/cloudflare-operator/api/v1beta1"
	"github.com/go-logr/logr"
	"io"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/util/jsonpath"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"regexp"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	"strings"
	"time"
)

// IPReconciler reconciles a IP object
type IPReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=cf.containeroo.ch,resources=ips,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cf.containeroo.ch,resources=ips/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=cf.containeroo.ch,resources=ips/finalizers,verbs=update
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *IPReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx)

	instance := &cfv1beta1.IP{}
	err := r.Get(ctx, req.NamespacedName, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			log.Info("IP resource not found. Ignoring since object must be deleted.")
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get IP resource")
		return ctrl.Result{}, err
	}

	if !controllerutil.ContainsFinalizer(instance, cloudflareOperatorFinalizer) {
		controllerutil.AddFinalizer(instance, cloudflareOperatorFinalizer)
		err := r.Update(ctx, instance)
		if err != nil {
			log.Error(err, "Failed to update IP finalizer")
			return ctrl.Result{}, err
		}
	}

	if instance.GetDeletionTimestamp() != nil {
		if controllerutil.ContainsFinalizer(instance, cloudflareOperatorFinalizer) {
			ipFailureCounter.DeleteLabelValues(instance.Name, instance.Spec.Type)
		}

		controllerutil.RemoveFinalizer(instance, cloudflareOperatorFinalizer)
		err := r.Update(ctx, instance)
		if err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	ipFailureCounter.WithLabelValues(instance.Name, instance.Spec.Type).Set(0)

	if instance.Spec.Type == "static" {
		if instance.Spec.Address == "" {
			err := r.markFailed(instance, ctx, "Address is required for static IPs")
			if err != nil {
				log.Error(err, "Failed to update IP resource")
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}
		if net.ParseIP(instance.Spec.Address) == nil {
			err := r.markFailed(instance, ctx, "Address is not a valid IP address")
			if err != nil {
				log.Error(err, "Failed to update IP resource")
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}
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

		if len(instance.Spec.IPSources) == 0 {
			err := r.markFailed(instance, ctx, "IPSources is required for dynamic IPs")
			if err != nil {
				log.Error(err, "Failed to update IP resource")
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, err
		}

		if len(instance.Spec.IPSources) > 1 {
			rand.Seed(time.Now().UnixNano())
			rand.Shuffle(len(instance.Spec.IPSources), func(i, j int) {
				instance.Spec.IPSources[i], instance.Spec.IPSources[j] = instance.Spec.IPSources[j], instance.Spec.IPSources[i]
			})
		}

		var ipSourceError string
		for _, source := range instance.Spec.IPSources {
			response, err := r.getIPSource(ctx, source, log)
			if err != nil {
				ipSourceError = err.Error()
				continue
			}
			instance.Spec.Address = response
			ipSourceError = ""
			break
		}

		if ipSourceError != "" {
			err := r.markFailed(instance, ctx, ipSourceError)
			if err != nil {
				log.Error(err, "Failed to update IP resource")
				return ctrl.Result{}, err
			}
			return ctrl.Result{RequeueAfter: time.Second * 30}, err
		}
	}

	if instance.Spec.Address != instance.Status.LastObservedIP {
		err := r.Update(ctx, instance)
		if err != nil {
			log.Error(err, "Failed to update IP resource")
			return ctrl.Result{}, err
		}
		instance.Status.LastObservedIP = instance.Spec.Address
		err = r.Status().Update(ctx, instance)
		if err != nil {
			log.Error(err, "Failed to update IP resource")
			return ctrl.Result{}, err
		}
	}

	dnsRecords := &cfv1beta1.DNSRecordList{}
	err = r.List(ctx, dnsRecords, client.InNamespace(instance.Namespace))
	if err != nil {
		log.Error(err, "Failed to list DNSRecords")
		return ctrl.Result{RequeueAfter: time.Second * 30}, err
	}

	for _, dnsRecord := range dnsRecords.Items {
		if dnsRecord.Spec.IPRef.Name != instance.Name {
			continue
		}
		if dnsRecord.Spec.Content == instance.Spec.Address {
			continue
		}
		dnsRecord.Spec.Content = instance.Spec.Address
		err := r.Update(ctx, &dnsRecord)
		if err != nil {
			log.Error(err, "Failed to update DNSRecord")
		}
	}

	if instance.Status.Phase != "Ready" || instance.Status.Message != "" {
		instance.Status.Phase = "Ready"
		instance.Status.Message = ""
		err := r.Status().Update(ctx, instance)
		if err != nil {
			log.Error(err, "Failed to update IP resource")
			return ctrl.Result{}, err
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
		For(&cfv1beta1.IP{}).
		Complete(r)
}

// getIPSource returns the IP gathered from the IPSource
func (r *IPReconciler) getIPSource(ctx context.Context, source cfv1beta1.IPSpecIPSources, log logr.Logger) (string, error) {
	_, err := url.Parse(source.URL)
	if err != nil {
		return "", fmt.Errorf("failed to parse URL %s: %s", source.URL, err)
	}

	httpClient := &http.Client{}
	req, err := http.NewRequest(source.RequestMethod, source.URL, io.Reader(bytes.NewBuffer([]byte(source.RequestBody))))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %s", err)
	}

	for key, value := range source.RequestHeaders {
		req.Header.Add(key, value)
	}

	if source.RequestHeadersSecretRef.Name != "" {
		secret := &v1.Secret{}
		err := r.Get(ctx, client.ObjectKey{Name: source.RequestHeadersSecretRef.Name,
			Namespace: source.RequestHeadersSecretRef.Namespace}, secret)
		if err != nil {
			return "", fmt.Errorf("failed to get secret %s: %s", source.RequestHeadersSecretRef.Name, err)
		}
		for key, value := range secret.Data {
			req.Header.Add(key, string(value))
		}
	}

	httpClient.Timeout = time.Second * 30
	req.Header.Add("User-Agent", "cloudflare-operator")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to get IP from %s: %s", source.URL, err)
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			log.Error(err, "Failed to close response body")
		}
	}(resp.Body)

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("failed to get IP from %s: %s", source.URL, resp.Status)
	}
	response, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to get IP from %s: %s", source.URL, err)
	}

	extractedIP := string(response)
	if source.ResponseJSONPath != "" {
		var jsonResponse map[string]interface{}
		err := json.Unmarshal(response, &jsonResponse)
		if err != nil {
			return "", fmt.Errorf("failed to get IP from %s: %s", source.URL, err)
		}
		j := jsonpath.New("jsonpath")
		buf := new(bytes.Buffer)
		if err := j.Parse(source.ResponseJSONPath); err != nil {
			return "", fmt.Errorf("failed to parse jsonpath %s: %s", source.ResponseJSONPath, err)
		}
		if err := j.Execute(buf, jsonResponse); err != nil {
			return "", fmt.Errorf("failed to extract IP from %s: %s", source.URL, err)
		}

		extractedIP = buf.String()
	}

	if source.ResponseRegex != "" {
		re, err := regexp.Compile(source.ResponseRegex)
		if err != nil {
			return "", fmt.Errorf("failed to compile regex %s: %s", source.ResponseRegex, err)
		}
		extractedIPBytes := []byte(extractedIP)
		match := re.Find(extractedIPBytes)
		if match == nil {
			return "", fmt.Errorf("failed to extract IP from %s. regex returned no matches", source.URL)
		}
		extractedIP = string(match)
	}

	if net.ParseIP(extractedIP) == nil {
		return "", fmt.Errorf("ip from source %s is invalid: %s", source.URL, extractedIP)
	}

	return strings.TrimSpace(extractedIP), nil
}

// markFailed marks the reconciled object as failed
func (r *IPReconciler) markFailed(instance *cfv1beta1.IP, ctx context.Context, message string) error {
	ipFailureCounter.WithLabelValues(instance.Name, instance.Spec.Type).Set(1)
	instance.Status.Phase = "Failed"
	instance.Status.Message = message
	if err := r.Status().Update(ctx, instance); err != nil {
		return err
	}
	return nil
}
