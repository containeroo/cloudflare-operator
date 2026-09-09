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
	cloudflareoperatoriov1 "github.com/containeroo/cloudflare-operator/api/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func newTestScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	utilruntime.Must(corev1.AddToScheme(s))
	utilruntime.Must(cloudflareoperatoriov1.AddToScheme(s))
	utilruntime.Must(networkingv1.AddToScheme(s))
	utilruntime.Must(gatewayv1.Install(s))
	return s
}

const (
	testRemoteZoneID           = "zone-id"
	testBoundRecordID          = "bound-id"
	testJSONNameField          = "name"
	testAlternateDNSRecordHost = "other.example.com"
	testIPTypeDynamic          = "dynamic"
	testIPTypeStatic           = "static"
	testAccountName            = "account"
	testContentAnnotation      = "cloudflare-operator.io/content"
	testDefaultNamespace       = "default"
	testDNSRecordHost          = "dnstest.containeroo-test.org"
	testIPv4Address            = "1.1.1.1"
	testAlternateIPv4Address   = "2.2.2.2"
	testRecordTypeTXT          = "TXT"
	testSecretName             = "secret"
	testWildcardHost           = "*.containeroo-test.org"
)

func newTestAccountObjects(token string) (*corev1.Secret, *cloudflareoperatoriov1.Account) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testSecretName,
			Namespace: testDefaultNamespace,
		},
		Data: map[string][]byte{
			"apiToken": []byte(token),
		},
	}

	account := &cloudflareoperatoriov1.Account{
		ObjectMeta: metav1.ObjectMeta{
			Name: testAccountName,
		},
		Spec: cloudflareoperatoriov1.AccountSpec{
			ApiToken: cloudflareoperatoriov1.AccountSpecApiToken{
				SecretRef: corev1.SecretReference{
					Name:      secret.Name,
					Namespace: secret.Namespace,
				},
			},
		},
	}

	return secret, account
}
