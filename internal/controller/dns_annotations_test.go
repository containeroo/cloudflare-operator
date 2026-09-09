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
	"testing"
	"time"

	. "github.com/onsi/gomega"
)

func TestParseDNSAnnotations(t *testing.T) {
	g := NewWithT(t)
	annotations := map[string]string{
		"cloudflare-operator.io/account-ref": testAccountName,
		testContentAnnotation:                testIPv4Address,
		"cloudflare-operator.io/ip-ref":      "ip",
		"cloudflare-operator.io/proxied":     "true",
		"cloudflare-operator.io/ttl":         "120", // Expecting to return 1 because proxied is true
		"cloudflare-operator.io/type":        "A",
		"cloudflare-operator.io/interval":    "10s",
	}

	parsedSpec := parseDNSAnnotations(annotations, 30*time.Second)

	g.Expect(parsedSpec.AccountRef).To(HaveField("Name", Equal(testAccountName)))
	g.Expect(parsedSpec).To(HaveField("Content", Equal(testIPv4Address)))
	g.Expect(parsedSpec.IPRef).To(HaveField("Name", Equal("ip")))
	g.Expect(parsedSpec).To(HaveField("Proxied", Equal(&[]bool{true}[0])))
	g.Expect(parsedSpec).To(HaveField("TTL", Equal(1)))
	g.Expect(parsedSpec).To(HaveField("Type", Equal("A")))
	g.Expect(parsedSpec.Interval.Duration).To(Equal(10 * time.Second))
}

func TestNonPositiveDNSAnnotationIntervalsUseDefault(t *testing.T) {
	for _, interval := range []string{"0s", "-1s", "not-a-duration"} {
		spec := parseDNSAnnotations(map[string]string{"cloudflare-operator.io/interval": interval}, time.Minute)
		if spec.Interval.Duration != time.Minute {
			t.Errorf("interval %q disabled polling: %v", interval, spec.Interval)
		}
	}
}
