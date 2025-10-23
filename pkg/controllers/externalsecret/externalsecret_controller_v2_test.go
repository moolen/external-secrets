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
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log"

	esv1 "github.com/external-secrets/external-secrets/apis/externalsecrets/v1"
	awsv2 "github.com/external-secrets/external-secrets/apis/provider/aws/v2alpha1"
	ssv2 "github.com/external-secrets/external-secrets/apis/secretstore/v2alpha1"
)

func TestReconciler_IsV2SecretStore(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = esv1.AddToScheme(scheme)
	_ = ssv2.AddToScheme(scheme)
	_ = awsv2.AddToScheme(scheme)

	// Create v2 SecretStore
	v2Store := &ssv2.SecretStore{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "v2-store",
			Namespace: "default",
		},
		Spec: ssv2.SecretStoreSpec{
			ProviderConfig: ssv2.ProviderConfig{
				Address: "aws-provider:8080",
				ProviderRef: ssv2.ProviderReference{
					APIVersion: "provider.aws.external-secrets.io/v2alpha1",
					Kind:       "AWSSecretsManager",
					Name:       "aws-config",
				},
			},
		},
	}

	// Create v1 SecretStore (for comparison)
	v1Store := &esv1.SecretStore{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "v1-store",
			Namespace: "default",
		},
		Spec: esv1.SecretStoreSpec{
			Provider: &esv1.SecretStoreProvider{
				AWS: &esv1.AWSProvider{
					Service: esv1.AWSServiceSecretsManager,
					Region:  "us-east-1",
				},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(v2Store, v1Store).
		Build()

	reconciler := &Reconciler{
		Client: fakeClient,
		Log:    log.Log.WithName("test"),
		Scheme: scheme,
	}

	t.Run("detects_v2_store", func(t *testing.T) {
		ref := esv1.SecretStoreRef{
			Name: "v2-store",
			Kind: "SecretStore",
		}
		isV2 := reconciler.isV2SecretStore(context.Background(), ref, "default")
		if !isV2 {
			t.Error("expected v2 store to be detected as v2")
		}
	})

	t.Run("detects_v1_store", func(t *testing.T) {
		ref := esv1.SecretStoreRef{
			Name: "v1-store",
			Kind: "SecretStore",
		}
		isV2 := reconciler.isV2SecretStore(context.Background(), ref, "default")
		if isV2 {
			t.Error("expected v1 store to NOT be detected as v2")
		}
	})

	t.Run("nonexistent_store", func(t *testing.T) {
		ref := esv1.SecretStoreRef{
			Name: "nonexistent",
			Kind: "SecretStore",
		}
		isV2 := reconciler.isV2SecretStore(context.Background(), ref, "default")
		if isV2 {
			t.Error("expected nonexistent store to NOT be detected as v2")
		}
	})
}

func TestReconciler_FetchProviderConfigForStore(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = ssv2.AddToScheme(scheme)
	_ = awsv2.AddToScheme(scheme)

	awsConfig := &awsv2.AWSSecretsManager{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "aws-config",
			Namespace: "default",
		},
		Spec: awsv2.AWSSecretsManagerSpec{
			Region: "us-east-1",
			Auth: awsv2.AWSAuthentication{
				SecretRef: &awsv2.AWSAuthSecretRef{
					Name: "aws-creds",
				},
			},
		},
	}

	store := &ssv2.SecretStore{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-store",
			Namespace: "default",
		},
		Spec: ssv2.SecretStoreSpec{
			ProviderConfig: ssv2.ProviderConfig{
				Address: "aws-provider:8080",
				ProviderRef: ssv2.ProviderReference{
					APIVersion: "provider.aws.external-secrets.io/v2alpha1",
					Kind:       "AWSSecretsManager",
					Name:       "aws-config",
				},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(awsConfig, store).
		Build()

	reconciler := &Reconciler{
		Client: fakeClient,
		Log:    log.Log.WithName("test"),
		Scheme: scheme,
	}

	t.Run("fetch_aws_config", func(t *testing.T) {
		config, err := reconciler.fetchProviderConfigForStore(context.Background(), store)
		if err != nil {
			t.Fatalf("failed to fetch provider config: %v", err)
		}

		awsCfg, ok := config.(*awsv2.AWSSecretsManager)
		if !ok {
			t.Fatalf("expected AWSSecretsManager, got %T", config)
		}

		if awsCfg.Spec.Region != "us-east-1" {
			t.Errorf("expected region us-east-1, got %s", awsCfg.Spec.Region)
		}
	})

	t.Run("unsupported_provider", func(t *testing.T) {
		badStore := store.DeepCopy()
		badStore.Spec.ProviderConfig.ProviderRef.APIVersion = "provider.gcp.external-secrets.io/v2alpha1"

		_, err := reconciler.fetchProviderConfigForStore(context.Background(), badStore)
		if err == nil {
			t.Error("expected error for unsupported provider")
		}
	})
}
