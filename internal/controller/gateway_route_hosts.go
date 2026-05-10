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

import gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

func gatewayHostnamesToHosts(hostnames []gatewayv1.Hostname) map[string]struct{} {
	hosts := make(map[string]struct{})
	for _, hostname := range hostnames {
		if hostname != "" {
			hosts[string(hostname)] = struct{}{}
		}
	}
	return hosts
}
