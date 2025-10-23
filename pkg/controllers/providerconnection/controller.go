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

// Package Provider implements the controller for Provider resources.
package providerconnection

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"

	esv1 "github.com/external-secrets/external-secrets/apis/externalsecrets/v1"
	k8sv2alpha1 "github.com/external-secrets/external-secrets/apis/provider/kubernetes/v2alpha1"
	"github.com/external-secrets/external-secrets/providers/v2/common/grpc"
)

// Reconciler reconciles a Provider object.
type Reconciler struct {
	client.Client
	Log             logr.Logger
	Scheme          *runtime.Scheme
	RequeueInterval time.Duration
}

// Reconcile validates the Provider and updates its status.
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("Provider", req.NamespacedName)

	log.Info("reconciling Provider")

	var store esv1.Provider
	if err := r.Get(ctx, req.NamespacedName, &store); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		log.Error(err, "unable to get Provider")
		return ctrl.Result{}, err
	}

	// Validate provider config and get capabilities
	capabilities, err := r.validateStoreAndGetCapabilities(ctx, &store)
	if err != nil {
		log.Error(err, "validation failed")
		r.setNotReadyCondition(&store, "ValidationFailed", err.Error())
		if updateErr := r.Status().Update(ctx, &store); updateErr != nil {
			log.Error(updateErr, "failed to update status")
			return ctrl.Result{}, updateErr
		}
		// Requeue after interval to retry
		return ctrl.Result{RequeueAfter: r.RequeueInterval}, nil
	}

	// Set ready condition and capabilities
	r.setReadyCondition(&store)
	store.Status.Capabilities = capabilities
	if err := r.Status().Update(ctx, &store); err != nil {
		log.Error(err, "failed to update status")
		return ctrl.Result{}, err
	}

	log.Info("Provider is ready", "capabilities", capabilities)
	return ctrl.Result{RequeueAfter: r.RequeueInterval}, nil
}

// validateStoreAndGetCapabilities validates the Provider configuration and retrieves capabilities by:
// 1. Fetching the provider-specific config (e.g., Kubernetes CRD)
// 2. Creating a gRPC client to the provider
// 3. Calling Validate() on the provider with the config
// 4. Calling Capabilities() to get the provider's capabilities
func (r *Reconciler) validateStoreAndGetCapabilities(ctx context.Context, store *esv1.Provider) (esv1.ProviderCapabilities, error) {
	// Get provider address
	address := store.Spec.Config.Address
	if address == "" {
		return "", fmt.Errorf("provider address is required")
	}

	// Fetch the provider-specific config resource
	providerConfig, err := r.fetchProviderConfig(ctx, &store.Spec.Config.ProviderRef)
	if err != nil {
		return "", fmt.Errorf("failed to fetch provider config: %w", err)
	}

	// Serialize provider config to JSON
	providerConfigJSON, err := r.serializeProviderConfig(providerConfig)
	if err != nil {
		return "", fmt.Errorf("failed to serialize provider config: %w", err)
	}

	// Load TLS configuration
	tlsConfig, err := grpc.LoadClientTLSConfig(ctx, r.Client, store.Spec.Config.ProviderRef.Kind, "external-secrets-system")
	if err != nil {
		return "", fmt.Errorf("failed to load TLS config: %w", err)
	}

	// Create gRPC client with TLS
	client, err := grpc.NewClient(address, tlsConfig)
	if err != nil {
		return "", fmt.Errorf("failed to create gRPC client: %w", err)
	}
	defer client.Close(ctx)

	// Validate the provider configuration
	if err := client.Validate(ctx, providerConfigJSON); err != nil {
		r.Log.Error(err, "provider validation failed")
		return "", fmt.Errorf("provider validation failed: %w", err)
	}

	// Get provider capabilities
	caps, err := client.Capabilities(ctx, providerConfigJSON)
	if err != nil {
		r.Log.Error(err, "failed to get capabilities")
		// Don't fail validation if capabilities check fails, just log and default to ReadOnly
		return esv1.ProviderReadOnly, nil
	}

	// Map gRPC capabilities to our API type
	var capabilities esv1.ProviderCapabilities
	switch caps {
	case 0: // READ_ONLY
		capabilities = esv1.ProviderReadOnly
	case 1: // WRITE_ONLY
		capabilities = esv1.ProviderWriteOnly
	case 2: // READ_WRITE
		capabilities = esv1.ProviderReadWrite
	default:
		capabilities = esv1.ProviderReadOnly
	}

	return capabilities, nil
}

// fetchProviderConfig fetches the provider-specific configuration resource from the cluster.
// It uses the ProviderRef to dynamically fetch the appropriate resource.
func (r *Reconciler) fetchProviderConfig(ctx context.Context, ref *esv1.ProviderReference) (interface{}, error) {
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

// serializeProviderConfig serializes the provider config to JSON for gRPC call.
func (r *Reconciler) serializeProviderConfig(config interface{}) ([]byte, error) {
	return json.Marshal(config)
}

// getProviderType extracts the provider type from the ProviderReference.
// For example, "provider.aws.external-secrets.io/v2alpha1" + "AWSSecretsManager" -> "aws"
func (r *Reconciler) getProviderType(ref esv1.ProviderReference) string {
	// Simple heuristic: extract from Kind
	// AWSSecretsManager -> aws
	// GCPSecretManager -> gcp
	// Could be made more sophisticated
	switch ref.Kind {
	case "AWSSecretsManager":
		return "aws"
	case "GCPSecretManager":
		return "gcp"
	case "AzureKeyVault":
		return "azure"
	default:
		// Fallback: lowercase the kind
		return ref.Kind
	}
}

// setReadyCondition sets the Ready condition to True.
func (r *Reconciler) setReadyCondition(store *esv1.Provider) {
	condition := esv1.ProviderCondition{
		Type:               esv1.ProviderReady,
		Status:             metav1.ConditionTrue,
		LastTransitionTime: metav1.Now(),
		Reason:             "Validated",
		Message:            "Provider is ready",
	}
	r.setCondition(store, condition)
}

// setNotReadyCondition sets the Ready condition to False.
func (r *Reconciler) setNotReadyCondition(store *esv1.Provider, reason, message string) {
	condition := esv1.ProviderCondition{
		Type:               esv1.ProviderReady,
		Status:             metav1.ConditionFalse,
		LastTransitionTime: metav1.Now(),
		Reason:             reason,
		Message:            message,
	}
	r.setCondition(store, condition)
}

// setCondition updates or adds a condition to the Provider status.
func (r *Reconciler) setCondition(store *esv1.Provider, newCondition esv1.ProviderCondition) {
	// Find existing condition
	for i, condition := range store.Status.Conditions {
		if condition.Type == newCondition.Type {
			// Only update if status changed
			if condition.Status != newCondition.Status {
				store.Status.Conditions[i] = newCondition
			}
			return
		}
	}
	// Add new condition
	store.Status.Conditions = append(store.Status.Conditions, newCondition)
}

// SetupWithManager sets up the controller with the Manager.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager, opts controller.Options) error {
	return ctrl.NewControllerManagedBy(mgr).
		WithOptions(opts).
		For(&esv1.Provider{}).
		Owns(&corev1.Secret{}). // Watch secrets that might be used for auth
		Complete(r)
}
