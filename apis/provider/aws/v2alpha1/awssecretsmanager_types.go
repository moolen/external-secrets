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

// AWSSecretsManagerSpec defines the desired state of AWSSecretsManager
type AWSSecretsManagerSpec struct {
	// Region specifies the AWS region to use.
	// +kubebuilder:validation:Required
	Region string `json:"region"`

	// Auth defines how to authenticate with AWS.
	// +kubebuilder:validation:Required
	Auth AWSAuthentication `json:"auth"`
}

// AWSAuthentication defines authentication methods for AWS.
type AWSAuthentication struct {
	// SecretRef holds reference to a secret that contains AWS credentials.
	// Expected keys: access-key-id, secret-access-key
	// +optional
	SecretRef *AWSAuthSecretRef `json:"secretRef,omitempty"`

	// TODO: Add IRSA, AssumeRole, etc. in production
}

// AWSAuthSecretRef holds a reference to a secret containing AWS credentials.
type AWSAuthSecretRef struct {
	// Name of the secret.
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Namespace of the secret. If empty, uses the same namespace as the AWSSecretsManager.
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// AccessKeyIDKey is the key in the secret that contains the AWS Access Key ID.
	// Defaults to "access-key-id".
	// +optional
	// +kubebuilder:default="access-key-id"
	AccessKeyIDKey string `json:"accessKeyIDKey,omitempty"`

	// SecretAccessKeyKey is the key in the secret that contains the AWS Secret Access Key.
	// Defaults to "secret-access-key".
	// +optional
	// +kubebuilder:default="secret-access-key"
	SecretAccessKeyKey string `json:"secretAccessKeyKey,omitempty"`
}

// AWSSecretsManagerStatus defines the observed state of AWSSecretsManager
type AWSSecretsManagerStatus struct {
	// Conditions represent the latest available observations of the resource's state.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,categories={externalsecrets},shortName=awssm
// +kubebuilder:printcolumn:name="Region",type=string,JSONPath=`.spec.region`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// AWSSecretsManager is the Schema for AWS Secrets Manager provider configuration
type AWSSecretsManager struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AWSSecretsManagerSpec   `json:"spec,omitempty"`
	Status AWSSecretsManagerStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AWSSecretsManagerList contains a list of AWSSecretsManager
type AWSSecretsManagerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AWSSecretsManager `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AWSSecretsManager{}, &AWSSecretsManagerList{})
}
