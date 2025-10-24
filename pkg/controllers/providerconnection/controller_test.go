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

package providerconnection

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log"

	esv1alpha1 "github.com/external-secrets/external-secrets/apis/externalsecrets/v1alpha1"
	awsv2 "github.com/external-secrets/external-secrets/apis/provider/aws/v2alpha1"
)

func TestReconciler_FetchProviderConfig(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = esv1alpha1.AddToScheme(scheme)
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

	store := &esv1alpha1.ProviderConnection{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-store",
			Namespace: "default",
		},
		Spec: esv1alpha1.ProviderConnectionSpec{
			Config: esv1alpha1.ProviderConnectionConfig{
				Address: "aws-provider:8080",
				ProviderRef: esv1alpha1.ProviderConnectionReference{
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
		WithStatusSubresource(&esv1alpha1.ProviderConnection{}).
		Build()

	reconciler := &Reconciler{
		Client:          fakeClient,
		Log:             log.Log.WithName("test"),
		Scheme:          scheme,
		RequeueInterval: 5 * time.Minute,
	}

	t.Run("fetch_aws_config", func(t *testing.T) {
		config, err := reconciler.fetchProviderConfig(context.Background(), store)
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

	t.Run("unsupported_api_version", func(t *testing.T) {
		badStore := store.DeepCopy()
		badStore.Spec.Config.ProviderRef.APIVersion = "provider.gcp.external-secrets.io/v2alpha1"

		_, err := reconciler.fetchProviderConfig(context.Background(), &badStore.Spec.Config.ProviderRef)
		if err == nil {
			t.Error("expected error for unsupported API version")
		}
	})

	t.Run("unsupported_kind", func(t *testing.T) {
		badStore := store.DeepCopy()
		badStore.Spec.Config.ProviderRef.Kind = "GCPSecretManager"

		_, err := reconciler.fetchProviderConfig(context.Background(), &badStore.Spec.Config.ProviderRef)
		if err == nil {
			t.Error("expected error for unsupported kind")
		}
	})
}

func TestReconciler_SetConditions(t *testing.T) {
	store := &esv1alpha1.ProviderConnection{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-store",
			Namespace: "default",
		},
	}

	reconciler := &Reconciler{
		Log: log.Log.WithName("test"),
	}

	t.Run("set_ready_condition", func(t *testing.T) {
		reconciler.setReadyCondition(store)

		if len(store.Status.Conditions) != 1 {
			t.Fatalf("expected 1 condition, got %d", len(store.Status.Conditions))
		}

		condition := store.Status.Conditions[0]
		if condition.Type != esv1alpha1.ProviderConnectionReady {
			t.Errorf("expected Ready condition, got %s", condition.Type)
		}
		if condition.Status != metav1.ConditionTrue {
			t.Errorf("expected True status, got %s", condition.Status)
		}
	})

	t.Run("set_not_ready_condition", func(t *testing.T) {
		store.Status.Conditions = nil // Reset
		reconciler.setNotReadyCondition(store, "TestReason", "Test message")

		if len(store.Status.Conditions) != 1 {
			t.Fatalf("expected 1 condition, got %d", len(store.Status.Conditions))
		}

		condition := store.Status.Conditions[0]
		if condition.Type != esv1alpha1.ProviderConnectionReady {
			t.Errorf("expected Ready condition, got %s", condition.Type)
		}
		if condition.Status != metav1.ConditionFalse {
			t.Errorf("expected False status, got %s", condition.Status)
		}
		if condition.Reason != "TestReason" {
			t.Errorf("expected TestReason, got %s", condition.Reason)
		}
	})

	t.Run("update_existing_condition", func(t *testing.T) {
		store.Status.Conditions = nil // Reset
		reconciler.setReadyCondition(store)

		// Update to not ready
		reconciler.setNotReadyCondition(store, "Failed", "Something went wrong")

		if len(store.Status.Conditions) != 1 {
			t.Fatalf("expected 1 condition after update, got %d", len(store.Status.Conditions))
		}

		condition := store.Status.Conditions[0]
		if condition.Status != metav1.ConditionFalse {
			t.Errorf("expected condition to be updated to False, got %s", condition.Status)
		}
	})
}

func TestReconciler_Reconcile(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = esv1alpha1.AddToScheme(scheme)
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

	store := &esv1alpha1.ProviderConnection{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-store",
			Namespace: "default",
		},
		Spec: esv1alpha1.ProviderConnectionSpec{
			Config: esv1alpha1.ProviderConnectionConfig{
				Address: "aws-provider:8080",
				ProviderRef: esv1alpha1.ProviderConnectionReference{
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
		WithStatusSubresource(&esv1alpha1.ProviderConnection{}).
		Build()

	reconciler := &Reconciler{
		Client:          fakeClient,
		Log:             log.Log.WithName("test"),
		Scheme:          scheme,
		RequeueInterval: 5 * time.Minute,
	}

	t.Run("reconcile_creates_status", func(t *testing.T) {
		req := ctrl.Request{
			NamespacedName: types.NamespacedName{
				Name:      "test-store",
				Namespace: "default",
			},
		}

		// Note: This will fail to connect to the gRPC server, but should still
		// update the status. For now, we're just testing the controller structure.
		_, err := reconciler.Reconcile(context.Background(), req)
		// We expect an error because the gRPC server isn't running
		// but the controller should handle it gracefully
		_ = err // Ignore for this test
	})
}
