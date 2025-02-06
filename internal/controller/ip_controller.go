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
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	cloudflareoperatoriov1 "github.com/containeroo/cloudflare-operator/api/v1"
	"github.com/containeroo/cloudflare-operator/internal/common"
	"github.com/containeroo/cloudflare-operator/internal/metrics"
	"github.com/go-logr/logr"
	"github.com/itchyny/gojq"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// IPReconciler reconciles a IP object
type IPReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// SetupWithManager sets up the controller with the Manager.
func (r *IPReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&cloudflareoperatoriov1.IP{}).
		Complete(r)
}

// +kubebuilder:rbac:groups=cloudflare-operator.io,resources=ips,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cloudflare-operator.io,resources=ips/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=cloudflare-operator.io,resources=ips/finalizers,verbs=update
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *IPReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	ip := &cloudflareoperatoriov1.IP{}
	if err := r.Get(ctx, req.NamespacedName, ip); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !controllerutil.ContainsFinalizer(ip, common.CloudflareOperatorFinalizer) {
		controllerutil.AddFinalizer(ip, common.CloudflareOperatorFinalizer)
		if err := r.Update(ctx, ip); err != nil {
			log.Error(err, "Failed to update IP finalizer")
			return ctrl.Result{}, err
		}
	}

	if !ip.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(ip, common.CloudflareOperatorFinalizer) {
			metrics.IpFailureCounter.DeleteLabelValues(ip.Name, ip.Spec.Type)
		}

		controllerutil.RemoveFinalizer(ip, common.CloudflareOperatorFinalizer)
		if err := r.Update(ctx, ip); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	metrics.IpFailureCounter.WithLabelValues(ip.Name, ip.Spec.Type).Set(0)

	if ip.Spec.Type == "static" {
		if ip.Spec.Address == "" {
			if err := r.markFailed(ip, ctx, "Address is required for static IPs"); err != nil {
				log.Error(err, "Failed to update IP resource")
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}
		if net.ParseIP(ip.Spec.Address) == nil {
			if err := r.markFailed(ip, ctx, "Address is not a valid IP address"); err != nil {
				log.Error(err, "Failed to update IP resource")
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}
	}

	if ip.Spec.Type == "dynamic" {
		if ip.Spec.Interval == nil {
			ip.Spec.Interval = &metav1.Duration{Duration: time.Minute * 5}
			if err := r.Update(ctx, ip); err != nil {
				log.Error(err, "Failed to update IP resource")
				return ctrl.Result{}, err
			}
		}

		if len(ip.Spec.IPSources) == 0 {
			if err := r.markFailed(ip, ctx, "IPSources is required for dynamic IPs"); err != nil {
				log.Error(err, "Failed to update IP resource")
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}

		if len(ip.Spec.IPSources) > 1 {
			rand.Shuffle(len(ip.Spec.IPSources), func(i, j int) {
				ip.Spec.IPSources[i], ip.Spec.IPSources[j] = ip.Spec.IPSources[j], ip.Spec.IPSources[i]
			})
		}

		var ipSourceError string
		for _, source := range ip.Spec.IPSources {
			response, err := r.getIPSource(ctx, source, log)
			if err != nil {
				ipSourceError = err.Error()
				continue
			}
			ip.Spec.Address = response
			ipSourceError = ""
			break
		}

		if ipSourceError != "" {
			if err := r.markFailed(ip, ctx, ipSourceError); err != nil {
				log.Error(err, "Failed to update IP resource")
				return ctrl.Result{}, err
			}
			return ctrl.Result{RequeueAfter: time.Second * 30}, nil
		}
	}

	if ip.Spec.Address != ip.Status.LastObservedIP {
		if err := r.Update(ctx, ip); err != nil {
			log.Error(err, "Failed to update IP resource")
			return ctrl.Result{}, err
		}
		ip.Status.LastObservedIP = ip.Spec.Address
		if err := r.Status().Update(ctx, ip); err != nil {
			log.Error(err, "Failed to update IP resource")
			return ctrl.Result{}, err
		}
	}

	dnsRecords := &cloudflareoperatoriov1.DNSRecordList{}
	if err := r.List(ctx, dnsRecords); err != nil {
		log.Error(err, "Failed to list DNSRecords")
		return ctrl.Result{RequeueAfter: time.Second * 30}, err
	}

	for _, dnsRecord := range dnsRecords.Items {
		if dnsRecord.Spec.IPRef.Name != ip.Name {
			continue
		}
		if dnsRecord.Spec.Content == ip.Spec.Address {
			continue
		}
		dnsRecord.Spec.Content = ip.Spec.Address
		if err := r.Update(ctx, &dnsRecord); err != nil {
			log.Error(err, "Failed to update DNSRecord")
		}
	}

	apimeta.SetStatusCondition(&ip.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             "True",
		Reason:             "Ready",
		Message:            "IP is ready",
		ObservedGeneration: ip.Generation,
	})
	if err := r.Status().Update(ctx, ip); err != nil {
		log.Error(err, "Failed to update IP resource")
		return ctrl.Result{}, err
	}

	if ip.Spec.Type == "dynamic" {
		return ctrl.Result{RequeueAfter: ip.Spec.Interval.Duration}, nil
	}

	return ctrl.Result{}, nil
}

// getIPSource returns the IP gathered from the IPSource
func (r *IPReconciler) getIPSource(ctx context.Context, source cloudflareoperatoriov1.IPSpecIPSources, log logr.Logger) (string, error) {
	if _, err := url.Parse(source.URL); err != nil {
		return "", fmt.Errorf("failed to parse URL %s: %s", source.URL, err)
	}

	tr := http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: source.InsecureSkipVerify},
		Proxy:           http.ProxyFromEnvironment,
	}
	httpClient := &http.Client{Transport: &tr}
	req, err := http.NewRequest(source.RequestMethod, source.URL, io.Reader(bytes.NewBuffer([]byte(source.RequestBody))))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %s", err)
	}

	if source.RequestHeaders != nil {
		var requestHeaders map[string]string
		if err := json.Unmarshal(source.RequestHeaders.Raw, &requestHeaders); err != nil {
			return "", fmt.Errorf("failed to unmarshal request headers: %s", err)
		}

		for key, value := range requestHeaders {
			req.Header.Add(key, value)
		}
	}

	if source.RequestHeadersSecretRef.Name != "" {
		secret := &corev1.Secret{}
		if err := r.Get(ctx, client.ObjectKey{
			Name:      source.RequestHeadersSecretRef.Name,
			Namespace: source.RequestHeadersSecretRef.Namespace,
		}, secret); err != nil {
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
		if err := Body.Close(); err != nil {
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
	if source.ResponseJQFilter != "" {
		var jsonResponse interface{}
		if err := json.Unmarshal(response, &jsonResponse); err != nil {
			return "", fmt.Errorf("failed to get IP from %s: %s", source.URL, err)
		}
		jq, err := gojq.Parse(source.ResponseJQFilter)
		if err != nil {
			return "", fmt.Errorf("failed to parse jq filter %s: %s", source.ResponseJQFilter, err)
		}
		iter := jq.Run(jsonResponse)
		result, ok := iter.Next()
		if !ok {
			return "", fmt.Errorf("failed to extract IP from %s. jq returned no results", source.URL)
		}
		extractedIP = fmt.Sprintf("%v", result)
	}

	if source.PostProcessingRegex != "" {
		re, err := regexp.Compile(source.PostProcessingRegex)
		if err != nil {
			return "", fmt.Errorf("failed to compile regex %s: %s", source.PostProcessingRegex, err)
		}
		match := re.FindStringSubmatch(extractedIP)
		if match == nil {
			return "", fmt.Errorf("failed to extract IP from %s. regex returned no matches", source.URL)
		}
		if len(match) < 2 {
			return "", fmt.Errorf("failed to extract IP from %s. regex returned no matches", source.URL)
		}
		extractedIP = match[1]
	}

	if net.ParseIP(extractedIP) == nil {
		return "", fmt.Errorf("ip from source %s is invalid: %s", source.URL, extractedIP)
	}

	return strings.TrimSpace(extractedIP), nil
}

// markFailed marks the reconciled object as failed
func (r *IPReconciler) markFailed(ip *cloudflareoperatoriov1.IP, ctx context.Context, message string) error {
	metrics.IpFailureCounter.WithLabelValues(ip.Name, ip.Spec.Type).Set(1)
	apimeta.SetStatusCondition(&ip.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             "False",
		Reason:             "Failed",
		Message:            message,
		ObservedGeneration: ip.Generation,
	})
	if err := r.Status().Update(ctx, ip); err != nil {
		return err
	}

	return nil
}
