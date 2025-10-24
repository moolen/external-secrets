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
	"fmt"
	"sync"
	"time"

	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	k8sv2 "github.com/external-secrets/external-secrets/apis/provider/kubernetes/v2alpha1"
)

// KubernetesClientPool manages a pool of Kubernetes clients.
type KubernetesClientPool struct {
	mu      sync.RWMutex
	clients map[string]*pooledK8sClient
	maxIdle time.Duration
}

// pooledK8sClient wraps a Kubernetes client with pooling metadata.
type pooledK8sClient struct {
	client   client.Client
	created  time.Time
	lastUsed time.Time
	mu       sync.Mutex
}

// NewKubernetesClientPool creates a new Kubernetes client pool.
func NewKubernetesClientPool() *KubernetesClientPool {
	pool := &KubernetesClientPool{
		clients: make(map[string]*pooledK8sClient),
		maxIdle: 5 * time.Minute,
	}

	// Start cleanup goroutine
	go pool.cleanup()

	return pool
}

// GetClient gets or creates a Kubernetes client for the given config.
// If the provider has an in-cluster config, it uses the provided fallback client.
func (p *KubernetesClientPool) GetClient(ctx context.Context, cfg *k8sv2.Kubernetes, fallbackClient client.Client) (client.Client, error) {
	// If no explicit server URL, use the fallback client (in-cluster)
	if cfg.Spec.Server == nil || cfg.Spec.Server.URL == "" {
		return fallbackClient, nil
	}

	key := p.clientKey(cfg)

	p.mu.RLock()
	if pooled, exists := p.clients[key]; exists {
		pooled.mu.Lock()
		pooled.lastUsed = time.Now()
		client := pooled.client
		pooled.mu.Unlock()
		p.mu.RUnlock()
		return client, nil
	}
	p.mu.RUnlock()

	// Create new client
	p.mu.Lock()
	defer p.mu.Unlock()

	// Double-check after acquiring write lock
	if pooled, exists := p.clients[key]; exists {
		pooled.mu.Lock()
		pooled.lastUsed = time.Now()
		client := pooled.client
		pooled.mu.Unlock()
		return client, nil
	}

	// Create new Kubernetes client
	k8sClient, err := createKubernetesClient(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	pooled := &pooledK8sClient{
		client:   k8sClient,
		created:  time.Now(),
		lastUsed: time.Now(),
	}

	p.clients[key] = pooled

	return k8sClient, nil
}

// cleanup periodically removes idle clients.
func (p *KubernetesClientPool) cleanup() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		p.mu.Lock()

		now := time.Now()
		toRemove := make([]string, 0)

		for key, pooled := range p.clients {
			pooled.mu.Lock()
			if now.Sub(pooled.lastUsed) > p.maxIdle {
				toRemove = append(toRemove, key)
			}
			pooled.mu.Unlock()
		}

		for _, key := range toRemove {
			delete(p.clients, key)
		}

		p.mu.Unlock()
	}
}

// clientKey generates a unique key for caching clients.
func (p *KubernetesClientPool) clientKey(cfg *k8sv2.Kubernetes) string {
	if cfg.Spec.Server == nil {
		return "in-cluster"
	}
	return fmt.Sprintf("url:%s", cfg.Spec.Server.URL)
}

// createKubernetesClient creates a Kubernetes client from the provider config.
func createKubernetesClient(ctx context.Context, cfg *k8sv2.Kubernetes) (client.Client, error) {
	var config *rest.Config

	if cfg.Spec.Server != nil && cfg.Spec.Server.URL != "" {
		// Use explicit server URL
		config = &rest.Config{
			Host: cfg.Spec.Server.URL,
		}

		if len(cfg.Spec.Server.CABundle) > 0 {
			config.TLSClientConfig.CAData = cfg.Spec.Server.CABundle
		}
	} else {
		// Use in-cluster config
		var err error
		config, err = rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("failed to get in-cluster config: %w", err)
		}
	}

	// TODO: Handle authentication (service account, token, cert)
	// For now, use the default configuration

	// Create controller-runtime client
	ctrlClient, err := client.New(config, client.Options{})
	if err != nil {
		return nil, fmt.Errorf("failed to create controller-runtime client: %w", err)
	}

	return ctrlClient, nil
}
