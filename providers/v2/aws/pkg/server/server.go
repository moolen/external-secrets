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

// Package server implements the gRPC server for the AWS provider.
package server

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go/aws"
	awssm "github.com/aws/aws-sdk-go/service/secretsmanager"
	"github.com/tidwall/gjson"

	awsv2 "github.com/external-secrets/external-secrets/apis/provider/aws/v2alpha1"
	pb "github.com/external-secrets/external-secrets/proto/provider"
)

// Server implements the SecretStoreProvider gRPC service for AWS.
type Server struct {
	pb.UnimplementedSecretStoreProviderServer
	clientPool *AWSClientPool
}

// NewServer creates a new AWS provider gRPC server.
func NewServer() *Server {
	return &Server{
		clientPool: NewAWSClientPool(),
	}
}

// GetSecret retrieves a secret from AWS Secrets Manager.
func (s *Server) GetSecret(ctx context.Context, req *pb.GetSecretRequest) (*pb.GetSecretResponse, error) {
	// Parse provider config from request
	if req.ProviderConfig == nil {
		return nil, fmt.Errorf("provider config is required")
	}

	if req.RemoteRef == nil {
		return nil, fmt.Errorf("remote ref is required")
	}

	var providerCfg awsv2.AWSSecretsManager
	if err := json.Unmarshal(req.ProviderConfig, &providerCfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal provider config: %w", err)
	}

	// Validate provider config
	if providerCfg.Spec.Region == "" {
		return nil, fmt.Errorf("region is required in provider config")
	}

	// Get AWS client from pool
	client, err := s.clientPool.GetClient(ctx, &providerCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to get AWS client: %w", err)
	}

	// Determine version
	version := "AWSCURRENT"
	if req.RemoteRef != nil && req.RemoteRef.Version != "" {
		version = req.RemoteRef.Version
	}

	// Build GetSecretValue request
	key := req.RemoteRef.Key
	input := &awssm.GetSecretValueInput{
		SecretId:     aws.String(key),
		VersionStage: aws.String(version),
	}

	// Call AWS Secrets Manager
	result, err := client.GetSecretValueWithContext(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to get secret from AWS: %w", err)
	}

	// Extract secret value
	var secretData []byte
	if result.SecretString != nil {
		// Handle property extraction if specified
		if req.RemoteRef.Property != "" {
			secretData, err = s.extractProperty(*result.SecretString, req.RemoteRef.Property)
			if err != nil {
				return nil, fmt.Errorf("failed to extract property: %w", err)
			}
		} else {
			secretData = []byte(*result.SecretString)
		}
	} else if result.SecretBinary != nil {
		if req.RemoteRef.Property != "" {
			return nil, fmt.Errorf("property extraction not supported for binary secrets")
		}
		secretData = result.SecretBinary
	} else {
		return nil, fmt.Errorf("secret contains neither string nor binary data")
	}

	// Build response with metadata
	metadata := make(map[string]string)
	if result.VersionId != nil {
		metadata["version"] = *result.VersionId
	}
	if result.CreatedDate != nil {
		metadata["created"] = result.CreatedDate.String()
	}
	if result.ARN != nil {
		metadata["arn"] = *result.ARN
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

	var providerCfg awsv2.AWSSecretsManager
	if err := json.Unmarshal(req.ProviderConfig, &providerCfg); err != nil {
		return &pb.ValidateResponse{
			Valid: false,
			Error: fmt.Sprintf("failed to unmarshal provider config: %v", err),
		}, nil
	}

	// Validate required fields
	if providerCfg.Spec.Region == "" {
		return &pb.ValidateResponse{
			Valid: false,
			Error: "region is required",
		}, nil
	}

	if providerCfg.Spec.Auth.SecretRef == nil {
		return &pb.ValidateResponse{
			Valid: false,
			Error: "auth.secretRef is required",
		}, nil
	}

	// Get AWS client from pool
	client, err := s.clientPool.GetClient(ctx, &providerCfg)
	if err != nil {
		return &pb.ValidateResponse{
			Valid: false,
			Error: fmt.Sprintf("failed to get AWS client: %v", err),
		}, nil
	}

	// Try a simple API call to validate
	_, err = client.ListSecretsWithContext(ctx, &awssm.ListSecretsInput{
		MaxResults: aws.Int64(1),
	})
	if err != nil {
		return &pb.ValidateResponse{
			Valid: false,
			Error: fmt.Sprintf("failed to validate AWS credentials: %v", err),
		}, nil
	}

	return &pb.ValidateResponse{
		Valid: true,
	}, nil
}

// extractProperty extracts a property from a JSON secret using gjson.
func (s *Server) extractProperty(secretString, property string) ([]byte, error) {
	val := gjson.Get(secretString, property)
	if !val.Exists() {
		return nil, fmt.Errorf("property %q does not exist in secret", property)
	}
	return []byte(val.String()), nil
}

// Capabilities returns the provider's capabilities.
func (s *Server) Capabilities(ctx context.Context, req *pb.CapabilitiesRequest) (*pb.CapabilitiesResponse, error) {
	// AWS Secrets Manager supports both read and write operations
	return &pb.CapabilitiesResponse{
		Capabilities: pb.SecretStoreCapabilities_READ_WRITE,
	}, nil
}

// PushSecret writes a secret to AWS Secrets Manager.
// TODO: Implement full PushSecret functionality
func (s *Server) PushSecret(ctx context.Context, req *pb.PushSecretRequest) (*pb.PushSecretResponse, error) {
	return nil, fmt.Errorf("PushSecret not yet implemented for AWS provider")
}

// DeleteSecret deletes a secret from AWS Secrets Manager.
// TODO: Implement full DeleteSecret functionality
func (s *Server) DeleteSecret(ctx context.Context, req *pb.DeleteSecretRequest) (*pb.DeleteSecretResponse, error) {
	return nil, fmt.Errorf("DeleteSecret not yet implemented for AWS provider")
}

// SecretExists checks if a secret exists in AWS Secrets Manager.
// TODO: Implement full SecretExists functionality
func (s *Server) SecretExists(ctx context.Context, req *pb.SecretExistsRequest) (*pb.SecretExistsResponse, error) {
	return nil, fmt.Errorf("SecretExists not yet implemented for AWS provider")
}

// GetAllSecrets retrieves multiple secrets from AWS Secrets Manager based on find criteria.
// TODO: Implement full GetAllSecrets functionality
func (s *Server) GetAllSecrets(ctx context.Context, req *pb.GetAllSecretsRequest) (*pb.GetAllSecretsResponse, error) {
	return nil, fmt.Errorf("GetAllSecrets not yet implemented for AWS provider")
}
