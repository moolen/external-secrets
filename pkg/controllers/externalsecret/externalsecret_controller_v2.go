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

package externalsecret

import (
	"context"
	"encoding/json"
	"fmt"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	esv1 "github.com/external-secrets/external-secrets/apis/externalsecrets/v1"
	k8sv2alpha1 "github.com/external-secrets/external-secrets/apis/provider/kubernetes/v2alpha1"
	"github.com/external-secrets/external-secrets/providers/v2/common/grpc"
	"github.com/external-secrets/external-secrets/runtime/esutils"
)

// isV2SecretStore checks if the referenced SecretStore is a v2 API version.
// For PoC, we try to fetch as v2 Provider and check if it exists.
func (r *Reconciler) isV2SecretStore(ctx context.Context, storeRef esv1.SecretStoreRef, namespace string) bool {
	// For PoC: try to get it as a v2 Provider
	// In production, we might use annotations or a different approach
	var store esv1.Provider
	storeKey := types.NamespacedName{
		Name:      storeRef.Name,
		Namespace: namespace,
	}
	err := r.Client.Get(ctx, storeKey, &store)
	return err == nil
}

// getProviderSecretDataV2 fetches secret data using v2 Provider via gRPC.
func (r *Reconciler) getProviderSecretDataV2(ctx context.Context, externalSecret *esv1.ExternalSecret) (map[string][]byte, error) {
	storeRef := externalSecret.Spec.SecretStoreRef

	// Get the v2 Provider
	var store esv1.Provider
	storeKey := types.NamespacedName{
		Name:      storeRef.Name,
		Namespace: externalSecret.Namespace,
	}
	if err := r.Client.Get(ctx, storeKey, &store); err != nil {
		return nil, fmt.Errorf("failed to get Provider: %w", err)
	}

	// Fetch the provider-specific config
	providerConfig, err := r.fetchProviderConfigForExternalSecret(ctx, &store.Spec.Config.ProviderRef)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch provider config: %w", err)
	}

	// Serialize provider config to JSON
	providerConfigJSON, err := r.serializeProviderConfigForExternalSecret(providerConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize provider config: %w", err)
	}

	// Get provider address
	address := store.Spec.Config.Address
	if address == "" {
		return nil, fmt.Errorf("provider address is required in Provider")
	}

	// Load TLS configuration
	tlsConfig, err := grpc.LoadClientTLSConfig(ctx, r.Client, store.Spec.Config.ProviderRef.Kind, "external-secrets-system")
	if err != nil {
		return nil, fmt.Errorf("failed to load TLS config: %w", err)
	}

	// Create gRPC client with TLS
	grpcClient, err := grpc.NewClient(address, tlsConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create gRPC client: %w", err)
	}
	defer grpcClient.Close(ctx)

	// Fetch secrets
	providerData := make(map[string][]byte)

	// Handle spec.data[]
	for i, secretRef := range externalSecret.Spec.Data {
		secretData, err := r.fetchSecretV2(ctx, grpcClient, providerConfigJSON, secretRef.RemoteRef)
		if err != nil {
			return nil, fmt.Errorf("error processing spec.data[%d] (key: %s): %w", i, secretRef.RemoteRef.Key, err)
		}

		// Decode if needed
		secretData, err = esutils.Decode(secretRef.RemoteRef.DecodingStrategy, secretData)
		if err != nil {
			return nil, fmt.Errorf("error decoding spec.data[%d]: %w", i, err)
		}

		providerData[secretRef.SecretKey] = secretData
	}

	// Handle spec.dataFrom[]
	for i, remoteRef := range externalSecret.Spec.DataFrom {
		if remoteRef.Extract != nil {
			secretData, err := r.fetchSecretV2(ctx, grpcClient, providerConfigJSON, *remoteRef.Extract)
			if err != nil {
				return nil, fmt.Errorf("error processing spec.dataFrom[%d].extract: %w", i, err)
			}

			// Parse as JSON and extract all keys
			var secretMap map[string]interface{}
			if err := json.Unmarshal(secretData, &secretMap); err != nil {
				return nil, fmt.Errorf("error parsing JSON from spec.dataFrom[%d].extract: %w", i, err)
			}

			// Convert to byte map
			for k, v := range secretMap {
				switch val := v.(type) {
				case string:
					providerData[k] = []byte(val)
				default:
					// Marshal non-string values as JSON
					b, err := json.Marshal(val)
					if err != nil {
						return nil, fmt.Errorf("error marshaling value for key %s: %w", k, err)
					}
					providerData[k] = b
				}
			}
		} else if remoteRef.Find != nil {
			// Handle find operation
			secretMap, err := grpcClient.GetAllSecrets(ctx, *remoteRef.Find, providerConfigJSON)
			if err != nil {
				return nil, fmt.Errorf("error processing spec.dataFrom[%d].find: %w", i, err)
			}

			// Apply key rewriting if specified
			secretMap, err = esutils.RewriteMap(remoteRef.Rewrite, secretMap)
			if err != nil {
				return nil, fmt.Errorf("error rewriting keys from spec.dataFrom[%d].find: %w", i, err)
			}

			// Apply key conversion if no rewrite rules
			if len(remoteRef.Rewrite) == 0 {
				secretMap, err = esutils.ConvertKeys(remoteRef.Find.ConversionStrategy, secretMap)
				if err != nil {
					return nil, fmt.Errorf("error converting keys from spec.dataFrom[%d].find: %w", i, err)
				}
			}

			// Validate secret keys
			if err := esutils.ValidateKeys(r.Log, secretMap); err != nil {
				return nil, fmt.Errorf("invalid keys from spec.dataFrom[%d].find: %w", i, err)
			}

			// Decode secrets if needed
			secretMap, err = esutils.DecodeMap(remoteRef.Find.DecodingStrategy, secretMap)
			if err != nil {
				return nil, fmt.Errorf("error decoding spec.dataFrom[%d].find: %w", i, err)
			}

			// Merge all found secrets into providerData
			providerData = esutils.MergeByteMap(providerData, secretMap)
		} else {
			return nil, fmt.Errorf("spec.dataFrom[%d]: only 'extract' and 'find' are supported in v2 (generators not yet implemented)", i)
		}
	}

	return providerData, nil
}

// fetchSecretV2 fetches a single secret via gRPC.
func (r *Reconciler) fetchSecretV2(ctx context.Context, client interface {
	GetSecret(context.Context, esv1.ExternalSecretDataRemoteRef, []byte) ([]byte, error)
}, providerConfig []byte, ref esv1.ExternalSecretDataRemoteRef) ([]byte, error) {
	return client.GetSecret(ctx, ref, providerConfig)
}

// fetchProviderConfigForExternalSecret fetches the provider-specific configuration resource from the cluster.
// It uses the ProviderRef to dynamically fetch the appropriate resource.
func (r *Reconciler) fetchProviderConfigForExternalSecret(ctx context.Context, ref *esv1.ProviderReference) (interface{}, error) {
	// Build the namespaced name
	nn := client.ObjectKey{
		Namespace: ref.Namespace,
		Name:      ref.Name,
	}

	// Fetch the resource based on the provider kind
	switch ref.Kind {
	case "Kubernetes":
		var k8sConfig k8sv2alpha1.Kubernetes
		if err := r.Client.Get(ctx, nn, &k8sConfig); err != nil {
			return nil, fmt.Errorf("failed to get Kubernetes provider config %s/%s: %w", nn.Namespace, nn.Name, err)
		}
		// Return just the spec to avoid sending metadata
		return k8sConfig.Spec, nil
	default:
		return nil, fmt.Errorf("unsupported provider kind: %s", ref.Kind)
	}
}

// serializeProviderConfigForExternalSecret serializes the provider config to JSON for gRPC call.
func (r *Reconciler) serializeProviderConfigForExternalSecret(config interface{}) ([]byte, error) {
	return json.Marshal(config)
}
