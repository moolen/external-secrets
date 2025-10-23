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

package server

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	k8sv2 "github.com/external-secrets/external-secrets/apis/provider/kubernetes/v2alpha1"
	pb "github.com/external-secrets/external-secrets/proto/provider"
)

// Server implements the SecretStoreProvider gRPC service for Kubernetes.
type Server struct {
	pb.UnimplementedSecretStoreProviderServer
	client     client.Client
	clientPool *KubernetesClientPool
}

// NewServer creates a new Kubernetes provider gRPC server.
func NewServer(kubeClient client.Client) *Server {
	return &Server{
		client:     kubeClient,
		clientPool: NewKubernetesClientPool(),
	}
}

// GetSecret retrieves a secret from a Kubernetes Secret.
func (s *Server) GetSecret(ctx context.Context, req *pb.GetSecretRequest) (*pb.GetSecretResponse, error) {
	// Parse provider config
	if req.ProviderConfig == nil {
		return nil, fmt.Errorf("provider config is required")
	}

	if req.RemoteRef == nil {
		return nil, fmt.Errorf("remote ref is required")
	}

	// Unmarshal into KubernetesSecretSpec (the controller sends only the Spec portion)
	var providerCfg k8sv2.KubernetesSecretSpec
	if err := json.Unmarshal(req.ProviderConfig, &providerCfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal provider config: %w", err)
	}

	// TODO: Implement proper authentication and client creation
	// For now, use the server's in-cluster client which has full access
	// When auth is implemented, this should create a scoped client based on providerCfg.Auth
	k8sClient := s.client

	// Parse the remote ref key
	// Format can be:
	//   1. "namespace/secret-name" - namespace and secret name
	//   2. "secret-name" - secret name only (uses RemoteNamespace from config)
	var namespace, secretName string
	secretKey := ""

	// Check if key contains namespace prefix
	if strings.Contains(req.RemoteRef.Key, "/") {
		parts := strings.SplitN(req.RemoteRef.Key, "/", 2)
		if len(parts) == 2 {
			namespace = parts[0]
			secretName = parts[1]
		} else {
			return nil, fmt.Errorf("invalid key format: %s", req.RemoteRef.Key)
		}
	} else {
		// No namespace in key, use RemoteNamespace from config
		secretName = req.RemoteRef.Key
		namespace = providerCfg.RemoteNamespace
		if namespace == "" {
			return nil, fmt.Errorf("remote namespace not specified in key or config")
		}
	}

	// If property is specified, use it as the key within the secret
	if req.RemoteRef.Property != "" {
		secretKey = req.RemoteRef.Property
	}

	// Fetch the Kubernetes Secret
	var secret corev1.Secret
	secretRef := types.NamespacedName{
		Name:      secretName,
		Namespace: namespace,
	}

	if err := k8sClient.Get(ctx, secretRef, &secret); err != nil {
		return nil, fmt.Errorf("failed to get Kubernetes secret %s/%s: %w", namespace, secretName, err)
	}

	// Extract secret data
	var secretData []byte
	if secretKey != "" {
		// Get specific key
		data, ok := secret.Data[secretKey]
		if !ok {
			return nil, fmt.Errorf("key %q not found in secret %s/%s", secretKey, namespace, secretName)
		}
		secretData = data
	} else {
		// Get all keys as JSON
		allData := make(map[string]string)
		for k, v := range secret.Data {
			allData[k] = string(v)
		}
		var err error
		secretData, err = json.Marshal(allData)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal secret data: %w", err)
		}
	}

	// Build response with metadata
	metadata := make(map[string]string)
	metadata["namespace"] = secret.Namespace
	metadata["name"] = secret.Name
	metadata["resourceVersion"] = secret.ResourceVersion
	if secret.CreationTimestamp.Time.String() != "" {
		metadata["created"] = secret.CreationTimestamp.Time.String()
	}
	for k, v := range secret.Labels {
		metadata["label."+k] = v
	}

	return &pb.GetSecretResponse{
		Value:    secretData,
		Metadata: metadata,
	}, nil
}

// Validate checks if the provider configuration is valid.
func (s *Server) Validate(ctx context.Context, req *pb.ValidateRequest) (*pb.ValidateResponse, error) {
	// Parse provider config
	if req.ProviderConfig == nil {
		return &pb.ValidateResponse{
			Valid: false,
			Error: "provider config is required",
		}, nil
	}

	// Unmarshal into KubernetesSecretSpec (the controller sends only the Spec portion)
	var providerCfg k8sv2.KubernetesSecretSpec
	if err := json.Unmarshal(req.ProviderConfig, &providerCfg); err != nil {
		return &pb.ValidateResponse{
			Valid: false,
			Error: fmt.Sprintf("failed to unmarshal provider config: %v", err),
		}, nil
	}

	// For now, just validate that the config is well-formed
	// Full authentication and API access validation will be implemented later
	// when authentication support is added to the client pool

	// Basic validation: check if server URL is present when specified
	if providerCfg.Server != nil && providerCfg.Server.URL != "" {
		// Validate URL format (basic check)
		if providerCfg.Server.URL == "" {
			return &pb.ValidateResponse{
				Valid: false,
				Error: "server URL cannot be empty when server is specified",
			}, nil
		}
	}

	// Validate that at least one auth method is specified if server is specified
	if providerCfg.Server != nil && providerCfg.Server.URL != "" {
		if providerCfg.Auth == nil {
			return &pb.ValidateResponse{
				Valid:    true,
				Warnings: []string{"no authentication configured - using default in-cluster credentials"},
			}, nil
		}
	}

	return &pb.ValidateResponse{
		Valid: true,
	}, nil
}

// Capabilities returns the provider's capabilities.
func (s *Server) Capabilities(ctx context.Context, req *pb.CapabilitiesRequest) (*pb.CapabilitiesResponse, error) {
	// Kubernetes provider supports both read and write operations
	return &pb.CapabilitiesResponse{
		Capabilities: pb.SecretStoreCapabilities_READ_WRITE,
	}, nil
}

// PushSecret writes a secret to a Kubernetes Secret.
func (s *Server) PushSecret(ctx context.Context, req *pb.PushSecretRequest) (*pb.PushSecretResponse, error) {
	// Parse provider config
	if req.ProviderConfig == nil {
		return nil, fmt.Errorf("provider config is required")
	}

	if req.PushSecretData == nil {
		return nil, fmt.Errorf("push secret data is required")
	}

	// Unmarshal into KubernetesSecretSpec (the controller sends only the Spec portion)
	var providerCfg k8sv2.KubernetesSecretSpec
	if err := json.Unmarshal(req.ProviderConfig, &providerCfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal provider config: %w", err)
	}

	// TODO: Implement proper authentication and client creation
	// For now, use the server's in-cluster client which has full access
	k8sClient := s.client

	// Parse remote ref to get namespace and name
	namespace, secretName, err := s.parseNamespaceAndName(req.PushSecretData.RemoteKey, providerCfg.RemoteNamespace)
	if err != nil {
		return nil, fmt.Errorf("failed to parse remote ref: %w", err)
	}

	// Get existing secret to check if it exists
	var existingSecret corev1.Secret
	secretRef := types.NamespacedName{
		Name:      secretName,
		Namespace: namespace,
	}
	err = k8sClient.Get(ctx, secretRef, &existingSecret)
	if err != nil && !apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("failed to get existing secret: %w", err)
	}

	// Prepare secret data
	var secretData map[string][]byte
	var secretLabels map[string]string
	var secretAnnotations map[string]string

	if !apierrors.IsNotFound(err) {
		// Use existing secret data as base
		secretData = make(map[string][]byte)
		for k, v := range existingSecret.Data {
			secretData[k] = v
		}
		secretLabels = existingSecret.Labels
		secretAnnotations = existingSecret.Annotations
	} else {
		// Create new secret data
		secretData = make(map[string][]byte)
		secretLabels = make(map[string]string)
		secretAnnotations = make(map[string]string)
	}

	// Handle metadata merge
	if len(req.PushSecretData.Metadata) > 0 {
		var metadata map[string]interface{}
		if err := json.Unmarshal(req.PushSecretData.Metadata, &metadata); err == nil {
			if labels, ok := metadata["labels"].(map[string]interface{}); ok {
				for k, v := range labels {
					if str, ok := v.(string); ok {
						secretLabels[k] = str
					}
				}
			}
			if annotations, ok := metadata["annotations"].(map[string]interface{}); ok {
				for k, v := range annotations {
					if str, ok := v.(string); ok {
						secretAnnotations[k] = str
					}
				}
			}
		}
	}

	// Handle the three cases for pushing secret data
	if req.PushSecretData.Property == "" && req.PushSecretData.SecretKey == "" {
		// Case 1: No property, no secretKey - push all data
		for k, v := range req.SecretData {
			secretData[k] = v
		}
	} else if req.PushSecretData.Property != "" && req.PushSecretData.SecretKey == "" {
		// Case 2: Property set, no secretKey - marshal all data to JSON, put in property
		jsonData, err := json.Marshal(req.SecretData)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal secret data to JSON: %w", err)
		}
		secretData[req.PushSecretData.Property] = jsonData
	} else if req.PushSecretData.Property != "" && req.PushSecretData.SecretKey != "" {
		// Case 3: Property and secretKey set - put specific key into property
		if value, exists := req.SecretData[req.PushSecretData.SecretKey]; exists {
			secretData[req.PushSecretData.Property] = value
		} else {
			return nil, fmt.Errorf("secret key %s not found in secret data", req.PushSecretData.SecretKey)
		}
	} else {
		return nil, fmt.Errorf("invalid combination: property=%s, secretKey=%s", req.PushSecretData.Property, req.PushSecretData.SecretKey)
	}

	// Create or update the secret
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:        secretName,
			Namespace:   namespace,
			Labels:      secretLabels,
			Annotations: secretAnnotations,
		},
		Data: secretData,
		Type: corev1.SecretTypeOpaque,
	}

	if !apierrors.IsNotFound(err) {
		// Update existing secret
		err = k8sClient.Update(ctx, secret)
		if err != nil {
			return nil, fmt.Errorf("failed to update secret: %w", err)
		}
	} else {
		// Create new secret
		err = k8sClient.Create(ctx, secret)
		if err != nil {
			return nil, fmt.Errorf("failed to create secret: %w", err)
		}
	}

	return &pb.PushSecretResponse{}, nil
}

// DeleteSecret deletes a secret from a Kubernetes Secret.
func (s *Server) DeleteSecret(ctx context.Context, req *pb.DeleteSecretRequest) (*pb.DeleteSecretResponse, error) {
	// Parse provider config
	if req.ProviderConfig == nil {
		return nil, fmt.Errorf("provider config is required")
	}

	if req.RemoteRef == nil {
		return nil, fmt.Errorf("remote ref is required")
	}

	// Unmarshal into KubernetesSecretSpec (the controller sends only the Spec portion)
	var providerCfg k8sv2.KubernetesSecretSpec
	if err := json.Unmarshal(req.ProviderConfig, &providerCfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal provider config: %w", err)
	}

	// TODO: Implement proper authentication and client creation
	// For now, use the server's in-cluster client which has full access
	k8sClient := s.client

	// Parse remote ref to get namespace and name
	namespace, secretName, err := s.parseNamespaceAndName(req.RemoteRef.RemoteKey, providerCfg.RemoteNamespace)
	if err != nil {
		return nil, fmt.Errorf("failed to parse remote ref: %w", err)
	}

	// Get existing secret to check if it exists
	var existingSecret corev1.Secret
	secretRef := types.NamespacedName{
		Name:      secretName,
		Namespace: namespace,
	}
	err = k8sClient.Get(ctx, secretRef, &existingSecret)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// Secret doesn't exist - return gracefully
			return &pb.DeleteSecretResponse{}, nil
		}
		return nil, fmt.Errorf("failed to get existing secret: %w", err)
	}

	// If property is specified, we need to delete only that property
	if req.RemoteRef.Property != "" {
		// Check if the property exists in the secret
		if _, exists := existingSecret.Data[req.RemoteRef.Property]; !exists {
			// Property doesn't exist - return gracefully
			return &pb.DeleteSecretResponse{}, nil
		}

		// If secret has more than one key, remove only the property
		if len(existingSecret.Data) > 1 {
			delete(existingSecret.Data, req.RemoteRef.Property)
			err = k8sClient.Update(ctx, &existingSecret)
			if err != nil {
				return nil, fmt.Errorf("failed to update secret after removing property: %w", err)
			}
			return &pb.DeleteSecretResponse{}, nil
		}
		// If secret has only one key, fall through to delete entire secret
	}

	// Delete the entire secret
	err = k8sClient.Delete(ctx, &existingSecret)
	if err != nil {
		return nil, fmt.Errorf("failed to delete secret: %w", err)
	}

	return &pb.DeleteSecretResponse{}, nil
}

// SecretExists checks if a secret exists in a Kubernetes Secret.
func (s *Server) SecretExists(ctx context.Context, req *pb.SecretExistsRequest) (*pb.SecretExistsResponse, error) {
	// Parse provider config
	if req.ProviderConfig == nil {
		return nil, fmt.Errorf("provider config is required")
	}

	if req.RemoteRef == nil {
		return nil, fmt.Errorf("remote ref is required")
	}

	// Unmarshal into KubernetesSecretSpec (the controller sends only the Spec portion)
	var providerCfg k8sv2.KubernetesSecretSpec
	if err := json.Unmarshal(req.ProviderConfig, &providerCfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal provider config: %w", err)
	}

	// TODO: Implement proper authentication and client creation
	// For now, use the server's in-cluster client which has full access
	k8sClient := s.client

	// Parse namespace and secret name from remote_ref.remote_key
	var namespace, secretName string
	if strings.Contains(req.RemoteRef.RemoteKey, "/") {
		parts := strings.SplitN(req.RemoteRef.RemoteKey, "/", 2)
		if len(parts) == 2 {
			namespace = parts[0]
			secretName = parts[1]
		} else {
			return nil, fmt.Errorf("invalid remote_key format: %s", req.RemoteRef.RemoteKey)
		}
	} else {
		secretName = req.RemoteRef.RemoteKey
		namespace = providerCfg.RemoteNamespace
		if namespace == "" {
			return nil, fmt.Errorf("remote namespace not specified in key or config")
		}
	}

	// Try to get the secret
	var secret corev1.Secret
	secretRef := types.NamespacedName{
		Name:      secretName,
		Namespace: namespace,
	}

	err := k8sClient.Get(ctx, secretRef, &secret)
	if err != nil {
		// If the error is NotFound, the secret doesn't exist - return false
		if apierrors.IsNotFound(err) {
			return &pb.SecretExistsResponse{
				Exists: false,
			}, nil
		}
		// Other errors should be returned
		return nil, fmt.Errorf("failed to check if secret exists %s/%s: %w", namespace, secretName, err)
	}

	// If we have a property specified, check if that key exists in the secret
	if req.RemoteRef.Property != "" {
		_, exists := secret.Data[req.RemoteRef.Property]
		return &pb.SecretExistsResponse{
			Exists: exists,
		}, nil
	}

	// Secret exists
	return &pb.SecretExistsResponse{
		Exists: true,
	}, nil
}

// GetAllSecrets retrieves multiple secrets from Kubernetes based on find criteria.
func (s *Server) GetAllSecrets(ctx context.Context, req *pb.GetAllSecretsRequest) (*pb.GetAllSecretsResponse, error) {
	// Parse provider config
	if req.ProviderConfig == nil {
		return nil, fmt.Errorf("provider config is required")
	}

	if req.Find == nil {
		return nil, fmt.Errorf("find criteria is required")
	}

	// Unmarshal into KubernetesSecretSpec (not the full Kubernetes CRD)
	// The controller sends only the Spec portion
	var providerCfg k8sv2.KubernetesSecretSpec
	if err := json.Unmarshal(req.ProviderConfig, &providerCfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal provider config: %w", err)
	}

	// TODO: Implement proper authentication and client creation
	// For now, use the server's in-cluster client which has full access
	k8sClient := s.client

	// Determine namespace to search in
	namespace := providerCfg.RemoteNamespace
	if req.Find.Path != "" {
		// If path is specified, use it as the namespace
		namespace = req.Find.Path
	}
	if namespace == "" {
		return nil, fmt.Errorf("namespace not specified in config or find path")
	}

	// List secrets based on criteria
	var secretList corev1.SecretList
	listOpts := &client.ListOptions{
		Namespace: namespace,
	}

	// If tags are specified, use them as label selector
	if len(req.Find.Tags) > 0 {
		selector := client.MatchingLabels(req.Find.Tags)
		selector.ApplyToList(listOpts)
	}

	if err := k8sClient.List(ctx, &secretList, listOpts); err != nil {
		return nil, fmt.Errorf("failed to list secrets in namespace %s: %w", namespace, err)
	}

	// Build result map
	result := make(map[string][]byte)

	// Filter by name if regexp is specified
	var nameFilter *regexp.Regexp
	if req.Find.Name != nil && req.Find.Name.Regexp != "" {
		var err error
		nameFilter, err = regexp.Compile(req.Find.Name.Regexp)
		if err != nil {
			return nil, fmt.Errorf("invalid name regexp %q: %w", req.Find.Name.Regexp, err)
		}
	}

	// Process each secret
	for _, secret := range secretList.Items {
		// Apply name filter if specified
		if nameFilter != nil && !nameFilter.MatchString(secret.Name) {
			continue
		}

		// Marshal the entire secret's data as JSON (following v1 provider pattern)
		secretData := make(map[string]string)
		for key, value := range secret.Data {
			secretData[key] = string(value)
		}

		jsonData, err := json.Marshal(secretData)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal secret %s data: %w", secret.Name, err)
		}

		// Use the secret name as the key
		result[secret.Name] = jsonData
	}

	return &pb.GetAllSecretsResponse{
		Secrets: result,
	}, nil
}

// parseNamespaceAndName parses a key that may contain namespace/name or just name
func (s *Server) parseNamespaceAndName(key, remoteNamespace string) (string, string, error) {
	if strings.Contains(key, "/") {
		parts := strings.SplitN(key, "/", 2)
		if len(parts) == 2 {
			return parts[0], parts[1], nil
		}
		return "", "", fmt.Errorf("invalid key format: %s", key)
	}
	if remoteNamespace == "" {
		return "", "", fmt.Errorf("remote namespace not specified")
	}
	return remoteNamespace, key, nil
}
