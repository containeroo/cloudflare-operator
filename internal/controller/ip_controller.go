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
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	cloudflareoperatoriov1 "github.com/containeroo/cloudflare-operator/api/v1"
	"github.com/containeroo/cloudflare-operator/internal/common"
	"github.com/containeroo/cloudflare-operator/internal/metrics"
	"github.com/fluxcd/pkg/runtime/patch"
	"github.com/itchyny/gojq"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	apierrutil "k8s.io/apimachinery/pkg/util/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
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
func (r *IPReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, retErr error) {
	ip := &cloudflareoperatoriov1.IP{}
	if err := r.Get(ctx, req.NamespacedName, ip); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	patchHelper := patch.NewSerialPatcher(ip, r.Client)

	defer func() {
		patchOpts := []patch.Option{}

		if errors.Is(retErr, reconcile.TerminalError(nil)) || (retErr == nil && (result.IsZero() || !result.Requeue)) {
			patchOpts = append(patchOpts, patch.WithStatusObservedGeneration{})
		}

		if err := patchHelper.Patch(ctx, ip, patchOpts...); err != nil {
			if !ip.DeletionTimestamp.IsZero() {
				err = apierrutil.FilterOut(err, func(e error) bool { return apierrors.IsNotFound(e) })
			}
			retErr = apierrutil.Reduce(apierrutil.NewAggregate([]error{retErr, err}))
		}
	}()

	if !ip.DeletionTimestamp.IsZero() {
		r.reconcileDelete(ip)
		return ctrl.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(ip, common.CloudflareOperatorFinalizer) {
		controllerutil.AddFinalizer(ip, common.CloudflareOperatorFinalizer)
		return ctrl.Result{Requeue: true}, nil
	}

	return r.reconcileIP(ctx, ip), nil
}

func (r *IPReconciler) reconcileIP(ctx context.Context, ip *cloudflareoperatoriov1.IP) ctrl.Result {
	switch ip.Spec.Type {
	case "static":
		if err := r.handleStatic(ip); err != nil {
			common.MarkFalse(ip, err)
			return ctrl.Result{}
		}
	case "dynamic":
		if err := r.handleDynamic(ctx, ip); err != nil {
			common.MarkFalse(ip, err)
			return ctrl.Result{}
		}
	}

	common.MarkTrue(ip, "IP is ready")

	if ip.Spec.Type == "dynamic" {
		return ctrl.Result{RequeueAfter: ip.Spec.Interval.Duration}
	}

	return ctrl.Result{}
}

// handleStatic handles the static ip
func (r *IPReconciler) handleStatic(ip *cloudflareoperatoriov1.IP) error {
	if ip.Spec.Address == "" {
		return errors.New("Address is required for static IPs")
	}
	if net.ParseIP(ip.Spec.Address) == nil {
		return errors.New("Address is not a valid IP address")
	}
	return nil
}

// handleDynamic handles the dynamic ip
func (r *IPReconciler) handleDynamic(ctx context.Context, ip *cloudflareoperatoriov1.IP) error {
	if ip.Spec.Interval == nil {
		ip.Spec.Interval = &metav1.Duration{Duration: time.Minute * 5}
	}
	if len(ip.Spec.IPSources) == 0 {
		return errors.New("IP sources are required for dynamic IPs")
	}
	// DeepCopy the ip sources to avoid modifying the original slice which would cause the object to be updated on every reconcile
	// which would lead to an infinite loop
	ipSources := ip.Spec.DeepCopy().IPSources
	rand.Shuffle(len(ipSources), func(i, j int) {
		ipSources[i], ipSources[j] = ipSources[j], ipSources[i]
	})
	var ipSourceError error
	for _, source := range ipSources {
		response, err := r.getIPSource(ctx, source)
		if err != nil {
			ipSourceError = err
			continue
		}
		ip.Spec.Address = response
		ipSourceError = nil
		break
	}
	if ipSourceError != nil {
		return ipSourceError
	}
	return nil
}

// getIPSource returns the IP gathered from the IPSource
func (r *IPReconciler) getIPSource(ctx context.Context, source cloudflareoperatoriov1.IPSpecIPSources) (string, error) {
	log := ctrl.LoggerFrom(ctx)

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

	extractedIP = strings.TrimSpace(extractedIP)
	if net.ParseIP(extractedIP) == nil {
		return "", fmt.Errorf("ip from source %s is invalid: %s", source.URL, extractedIP)
	}

	return extractedIP, nil
}

// reconcileDelete reconciles the deletion of the ip
func (r *IPReconciler) reconcileDelete(ip *cloudflareoperatoriov1.IP) {
	metrics.IpFailureCounter.DeleteLabelValues(ip.Name, ip.Spec.Type)
	controllerutil.RemoveFinalizer(ip, common.CloudflareOperatorFinalizer)
}
