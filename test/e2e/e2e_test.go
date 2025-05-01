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

package e2e

import (
	"fmt"
	"os/exec"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/containeroo/cloudflare-operator/test/utils"
)

const namespace = "cloudflare-operator-system"

var _ = Describe("controller", Ordered, func() {
	BeforeAll(func() {
		By("installing prometheus operator")
		Expect(utils.InstallPrometheusOperator()).To(Succeed())

		By("creating manager namespace")
		cmd := exec.Command("kubectl", "create", "ns", namespace)
		_, _ = utils.Run(cmd)
	})

	AfterAll(func() {
		By("uninstalling the Prometheus manager bundle")
		utils.UninstallPrometheusOperator()

		By("removing all ingresses")
		cmd := exec.Command("kubectl", "delete", "ingresses", "--all", "--all-namespaces")
		_, _ = utils.Run(cmd)

		By("removing all dnsrecords")
		cmd = exec.Command("kubectl", "delete", "dnsrecords", "--all", "--all-namespaces")
		_, _ = utils.Run(cmd)

		By("removing all ips")
		cmd = exec.Command("kubectl", "delete", "ips", "--all", "--all-namespaces")
		_, _ = utils.Run(cmd)

		By("removing all zones")
		cmd = exec.Command("kubectl", "delete", "zones", "--all", "--all-namespaces")
		_, _ = utils.Run(cmd)

		By("removing all accounts")
		cmd = exec.Command("kubectl", "delete", "accounts", "--all", "--all-namespaces")
		_, _ = utils.Run(cmd)

		By("removing manager namespace")
		cmd = exec.Command("kubectl", "delete", "ns", namespace)
		_, _ = utils.Run(cmd)
	})

	Context("Operator", func() {
		It("should run successfully", func() {
			var controllerPodName string
			var err error

			// projectimage stores the name of the image used in the example
			projectimage := "containeroo/cloudflare-operator:test"

			By("building the manager(Operator) image")
			cmd := exec.Command("make", "docker-build", fmt.Sprintf("IMG=%s", projectimage))
			_, err = utils.Run(cmd)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())

			By("loading the the manager(Operator) image on Kind")
			err = utils.LoadImageToKindClusterWithName(projectimage)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())

			By("installing CRDs")
			cmd = exec.Command("make", "install")
			_, err = utils.Run(cmd)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())

			By("deploying the controller-manager")
			cmd = exec.Command("make", "deploy", fmt.Sprintf("IMG=%s", projectimage))
			_, err = utils.Run(cmd)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())

			By("validating that the controller-manager pod is running as expected")
			verifyControllerUp := func() error {
				// Get pod name

				cmd = exec.Command("kubectl", "get",
					"pods", "-l", "control-plane=controller-manager",
					"-o", "go-template={{ range .items }}"+
						"{{ if not .metadata.deletionTimestamp }}"+
						"{{ .metadata.name }}"+
						"{{ \"\\n\" }}{{ end }}{{ end }}",
					"-n", namespace,
				)

				podOutput, err := utils.Run(cmd)
				ExpectWithOffset(2, err).NotTo(HaveOccurred())
				podNames := utils.GetNonEmptyLines(string(podOutput))
				if len(podNames) != 1 {
					return fmt.Errorf("expect 1 controller pods running, but got %d", len(podNames))
				}
				controllerPodName = podNames[0]
				ExpectWithOffset(2, controllerPodName).Should(ContainSubstring("controller-manager"))

				// Validate pod status
				cmd = exec.Command("kubectl", "get",
					"pods", controllerPodName, "-o", "jsonpath={.status.phase}",
					"-n", namespace,
				)
				status, err := utils.Run(cmd)
				ExpectWithOffset(2, err).NotTo(HaveOccurred())
				if string(status) != "Running" {
					return fmt.Errorf("controller pod in %s status", status)
				}
				return nil
			}
			EventuallyWithOffset(1, verifyControllerUp, time.Minute, time.Second).Should(Succeed())
		})
	})

	It("should reconcile account", func() {
		command := "envsubst < config/samples/cloudflareoperatorio_v1_account.yaml | kubectl apply -f -"
		cmd := exec.Command("sh", "-c", command)
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())

		Eventually(utils.VerifyObjectReady, time.Minute, time.Second).
			WithArguments("account", "account-sample").Should(Succeed())
	})

	It("should reconcile zone", func() {
		cmd := exec.Command("kubectl", "apply", "-f", "config/samples/cloudflareoperatorio_v1_zone.yaml")
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())

		Eventually(utils.VerifyObjectReady, time.Minute, time.Second).WithArguments("zone", "zone-sample").Should(Succeed())
	})

	It("should reconcile ip", func() {
		cmd := exec.Command("kubectl", "apply", "-f", "config/samples/cloudflareoperatorio_v1_ip.yaml")
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())

		Eventually(utils.VerifyObjectReady, time.Minute, time.Second).
			WithArguments("ip", "ip-sample").Should(Succeed())
	})

	It("should reconcile dnsreocrd", func() {
		cmd := exec.Command("kubectl", "apply", "-f", "config/samples/cloudflareoperatorio_v1_dnsrecord.yaml")
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())

		Eventually(utils.VerifyObjectReady, time.Minute, time.Second).
			WithArguments("dnsrecord", "dnsrecord-sample").Should(Succeed())
	})

	It("should reconcile dnsrecord with ipRef", func() {
		Eventually(utils.VerifyDNSRecordContent, time.Minute, time.Second).
			WithArguments("dnsrecord-ip-ref-sample", "1.1.1.1").Should(Succeed())
	})

	It("should reconcile dnsrecord with ipRef when ip changes", func() {
		cmd := exec.Command("kubectl", "patch", "ip", "ip-sample", "--type=merge", "-p", `{"spec":{"address":"9.9.9.9"}}`)
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())

		Eventually(utils.VerifyDNSRecordContent, time.Minute, time.Second).
			WithArguments("dnsrecord-ip-ref-sample", "9.9.9.9").Should(Succeed())
	})

	It("should create dnsrecord from an ingress", func() {
		cmd := exec.Command("kubectl", "apply", "-f", "config/samples/networking_v1_ingress.yaml")
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())

		Eventually(utils.VerifyObjectReady, time.Minute, time.Second).
			WithArguments("dnsrecord", "ingress-containeroo-test-org").Should(Succeed())
	})

	It("should update dnsrecord when ingress annotations change", func() {
		cmd := exec.Command(
			"kubectl", "-n", namespace, "patch", "ingress", "ingress-sample",
			"--type=merge", "-p", `{"metadata":{"annotations":{"cloudflare-operator.io/content":"145.145.145.145"}}}`)
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())

		Eventually(utils.VerifyDNSRecordContent, time.Minute, time.Second).
			WithArguments("ingress-containeroo-test-org", "145.145.145.145").Should(Succeed())
	})

	It("should recreate dnsrecord when it gets deleted", func() {
		cmd := exec.Command("kubectl", "-n", namespace, "delete", "dnsrecord", "ingress-containeroo-test-org")
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())

		Eventually(utils.VerifyObjectReady, time.Minute, time.Second).
			WithArguments("dnsrecord", "ingress-containeroo-test-org").Should(Succeed())
	})

	It("should delete dnsrecord when ingress annotations are absent", func() {
		cmd := exec.Command(
			"kubectl", "-n", namespace, "patch", "ingress", "ingress-sample",
			"--type=json", "-p", `[{"op": "remove", "path": "/metadata/annotations"}]`)
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())

		Eventually(utils.VerifyDNSRecordAbsent, time.Minute, time.Second).
			WithArguments("ingress-containeroo-test-org").Should(Succeed())
	})
})
