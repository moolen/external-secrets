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

// Package common provides the v2 provider interface for out-of-tree providers communicating via gRPC.
package common

import (
	"context"

	esv1 "github.com/external-secrets/external-secrets/apis/externalsecrets/v1"
	pb "github.com/external-secrets/external-secrets/proto/provider"
)

// Provider is the interface that v2 out-of-tree providers must satisfy.
// Unlike v1 providers which are compiled into ESO, v2 providers run as separate services
// and communicate with ESO via gRPC.
type Provider interface {
	// GetSecret retrieves a single secret from the provider.
	// If the secret doesn't exist, it should return an error.
	// The providerConfig is the JSON-serialized provider-specific configuration (e.g., Kubernetes CRD).
	GetSecret(ctx context.Context, ref esv1.ExternalSecretDataRemoteRef, providerConfig []byte) ([]byte, error)

	// GetAllSecrets retrieves multiple secrets based on find criteria.
	// Returns a map of secret names to their byte values.
	// The providerConfig is the JSON-serialized provider-specific configuration (e.g., Kubernetes CRD).
	GetAllSecrets(ctx context.Context, find esv1.ExternalSecretFind, providerConfig []byte) (map[string][]byte, error)

	// PushSecret writes a secret to the provider.
	// The secretData is the Kubernetes secret data to push, and pushSecretData contains the push configuration.
	// The providerConfig is the JSON-serialized provider-specific configuration (e.g., Kubernetes CRD).
	PushSecret(ctx context.Context, secretData map[string][]byte, pushSecretData *pb.PushSecretData, providerConfig []byte) error

	// DeleteSecret deletes a secret from the provider.
	// The providerConfig is the JSON-serialized provider-specific configuration (e.g., Kubernetes CRD).
	DeleteSecret(ctx context.Context, remoteRef *pb.PushSecretRemoteRef, providerConfig []byte) error

	// SecretExists checks if a secret exists in the provider.
	// The providerConfig is the JSON-serialized provider-specific configuration (e.g., Kubernetes CRD).
	SecretExists(ctx context.Context, remoteRef *pb.PushSecretRemoteRef, providerConfig []byte) (bool, error)

	// Validate checks if the provider is properly configured and can communicate with the backend.
	// This is called by the SecretStore controller during reconciliation.
	// The providerConfig is the JSON-serialized provider-specific configuration (e.g., Kubernetes CRD).
	Validate(ctx context.Context, providerConfig []byte) error

	// Capabilities returns what operations the provider supports (ReadOnly, WriteOnly, ReadWrite).
	// The providerConfig is the JSON-serialized provider-specific configuration (e.g., Kubernetes CRD).
	Capabilities(ctx context.Context, providerConfig []byte) (pb.SecretStoreCapabilities, error)

	// Close cleans up any resources held by the provider client.
	Close(ctx context.Context) error
}
