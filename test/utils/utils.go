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
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/cloudflare/cloudflare-go"
	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
)

const (
	prometheusOperatorVersion = "v0.68.0"
	prometheusOperatorURL     = "https://github.com/prometheus-operator/prometheus-operator/" +
		"releases/download/%s/bundle.yaml"
)

func warnError(err error) {
	fmt.Fprintf(GinkgoWriter, "warning: %v\n", err) // nolint:errcheck
}

// InstallPrometheusOperator installs the prometheus Operator to be used to export the enabled metrics.
func InstallPrometheusOperator() error {
	url := fmt.Sprintf(prometheusOperatorURL, prometheusOperatorVersion)
	cmd := exec.Command("kubectl", "create", "-f", url)
	_, err := Run(cmd)
	return err
}

// Run executes the provided command within this context
func Run(cmd *exec.Cmd) ([]byte, error) {
	dir, _ := GetProjectDir()
	cmd.Dir = dir

	if err := os.Chdir(cmd.Dir); err != nil {
		fmt.Fprintf(GinkgoWriter, "chdir dir: %s\n", err) // nolint:errcheck
	}

	cmd.Env = append(os.Environ(), "GO111MODULE=on")
	command := strings.Join(cmd.Args, " ")
	fmt.Fprintf(GinkgoWriter, "running: %s\n", command) // nolint:errcheck
	output, err := cmd.CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("%s failed with error: (%v) %s", command, err, string(output))
	}

	return output, nil
}

// UninstallPrometheusOperator uninstalls the prometheus
func UninstallPrometheusOperator() {
	url := fmt.Sprintf(prometheusOperatorURL, prometheusOperatorVersion)
	cmd := exec.Command("kubectl", "delete", "-f", url)
	if _, err := Run(cmd); err != nil {
		warnError(err)
	}
}

// LoadImageToKindCluster loads a local docker image to the kind cluster
func LoadImageToKindClusterWithName(name string) error {
	cluster := "cloudflare-operator-test"
	if v, ok := os.LookupEnv("KIND_CLUSTER"); ok {
		cluster = v
	}
	kindOptions := []string{"load", "docker-image", name, "--name", cluster}
	cmd := exec.Command("kind", kindOptions...)
	_, err := Run(cmd)
	return err
}

// GetNonEmptyLines converts given command output string into individual objects
// according to line breakers, and ignores the empty elements in it.
func GetNonEmptyLines(output string) []string {
	var res []string
	for element := range strings.SplitSeq(output, "\n") {
		if element != "" {
			res = append(res, element)
		}
	}

	return res
}

// GetProjectDir will return the directory where the project is
func GetProjectDir() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return wd, err
	}
	wd = strings.ReplaceAll(wd, "/test/e2e", "")
	return wd, nil
}

func VerifyObjectReady(objType, objName string) error {
	cmd := exec.Command(
		"kubectl",
		"get",
		objType,
		objName,
		"-n",
		"cloudflare-operator-system",
		"-o",
		"jsonpath={.status.conditions[?(@.type=='Ready')].status}",
	)
	status, err := Run(cmd)
	if err != nil {
		return err
	}
	if string(status) != "True" {
		return fmt.Errorf("%s %s is not ready", objType, objName)
	}
	return nil
}

func VerifyDNSRecordContent(objName, expectedContent string) error {
	cmd := exec.Command(
		"kubectl",
		"get",
		"dnsrecord",
		objName,
		"-n",
		"cloudflare-operator-system",
		"-o",
		"jsonpath={.spec.content}",
	)
	ip, err := Run(cmd)
	if err != nil {
		return err
	}
	if string(ip) == expectedContent {
		return nil
	}

	cmd = exec.Command(
		"kubectl",
		"get",
		"dnsrecord",
		objName,
		"-n",
		"cloudflare-operator-system",
		"-o",
		"jsonpath={.spec.name}",
	)
	recordName, err := Run(cmd)
	if err != nil {
		return err
	}

	cmd = exec.Command(
		"kubectl",
		"get",
		"dnsrecord",
		objName,
		"-n",
		"cloudflare-operator-system",
		"-o",
		"jsonpath={.status.recordID}",
	)
	recordID, err := Run(cmd)
	if err != nil {
		return err
	}
	if string(recordID) == "" {
		return fmt.Errorf("dnsrecord has unexpected content: %s", ip)
	}

	api, err := cloudflare.NewWithAPIToken(os.Getenv("CF_API_TOKEN"))
	if err != nil {
		return fmt.Errorf("failed to create Cloudflare API client: %w", err)
	}

	zoneID, err := zoneIDForDNSRecordName(strings.TrimSpace(string(recordName)))
	if err != nil {
		return err
	}

	record, err := api.GetDNSRecord(
		context.Background(),
		cloudflare.ZoneIdentifier(zoneID),
		strings.TrimSpace(string(recordID)),
	)
	if err != nil {
		return fmt.Errorf("failed to get Cloudflare DNS record %s: %w", string(recordID), err)
	}
	if record.Content != expectedContent {
		return fmt.Errorf("dnsrecord has unexpected content: %s", record.Content)
	}
	return nil
}

func zoneIDForDNSRecordName(dnsRecordName string) (string, error) {
	if zoneID := strings.TrimSpace(os.Getenv("CF_ZONE_ID")); zoneID != "" {
		return zoneID, nil
	}

	cmd := exec.Command("kubectl", "get", "zones", "-o", "json")
	output, err := Run(cmd)
	if err != nil {
		return "", err
	}

	var zoneList struct {
		Items []struct {
			Spec struct {
				Name string `json:"name"`
			} `json:"spec"`
			Status struct {
				ID string `json:"id"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(output, &zoneList); err != nil {
		return "", fmt.Errorf("failed to parse zones: %w", err)
	}

	longestMatch := ""
	zoneID := ""
	for _, zone := range zoneList.Items {
		if dnsRecordName != zone.Spec.Name && !strings.HasSuffix(dnsRecordName, "."+zone.Spec.Name) {
			continue
		}
		if len(zone.Spec.Name) <= len(longestMatch) {
			continue
		}
		longestMatch = zone.Spec.Name
		zoneID = zone.Status.ID
	}

	if zoneID == "" {
		if longestMatch != "" {
			return "", fmt.Errorf("zone %q matched DNS record %q but has no status.id yet", longestMatch, dnsRecordName)
		}
		return "", fmt.Errorf("no Zone matched DNS record %q and CF_ZONE_ID is not set", dnsRecordName)
	}

	return zoneID, nil
}

func VerifyDNSRecordAbsent(objName string) error {
	cmd := exec.Command(
		"kubectl",
		"get",
		"dnsrecord",
		objName,
		"-n",
		"cloudflare-operator-system",
	)
	status, err := Run(cmd)
	if err != nil {
		if strings.Contains(string(status), "NotFound") {
			return nil
		}
		return err
	}
	return fmt.Errorf("dnsrecord %s still exists", objName)
}
