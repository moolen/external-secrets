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

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	awssm "github.com/aws/aws-sdk-go/service/secretsmanager"

	awsv2 "github.com/external-secrets/external-secrets/apis/provider/aws/v2alpha1"
)

// AWSClientPool manages a pool of AWS Secrets Manager clients.
// This prevents creating new sessions for every request.
type AWSClientPool struct {
	mu      sync.RWMutex
	clients map[string]*pooledAWSClient
	maxIdle time.Duration
}

// pooledAWSClient wraps an AWS client with pooling metadata.
type pooledAWSClient struct {
	session  *session.Session
	client   *awssm.SecretsManager
	created  time.Time
	lastUsed time.Time
	mu       sync.Mutex
}

// NewAWSClientPool creates a new AWS client pool.
func NewAWSClientPool() *AWSClientPool {
	pool := &AWSClientPool{
		clients: make(map[string]*pooledAWSClient),
		maxIdle: 5 * time.Minute,
	}

	// Start cleanup goroutine
	go pool.cleanup()

	return pool
}

// GetClient gets or creates an AWS Secrets Manager client for the given config.
func (p *AWSClientPool) GetClient(ctx context.Context, cfg *awsv2.AWSSecretsManager) (*awssm.SecretsManager, error) {
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

	// Create new session and client
	sess, err := createSession(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create AWS session: %w", err)
	}

	client := awssm.New(sess)

	pooled := &pooledAWSClient{
		session:  sess,
		client:   client,
		created:  time.Now(),
		lastUsed: time.Now(),
	}

	p.clients[key] = pooled

	return client, nil
}

// cleanup periodically removes idle clients.
func (p *AWSClientPool) cleanup() {
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
func (p *AWSClientPool) clientKey(cfg *awsv2.AWSSecretsManager) string {
	// For now, use region as key
	// In production, this should include auth details (without exposing secrets)
	return fmt.Sprintf("region:%s", cfg.Spec.Region)
}

// createSession creates an AWS session from the provider config.
func createSession(ctx context.Context, cfg *awsv2.AWSSecretsManager) (*session.Session, error) {
	awsCfg := &aws.Config{
		Region: aws.String(cfg.Spec.Region),
	}

	// If secret ref is provided, use environment credentials
	// TODO: In production, fetch from Kubernetes Secret
	if cfg.Spec.Auth.SecretRef != nil {
		creds := credentials.NewEnvCredentials()
		awsCfg.Credentials = creds
	}

	sess, err := session.NewSession(awsCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create AWS session: %w", err)
	}

	return sess, nil
}
