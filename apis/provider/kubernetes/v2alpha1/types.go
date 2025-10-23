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
package v2alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Kubernetes defines the configuration for the Kubernetes Secret provider.
// This provider fetches secrets from Kubernetes Secrets in the same cluster.
// It's primarily useful for testing and migration scenarios.
// +kubebuilder:object:root=true
// +kubebuilder:storageversion
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,categories={external-secrets}
type Kubernetes struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec KubernetesSecretSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true
// KubernetesList contains a list of Kubernetes resources.
type KubernetesList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Kubernetes `json:"items"`
}

// KubernetesSecretSpec defines the desired state of KubernetesSecret provider.
type KubernetesSecretSpec struct {
	// Server defines the Kubernetes API server configuration.
	// If not specified, uses in-cluster config.
	// +optional
	Server *KubernetesServer `json:"server,omitempty"`

	// Auth defines how to authenticate to the Kubernetes API server.
	// If not specified, uses the provider's service account.
	// +optional
	Auth *KubernetesAuth `json:"auth,omitempty"`

	// RemoteNamespace specifies the namespace where secrets are stored.
	// If not specified, uses the same namespace as the ExternalSecret.
	// +optional
	RemoteNamespace string `json:"remoteNamespace,omitempty"`
}

// KubernetesServer defines Kubernetes API server configuration.
type KubernetesServer struct {
	// URL is the Kubernetes API server URL.
	// If not specified, uses in-cluster config.
	// +optional
	URL string `json:"url,omitempty"`

	// CABundle is a base64-encoded CA certificate bundle for TLS.
	// +optional
	CABundle []byte `json:"caBundle,omitempty"`
}

// KubernetesAuth defines authentication configuration.
type KubernetesAuth struct {
	// ServiceAccount specifies the service account to use.
	// If not specified, uses the provider pod's service account.
	// +optional
	ServiceAccount *KubernetesServiceAccount `json:"serviceAccount,omitempty"`

	// Token is a reference to a bearer token secret.
	// +optional
	Token *KubernetesSecretRef `json:"token,omitempty"`

	// Cert is a reference to a client certificate secret.
	// +optional
	Cert *KubernetesCertAuth `json:"cert,omitempty"`
}

// KubernetesServiceAccount references a service account.
type KubernetesServiceAccount struct {
	// Name is the service account name.
	Name string `json:"name"`

	// Namespace is the service account namespace.
	// If not specified, uses the same namespace as the SecretStore.
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

// KubernetesSecretRef references a Kubernetes secret.
type KubernetesSecretRef struct {
	// Name is the secret name.
	Name string `json:"name"`

	// Namespace is the secret namespace.
	// If not specified, uses the same namespace as the SecretStore.
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// Key is the key within the secret.
	Key string `json:"key"`
}

// KubernetesCertAuth defines client certificate authentication.
type KubernetesCertAuth struct {
	// ClientCert is a reference to the client certificate.
	ClientCert KubernetesSecretRef `json:"clientCert"`

	// ClientKey is a reference to the client private key.
	ClientKey KubernetesSecretRef `json:"clientKey"`
}

func init() {
	SchemeBuilder.Register(&Kubernetes{}, &KubernetesList{})
}
