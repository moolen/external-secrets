/*
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
limitations under the License.
*/
package olm

import (
	"fmt"
	"os"
	"os/exec"

	// nolint
	. "github.com/onsi/ginkgo"
	// nolint
	. "github.com/onsi/gomega"

	"github.com/external-secrets/external-secrets/e2e/framework"
)

// This test just installs eso via generated olm bundle manifests
var _ = Describe("[olm] ", func() {
	f := framework.New("eso-olm")

	e2eVersion := os.Getenv("E2E_VERSION")
	if e2eVersion == "" {
		Fail("E2E_VERSION not defined")
	}
	It("should install eso via OLM", func() {
		args := []string{
			"run",
			"bundle",
			"-n",
			f.Namespace.Name,
			fmt.Sprintf("ghcr.io/external-secrets/external-secrets-e2e-bundle:bundle-%s", e2eVersion),
			"--verbose",
		}
		out, err := exec.Command("operator-sdk", args...).CombinedOutput()
		GinkgoWriter.Write(out)
		Expect(err).ToNot(HaveOccurred())
	})

})
