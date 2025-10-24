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
	"testing"

	awsv2 "github.com/external-secrets/external-secrets/apis/provider/aws/v2alpha1"
	pb "github.com/external-secrets/external-secrets/proto/provider"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestServer_Validate(t *testing.T) {
	server := NewServer()

	t.Run("missing_provider_config", func(t *testing.T) {
		resp, err := server.Validate(context.Background(), &pb.ValidateRequest{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.Valid {
			t.Error("expected validation to fail for missing config")
		}
	})

	t.Run("missing_region", func(t *testing.T) {
		cfg := &awsv2.AWSSecretsManager{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "provider.aws.external-secrets.io/v2alpha1",
				Kind:       "AWSSecretsManager",
			},
			Spec: awsv2.AWSSecretsManagerSpec{
				// Region missing
				Auth: awsv2.AWSAuthentication{
					SecretRef: &awsv2.AWSAuthSecretRef{
						Name: "aws-creds",
					},
				},
			},
		}

		cfgBytes, _ := json.Marshal(cfg)
		resp, err := server.Validate(context.Background(), &pb.ValidateRequest{
			ProviderConfig: cfgBytes,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.Valid {
			t.Error("expected validation to fail for missing region")
		}
	})

	t.Run("missing_auth", func(t *testing.T) {
		cfg := &awsv2.AWSSecretsManager{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "provider.aws.external-secrets.io/v2alpha1",
				Kind:       "AWSSecretsManager",
			},
			Spec: awsv2.AWSSecretsManagerSpec{
				Region: "us-east-1",
				// Auth missing
			},
		}

		cfgBytes, _ := json.Marshal(cfg)
		resp, err := server.Validate(context.Background(), &pb.ValidateRequest{
			ProviderConfig: cfgBytes,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.Valid {
			t.Error("expected validation to fail for missing auth")
		}
	})
}

func TestServer_GetSecret_Validation(t *testing.T) {
	server := NewServer()

	t.Run("missing_provider_config", func(t *testing.T) {
		_, err := server.GetSecret(context.Background(), &pb.GetSecretRequest{
			RemoteRef: &pb.ExternalSecretDataRemoteRef{
				Key: "test-key",
			},
		})
		if err == nil {
			t.Error("expected error for missing provider config")
		}
	})

	t.Run("missing_remote_ref", func(t *testing.T) {
		cfg := &awsv2.AWSSecretsManager{
			Spec: awsv2.AWSSecretsManagerSpec{
				Region: "us-east-1",
				Auth: awsv2.AWSAuthentication{
					SecretRef: &awsv2.AWSAuthSecretRef{
						Name: "aws-creds",
					},
				},
			},
		}

		cfgBytes, _ := json.Marshal(cfg)
		_, err := server.GetSecret(context.Background(), &pb.GetSecretRequest{
			ProviderConfig: cfgBytes,
			// RemoteRef missing
		})
		// This will fail in createSession or GetSecretValue, which is expected
		if err == nil {
			t.Error("expected error for operation without proper setup")
		}
	})
}
