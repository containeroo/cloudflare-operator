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

func cloudflareAPIFromZone(ctx context.Context, kubeClient client.Client, zone *cloudflareoperatoriov1.Zone) (*cloudflare.API, error) {
	return cloudflareAPIForAccountName(ctx, kubeClient, zone.Spec.AccountRef.Name)
}

func cloudflareAPIFromDNSRecord(ctx context.Context, kubeClient client.Client, dnsRecord *cloudflareoperatoriov1.DNSRecord, zone *cloudflareoperatoriov1.Zone) (*cloudflare.API, error) {
	accountName := dnsRecord.Spec.AccountRef.Name
	if zone != nil && zone.Spec.AccountRef.Name != "" {
		if accountName != "" && accountName != zone.Spec.AccountRef.Name {
			return nil, fmt.Errorf("DNSRecord %q references Account %q but Zone %q references Account %q", dnsRecord.Name, accountName, zone.Name, zone.Spec.AccountRef.Name)
		}
		accountName = zone.Spec.AccountRef.Name
	}

	return cloudflareAPIForAccountName(ctx, kubeClient, accountName)
}

func cloudflareAPIForAccountName(ctx context.Context, kubeClient client.Client, accountName string) (*cloudflare.API, error) {
	account, err := accountForName(ctx, kubeClient, accountName)
	if err != nil {
		return nil, err
	}

	token, err := cloudflareTokenForAccount(ctx, kubeClient, account)
	if err != nil {
		return nil, err
	}

	cloudflareAPI, err := cloudflare.NewWithAPIToken(token)
	if err != nil {
		return nil, fmt.Errorf("failed to create Cloudflare API client for account %q: %w", account.Name, err)
	}

	return cloudflareAPI, nil
}

func accountForName(ctx context.Context, kubeClient client.Client, accountName string) (*cloudflareoperatoriov1.Account, error) {
	if accountName != "" {
		account := &cloudflareoperatoriov1.Account{}
		if err := kubeClient.Get(ctx, client.ObjectKey{Name: accountName}, account); err != nil {
			if apierrors.IsNotFound(err) {
				return nil, fmt.Errorf("account %q not found", accountName)
			}
			return nil, fmt.Errorf("failed to get Account %q: %w", accountName, err)
		}
		return account, nil
	}

	account := &cloudflareoperatoriov1.AccountList{}
	if err := kubeClient.List(ctx, account); err != nil {
		return nil, fmt.Errorf("failed to list accounts: %w", err)
	}

	switch len(account.Items) {
	case 0:
		return nil, errWaitForAccount
	case 1:
		return &account.Items[0], nil
	default:
		return nil, errors.New("multiple Account resources found; specify spec.accountRef.name")
	}
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

func accountMatchesSecret(account *cloudflareoperatoriov1.Account, secret client.ObjectKey) bool {
	return account.Spec.ApiToken.SecretRef.Name == secret.Name &&
		account.Spec.ApiToken.SecretRef.Namespace == secret.Namespace
}
