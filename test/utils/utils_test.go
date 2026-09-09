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

package utils

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifyDNSRecordContentChecksCloudflare(t *testing.T) {
	// kubectl reports the desired content even while the remote record is stale.
	binDir := t.TempDir()
	kubectl := `#!/bin/sh
case "$*" in
 *".spec.content"*) printf '2.2.2.2' ;;
 *".spec.name"*) printf 'test.example.com' ;;
 *".status.recordID"*) printf 'record-id' ;;
 *) exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(binDir, "kubectl"), []byte(kubectl), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CF_ZONE_ID", "zone-id")
	t.Setenv("CF_API_TOKEN", "test-token")
	for _, tt := range []struct {
		name      string
		content   string
		wantError bool
	}{
		{name: "stale remote", content: "1.1.1.1", wantError: true},
		{name: "synced remote", content: "2.2.2.2"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/zones/zone-id/dns_records/record-id" {
					t.Errorf("unexpected request path: %s", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprintf(w, `{"success":true,"result":{"id":"record-id","content":%q}}`, tt.content)
			}))
			t.Cleanup(server.Close)
			t.Setenv("CLOUDFLARE_BASE_URL", server.URL)
			err := VerifyDNSRecordContent("test-record", "2.2.2.2")
			if tt.wantError {
				if err == nil || !strings.Contains(err.Error(), "unexpected content") {
					t.Fatalf("expected stale remote content error, got %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}
