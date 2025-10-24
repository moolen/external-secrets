/*
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

package v2

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/external-secrets/external-secrets-e2e/framework"
	"github.com/external-secrets/external-secrets-e2e/framework/log"
	esv1 "github.com/external-secrets/external-secrets/apis/externalsecrets/v1"
	esv1alpha1 "github.com/external-secrets/external-secrets/apis/externalsecrets/v1alpha1"
)

var _ = Describe("[v2] PushSecret", Label("v2", "kubernetes"), func() {
	f := framework.New("eso-v2-push-secret")

	var (
		testNamespace *corev1.Namespace
	)

	BeforeEach(func() {
		testNamespace = SetupTestNamespace(f, "v2-push-secret-")
	})

	AfterEach(func() {
		// Cleanup namespace
		if testNamespace != nil {
			Expect(f.CRClient.Delete(context.Background(), testNamespace)).To(Succeed())
		}
	})

	It("should push secret to Kubernetes provider", func() {
		caBundle := GetClusterCABundle(f, testNamespace.Name)
		CreateKubernetesProvider(f, testNamespace.Name, "k8s-provider", testNamespace.Name, caBundle)
		CreateProviderConnection(f, testNamespace.Name, "test-secretstore", "k8s-provider", testNamespace.Name)
		WaitForProviderConnectionReady(f, testNamespace.Name, "test-secretstore", 30*time.Second)

		By("granting provider service account permission to create secrets in test namespace")
		// Create a Role that allows creating and updating secrets
		role := &rbacv1.Role{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "provider-secret-writer",
				Namespace: testNamespace.Name,
			},
			Rules: []rbacv1.PolicyRule{
				{
					APIGroups: []string{""},
					Resources: []string{"secrets"},
					Verbs:     []string{"get", "list", "watch", "create", "update", "patch", "delete"},
				},
			},
		}
		Expect(f.CRClient.Create(context.Background(), role)).To(Succeed())

		// Create a RoleBinding that grants the provider service account these permissions
		roleBinding := &rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "provider-secret-writer-binding",
				Namespace: testNamespace.Name,
			},
			Subjects: []rbacv1.Subject{
				{
					Kind:      "ServiceAccount",
					Name:      "external-secrets-provider-kubernetes",
					Namespace: "external-secrets-system",
				},
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: "rbac.authorization.k8s.io",
				Kind:     "Role",
				Name:     "provider-secret-writer",
			},
		}
		Expect(f.CRClient.Create(context.Background(), roleBinding)).To(Succeed())
		log.Logf("granted provider service account permissions to write secrets in %s", testNamespace.Name)

		By("creating source secret")
		sourceSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "source-secret",
				Namespace: testNamespace.Name,
			},
			Data: map[string][]byte{
				"username": []byte("admin"),
				"password": []byte("secret123"),
			},
		}
		Expect(f.CRClient.Create(context.Background(), sourceSecret)).To(Succeed())
		log.Logf("created source secret: %s/%s", testNamespace.Name, "source-secret")

		VerifyProviderConnectionCapabilities(f, testNamespace.Name, "test-secretstore", esv1.ProviderReadWrite)

		By("creating PushSecret")
		pushSecret := &esv1alpha1.PushSecret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pushsecret",
				Namespace: testNamespace.Name,
			},
			Spec: esv1alpha1.PushSecretSpec{
				RefreshInterval: &metav1.Duration{Duration: 10 * time.Second},
				SecretStoreRefs: []esv1alpha1.PushSecretStoreRef{
					{
						Name:       "test-secretstore",
						Kind:       "Provider",
						APIVersion: "external-secrets.io/v1",
					},
				},
				Selector: esv1alpha1.PushSecretSelector{
					Secret: &esv1alpha1.PushSecretSecret{
						Name: "source-secret",
					},
				},
				Data: []esv1alpha1.PushSecretData{
					{
						Match: esv1alpha1.PushSecretMatch{
							SecretKey: "username",
							RemoteRef: esv1alpha1.PushSecretRemoteRef{
								RemoteKey: "pushed-secret",
								Property:  "username",
							},
						},
					},
					{
						Match: esv1alpha1.PushSecretMatch{
							SecretKey: "password",
							RemoteRef: esv1alpha1.PushSecretRemoteRef{
								RemoteKey: "pushed-secret",
								Property:  "password",
							},
						},
					},
				},
			},
		}
		Expect(f.CRClient.Create(context.Background(), pushSecret)).To(Succeed())
		log.Logf("created PushSecret: %s/%s", testNamespace.Name, "test-pushsecret")

		By("verifying PushSecret is synced")
		Eventually(func() bool {
			var ps esv1alpha1.PushSecret
			err := f.CRClient.Get(context.Background(),
				types.NamespacedName{Name: "test-pushsecret", Namespace: testNamespace.Name},
				&ps)
			if err != nil {
				log.Logf("failed to get PushSecret: %v", err)
				return false
			}

			for _, condition := range ps.Status.Conditions {
				if condition.Type == esv1alpha1.PushSecretReady && condition.Status == corev1.ConditionTrue {
					log.Logf("PushSecret is ready with status: %s", condition.Reason)
					return true
				}
			}
			log.Logf("PushSecret not ready yet, conditions: %+v", ps.Status.Conditions)
			return false
		}, 60*time.Second, 2*time.Second).Should(BeTrue(), "PushSecret should become ready")

		By("verifying pushed secret exists in target namespace")
		var pushedSecret corev1.Secret
		Eventually(func() bool {
			err := f.CRClient.Get(context.Background(),
				types.NamespacedName{Name: "pushed-secret", Namespace: testNamespace.Name},
				&pushedSecret)
			if err != nil {
				log.Logf("pushed secret not found yet: %v", err)
				return false
			}
			return true
		}, 30*time.Second, 2*time.Second).Should(BeTrue(), "pushed secret should exist")

		By("verifying pushed secret has correct data")
		Expect(pushedSecret.Data).To(HaveKey("username"))
		Expect(pushedSecret.Data).To(HaveKey("password"))
		Expect(string(pushedSecret.Data["username"])).To(Equal("admin"))
		Expect(string(pushedSecret.Data["password"])).To(Equal("secret123"))
		log.Logf("successfully verified pushed secret data")
	})
})
