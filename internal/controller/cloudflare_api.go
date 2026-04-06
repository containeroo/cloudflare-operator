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
	"errors"
	"fmt"

	"github.com/cloudflare/cloudflare-go"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	cloudflareoperatoriov1 "github.com/containeroo/cloudflare-operator/api/v1"
)

func cloudflareAPIFromAccount(ctx context.Context, kubeClient client.Client) (*cloudflare.API, error) {
	account := &cloudflareoperatoriov1.AccountList{}
	if err := kubeClient.List(ctx, account); err != nil {
		return nil, fmt.Errorf("failed to list accounts: %w", err)
	}

	switch len(account.Items) {
	case 0:
		return nil, errWaitForAccount
	case 1:
	default:
		return nil, errors.New("multiple Account resources found; exactly one is supported")
	}

	token, err := cloudflareTokenForAccount(ctx, kubeClient, &account.Items[0])
	if err != nil {
		return nil, err
	}

	cloudflareAPI, err := cloudflare.NewWithAPIToken(token)
	if err != nil {
		return nil, fmt.Errorf("failed to create Cloudflare API client for account %q: %w", account.Items[0].Name, err)
	}

	return cloudflareAPI, nil
}

func cloudflareTokenForAccount(ctx context.Context, kubeClient client.Client, account *cloudflareoperatoriov1.Account) (string, error) {
	secret := &corev1.Secret{}
	if err := kubeClient.Get(ctx, client.ObjectKey{
		Namespace: account.Spec.ApiToken.SecretRef.Namespace,
		Name:      account.Spec.ApiToken.SecretRef.Name,
	}, secret); err != nil {
		if apierrors.IsNotFound(err) {
			return "", fmt.Errorf("secret %s/%s for account %q not found", account.Spec.ApiToken.SecretRef.Namespace, account.Spec.ApiToken.SecretRef.Name, account.Name)
		}
		return "", fmt.Errorf("failed to get secret %s/%s for account %q: %w", account.Spec.ApiToken.SecretRef.Namespace, account.Spec.ApiToken.SecretRef.Name, account.Name, err)
	}

	token := string(secret.Data["apiToken"])
	if token == "" {
		return "", fmt.Errorf("secret %s/%s for account %q has no key named %q", secret.Namespace, secret.Name, account.Name, "apiToken")
	}

	return token, nil
}
