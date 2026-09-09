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
	"os"
	"testing"
	"time"

	api "github.com/containeroo/cloudflare-operator/api/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

// Run with KUBEBUILDER_ASSETS pointing to setup-envtest's local binaries.
// This checks admission and actual informer events, which fake clients cannot model.
func TestAdmissionAndFinalization(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("set KUBEBUILDER_ASSETS to run Kubernetes admission and informer integration tests")
	}
	environment := &envtest.Environment{CRDDirectoryPaths: []string{"../../config/crd/bases"}, ErrorIfCRDPathMissing: true}
	config, err := environment.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := environment.Stop(); err != nil {
			t.Error(err)
		}
	})
	scheme := newTestScheme()
	kube, err := client.New(config, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	if err := kube.Create(t.Context(), &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: testDefaultNamespace}}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatal(err)
	}
	for _, kind := range []string{"Account", "Zone", "DNSRecord", "IP"} {
		t.Run(kind, func(t *testing.T) {
			for i, interval := range []string{"tomorrow", "0s", "-1m", "999999999999999999999h", "0.1ns", "5m", "1h30m", "1.5s", "1ns"} {
				obj := &unstructured.Unstructured{Object: map[string]any{
					"apiVersion": api.GroupVersion.String(), "kind": kind,
					"metadata": map[string]any{testJSONNameField: fmt.Sprintf("interval-%d", i)},
					"spec":     map[string]any{"interval": interval},
				}}
				spec := obj.Object["spec"].(map[string]any)
				switch kind {
				case "Account":
					spec["apiToken"] = map[string]any{"secretRef": map[string]any{testJSONNameField: "token", "namespace": testDefaultNamespace}}
				case "Zone":
					spec[testJSONNameField] = regressionZoneName
				case "DNSRecord":
					spec[testJSONNameField] = regressionDNSName
					obj.SetNamespace(testDefaultNamespace)
				case "IP":
					spec["type"] = testIPTypeStatic
					spec["address"] = testIPv4Address
				}
				err := kube.Create(t.Context(), obj)
				if i < 5 {
					if !apierrors.IsInvalid(err) {
						t.Errorf("interval %q should be rejected, got %v", interval, err)
					}
				} else if err != nil {
					t.Errorf("valid interval %q rejected: %v", interval, err)
				}
			}
		})
	}
	t.Run("static IP deletion reaches finalizer", func(t *testing.T) {
		mgr, err := ctrl.NewManager(config, ctrl.Options{Scheme: scheme, Metrics: metricsserver.Options{BindAddress: "0"}, HealthProbeBindAddress: "0"})
		if err != nil {
			t.Fatal(err)
		}
		r := &IPReconciler{Client: mgr.GetClient(), RetryInterval: time.Second}
		if err := r.SetupWithManager(mgr); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		stopped := make(chan error, 1)
		go func() { stopped <- mgr.Start(ctx) }()
		t.Cleanup(func() {
			cancel()
			if err := <-stopped; err != nil {
				t.Error(err)
			}
		})
		if !mgr.GetCache().WaitForCacheSync(ctx) {
			t.Fatal("cache did not sync")
		}
		ip := &api.IP{ObjectMeta: metav1.ObjectMeta{Name: "finalizer-event"}, Spec: api.IPSpec{Type: testIPTypeStatic, Address: testIPv4Address}}
		if err := kube.Create(ctx, ip); err != nil {
			t.Fatal(err)
		}
		key := client.ObjectKeyFromObject(ip)
		deadline := time.Now().Add(10 * time.Second)
		for {
			if err := kube.Get(ctx, key, ip); err != nil {
				t.Fatal(err)
			}
			if ip.Status.Address == testIPv4Address {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("static IP did not become ready")
			}
			time.Sleep(20 * time.Millisecond)
		}
		if err := kube.Delete(ctx, ip); err != nil {
			t.Fatal(err)
		}
		for {
			err := kube.Get(ctx, key, ip)
			if apierrors.IsNotFound(err) {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			if time.Now().After(deadline) {
				t.Fatal("static IP remains stuck deleting")
			}
			time.Sleep(20 * time.Millisecond)
		}
	})
}
