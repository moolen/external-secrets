/*
Copyright © 2025 ESO Maintainer Team

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    https://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package secretstore implements the controllers for managing SecretStore resources
package secretstore

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/go-logr/logr"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	esv1 "github.com/external-secrets/external-secrets/apis/externalsecrets/v1"
	pb "github.com/external-secrets/external-secrets/proto/provider"
	adapterstore "github.com/external-secrets/external-secrets/providers/v2/adapter/store"
	"github.com/external-secrets/external-secrets/providers/v2/common/grpc"
)

const (
	errGetClusterSecretStore = "could not get ClusterSecretStore %q, %w"
	errGetSecretStore        = "could not get SecretStore %q, %w"
	errSecretStoreNotReady   = "%s %q is not ready"
	errClusterStoreMismatch  = "using cluster store %q is not allowed from namespace %q: denied by spec.condition"
)

// Manager stores instances of provider clients
// At any given time we must have no more than one instance
// of a client (due to limitations in GCP / see mutexlock there)
// If the controller requests another instance of a given client
// we will close the old client first and then construct a new one.
type Manager struct {
	log             logr.Logger
	client          client.Client
	controllerClass string
	enableFloodgate bool

	// store clients by provider type
	clientMap map[clientKey]*clientVal
}

type clientKey struct {
	providerType string
	// For v2 providers, store the provider name and namespace
	v2ProviderName      string
	v2ProviderNamespace string
}

type clientVal struct {
	client esv1.SecretsClient
	store  esv1.GenericStore
	// For v2 providers, store the generation for cache invalidation
	v2ProviderGeneration int64
}

// NewManager constructs a new manager with defaults.
func NewManager(ctrlClient client.Client, controllerClass string, enableFloodgate bool) *Manager {
	log := ctrl.Log.WithName("clientmanager")
	return &Manager{
		log:             log,
		client:          ctrlClient,
		controllerClass: controllerClass,
		enableFloodgate: enableFloodgate,
		clientMap:       make(map[clientKey]*clientVal),
	}
}

// GetFromStore returns a provider client from the given store.
// Do not close the client returned from this func, instead close
// the manager once you're done with reconciling the external secret.
func (m *Manager) GetFromStore(ctx context.Context, store esv1.GenericStore, namespace string) (esv1.SecretsClient, error) {
	storeProvider, err := esv1.GetProvider(store)
	if err != nil {
		return nil, err
	}
	secretClient := m.getStoredClient(ctx, storeProvider, store)
	if secretClient != nil {
		return secretClient, nil
	}
	m.log.V(1).Info("creating new client",
		"provider", fmt.Sprintf("%T", storeProvider),
		"store", fmt.Sprintf("%s/%s", store.GetNamespace(), store.GetName()))
	// secret client is created only if we are going to refresh
	// this skip an unnecessary check/request in the case we are not going to do anything
	secretClient, err = storeProvider.NewClient(ctx, store, m.client, namespace)
	if err != nil {
		return nil, err
	}
	idx := storeKey(storeProvider)
	m.clientMap[idx] = &clientVal{
		client: secretClient,
		store:  store,
	}
	return secretClient, nil
}

// Get returns a provider client from the given storeRef or sourceRef.secretStoreRef
// while sourceRef.SecretStoreRef takes precedence over storeRef.
// Do not close the client returned from this func, instead close
// the manager once you're done with recinciling the external secret.
func (m *Manager) Get(ctx context.Context, storeRef esv1.SecretStoreRef, namespace string, sourceRef *esv1.StoreGeneratorSourceRef) (esv1.SecretsClient, error) {
	if storeRef.Kind == "Provider" {
		return m.getV2ProviderClient(ctx, storeRef.Name, namespace)
	}
	if sourceRef != nil && sourceRef.SecretStoreRef != nil {
		storeRef = *sourceRef.SecretStoreRef
	}
	store, err := m.getStore(ctx, &storeRef, namespace)
	if err != nil {
		return nil, err
	}
	// check if store should be handled by this controller instance
	if !ShouldProcessStore(store, m.controllerClass) {
		return nil, errors.New("can not reference unmanaged store")
	}
	// when using ClusterSecretStore, validate the ClusterSecretStore namespace conditions
	shouldProcess, err := m.shouldProcessSecret(store, namespace)
	if err != nil {
		return nil, err
	}
	if !shouldProcess {
		return nil, fmt.Errorf(errClusterStoreMismatch, store.GetName(), namespace)
	}

	if m.enableFloodgate {
		err := assertStoreIsUsable(store)
		if err != nil {
			return nil, err
		}
	}
	return m.GetFromStore(ctx, store, namespace)
}

// getV2ProviderClient creates or retrieves a cached gRPC client for a v2 Provider.
func (m *Manager) getV2ProviderClient(ctx context.Context, providerName, namespace string) (esv1.SecretsClient, error) {
	// Fetch the Provider resource
	var provider esv1.Provider
	providerKey := types.NamespacedName{
		Name:      providerName,
		Namespace: namespace,
	}
	if err := m.client.Get(ctx, providerKey, &provider); err != nil {
		return nil, fmt.Errorf("failed to get Provider %q: %w", providerName, err)
	}

	// Create cache key
	cacheKey := clientKey{
		providerType:        "v2-provider",
		v2ProviderName:      providerName,
		v2ProviderNamespace: namespace,
	}

	// Check if we have a cached client
	if cached, ok := m.clientMap[cacheKey]; ok {
		// Validate cache is still valid (check generation)
		if cached.v2ProviderGeneration == provider.Generation {
			m.log.V(1).Info("reusing cached v2 provider client",
				"provider", providerName,
				"namespace", namespace,
				"generation", provider.Generation)
			return cached.client, nil
		}
		// Cache is stale, clean up old client
		m.log.V(1).Info("cleaning up stale v2 provider client",
			"provider", providerName,
			"namespace", namespace,
			"oldGeneration", cached.v2ProviderGeneration,
			"newGeneration", provider.Generation)
		_ = cached.client.Close(ctx)
		delete(m.clientMap, cacheKey)
	}

	m.log.V(1).Info("creating new v2 provider client",
		"provider", providerName,
		"namespace", namespace,
		"address", provider.Spec.Config.Address)

	// Get provider address
	address := provider.Spec.Config.Address
	if address == "" {
		return nil, fmt.Errorf("provider address is required in Provider %q", providerName)
	}

	// Load TLS configuration
	// TODO: use namespace of controller
	tlsConfig, err := grpc.LoadClientTLSConfig(ctx, m.client, provider.Spec.Config.Address, "external-secrets-system")
	if err != nil {
		return nil, fmt.Errorf("failed to load TLS config for Provider %q: %w", providerName, err)
	}

	// Create gRPC client
	grpcClient, err := grpc.NewClient(address, tlsConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create gRPC client for Provider %q: %w", providerName, err)
	}

	// Convert ProviderReference to protobuf format
	providerRef := &pb.ProviderReference{
		ApiVersion: provider.Spec.Config.ProviderRef.APIVersion,
		Kind:       provider.Spec.Config.ProviderRef.Kind,
		Name:       provider.Spec.Config.ProviderRef.Name,
		Namespace:  provider.Spec.Config.ProviderRef.Namespace,
	}

	// Wrap with V2ClientWrapper
	wrappedClient := adapterstore.NewClient(grpcClient, providerRef, namespace)

	// Cache the client
	m.clientMap[cacheKey] = &clientVal{
		client:               wrappedClient,
		store:                nil, // v2 providers don't use GenericStore
		v2ProviderGeneration: provider.Generation,
	}

	m.log.Info("v2 provider client created and cached",
		"provider", providerName,
		"namespace", namespace,
		"address", address)

	return wrappedClient, nil
}

// returns a previously stored client from the cache if store and store-version match
// if a client exists for the same provider which points to a different store or store version
// it will be cleaned up.
func (m *Manager) getStoredClient(ctx context.Context, storeProvider esv1.ProviderInterface, store esv1.GenericStore) esv1.SecretsClient {
	idx := storeKey(storeProvider)
	val, ok := m.clientMap[idx]
	if !ok {
		return nil
	}
	valGVK, err := m.client.GroupVersionKindFor(val.store)
	if err != nil {
		return nil
	}
	storeGVK, err := m.client.GroupVersionKindFor(store)
	if err != nil {
		return nil
	}
	storeName := fmt.Sprintf("%s/%s", store.GetNamespace(), store.GetName())
	// return client if it points to the very same store
	if val.store.GetObjectMeta().Generation == store.GetGeneration() &&
		valGVK == storeGVK &&
		val.store.GetName() == store.GetName() &&
		val.store.GetNamespace() == store.GetNamespace() {
		m.log.V(1).Info("reusing stored client",
			"provider", fmt.Sprintf("%T", storeProvider),
			"store", storeName)
		return val.client
	}
	m.log.V(1).Info("cleaning up client",
		"provider", fmt.Sprintf("%T", storeProvider),
		"store", storeName)
	// if we have a client, but it points to a different store
	// we must clean it up
	_ = val.client.Close(ctx)
	delete(m.clientMap, idx)
	return nil
}

func storeKey(storeProvider esv1.ProviderInterface) clientKey {
	return clientKey{
		providerType: fmt.Sprintf("%T", storeProvider),
	}
}

// getStore fetches the (Cluster)SecretStore from the kube-apiserver
// and returns a GenericStore representing it.
func (m *Manager) getStore(ctx context.Context, storeRef *esv1.SecretStoreRef, namespace string) (esv1.GenericStore, error) {
	ref := types.NamespacedName{
		Name: storeRef.Name,
	}
	if storeRef.Kind == esv1.ClusterSecretStoreKind {
		var store esv1.ClusterSecretStore
		err := m.client.Get(ctx, ref, &store)
		if err != nil {
			return nil, fmt.Errorf(errGetClusterSecretStore, ref.Name, err)
		}
		return &store, nil
	}
	ref.Namespace = namespace
	var store esv1.SecretStore
	err := m.client.Get(ctx, ref, &store)
	if err != nil {
		return nil, fmt.Errorf(errGetSecretStore, ref.Name, err)
	}
	return &store, nil
}

// Close cleans up all clients.
func (m *Manager) Close(ctx context.Context) error {
	var errs []string
	for key, val := range m.clientMap {
		err := val.client.Close(ctx)
		if err != nil {
			errs = append(errs, err.Error())
		}
		delete(m.clientMap, key)
	}
	if len(errs) != 0 {
		return fmt.Errorf("errors while closing clients: %s", strings.Join(errs, ", "))
	}
	return nil
}

func (m *Manager) shouldProcessSecret(store esv1.GenericStore, ns string) (bool, error) {
	if store.GetKind() != esv1.ClusterSecretStoreKind {
		return true, nil
	}

	if len(store.GetSpec().Conditions) == 0 {
		return true, nil
	}

	namespace := v1.Namespace{}
	if err := m.client.Get(context.Background(), client.ObjectKey{Name: ns}, &namespace); err != nil {
		return false, fmt.Errorf("failed to get a namespace %q: %w", ns, err)
	}

	nsLabels := labels.Set(namespace.GetLabels())
	for _, condition := range store.GetSpec().Conditions {
		var labelSelectors []*metav1.LabelSelector
		if condition.NamespaceSelector != nil {
			labelSelectors = append(labelSelectors, condition.NamespaceSelector)
		}
		for _, n := range condition.Namespaces {
			labelSelectors = append(labelSelectors, &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"kubernetes.io/metadata.name": n,
				},
			})
		}

		for _, ls := range labelSelectors {
			selector, err := metav1.LabelSelectorAsSelector(ls)
			if err != nil {
				return false, fmt.Errorf("failed to convert label selector into selector %v: %w", ls, err)
			}
			if selector.Matches(nsLabels) {
				return true, nil
			}
		}

		for _, reg := range condition.NamespaceRegexes {
			match, err := regexp.MatchString(reg, ns)
			if err != nil {
				// Should not happen since store validation already verified the regexes.
				return false, fmt.Errorf("failed to compile regex %v: %w", reg, err)
			}

			if match {
				return true, nil
			}
		}
	}

	return false, nil
}

// assertStoreIsUsable assert that the store is ready to use.
func assertStoreIsUsable(store esv1.GenericStore) error {
	if store == nil {
		return nil
	}
	condition := GetSecretStoreCondition(store.GetStatus(), esv1.SecretStoreReady)
	if condition == nil || condition.Status != v1.ConditionTrue {
		return fmt.Errorf(errSecretStoreNotReady, store.GetKind(), store.GetName())
	}
	return nil
}
