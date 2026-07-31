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
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/cloudflare/cloudflare-go/v7/option"
	. "github.com/onsi/gomega"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestNewCloudflareClientUsesProductionEnvironment(t *testing.T) {
	g := NewWithT(t)

	httpClient := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		g.Expect(req.URL.Scheme).To(Equal("https"))
		g.Expect(req.URL.Host).To(Equal("api.cloudflare.com"))
		g.Expect(req.URL.Path).To(Equal("/client/v4/zones"))
		g.Expect(req.Header.Get("Authorization")).To(Equal("Bearer token"))

		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(`{
				"result": [{"id": "zone-id", "name": "example.com"}],
				"result_info": {"page": 1, "per_page": 50, "count": 1, "total_count": 1, "total_pages": 1},
				"success": true,
				"errors": [],
				"messages": []
			}`)),
			Request: req,
		}, nil
	})}

	client := newCloudflareClient("token", option.WithHTTPClient(httpClient))
	zoneID, err := cloudflareZoneIDByName(context.Background(), client, "example.com")

	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(zoneID).To(Equal("zone-id"))
}
