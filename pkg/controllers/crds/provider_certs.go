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

package crds

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

const (
	clientCertName = "client.crt"
	clientKeyName  = "client.key"
)

// ProviderCertConfig defines configuration for a single provider's certificates.
type ProviderCertConfig struct {
	Name        string // Provider name (e.g., "aws")
	Namespace   string // Namespace where provider is deployed
	SecretName  string // Secret name to store certificates
	ServiceName string // Service name for DNS SANs
}

// ProviderCertificates holds the generated certificates for a provider.
type ProviderCertificates struct {
	ServerCert []byte
	ServerKey  []byte
	ClientCert []byte
	ClientKey  []byte
}

// ReconcileProviderCert ensures provider certificates exist and are valid.
func (r *Reconciler) ReconcileProviderCert(ctx context.Context, config ProviderCertConfig) error {
	log := r.Log.WithValues("provider", config.Name, "namespace", config.Namespace)

	// Get or create the secret
	secretName := types.NamespacedName{
		Name:      config.SecretName,
		Namespace: config.Namespace,
	}

	var secret corev1.Secret
	err := r.Get(ctx, secretName, &secret)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get provider secret: %w", err)
		}

		// Secret doesn't exist, create it
		log.Info("creating provider certificate secret")
		secret = corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      config.SecretName,
				Namespace: config.Namespace,
				Labels: map[string]string{
					"app.kubernetes.io/managed-by": "external-secrets",
					"app.kubernetes.io/component":  "provider-certificates",
					"app.kubernetes.io/provider":   config.Name,
				},
			},
			Type: corev1.SecretTypeTLS,
		}
	}

	// Check if certificates need refresh
	needRefresh, err := r.needProviderCertRefresh(&secret, config)
	if err != nil {
		return fmt.Errorf("failed to check certificate validity: %w", err)
	}

	if needRefresh {
		log.Info("refreshing provider certificates")
		if err := r.refreshProviderCerts(&secret, config); err != nil {
			return fmt.Errorf("failed to refresh provider certificates: %w", err)
		}

		// Create or update the secret
		if secret.UID == "" {
			if err := r.Create(ctx, &secret); err != nil {
				return fmt.Errorf("failed to create provider secret: %w", err)
			}
			log.Info("created provider certificate secret")
		} else {
			if err := r.Update(ctx, &secret); err != nil {
				return fmt.Errorf("failed to update provider secret: %w", err)
			}
			log.Info("updated provider certificate secret")
		}
	}

	return nil
}

// needProviderCertRefresh checks if provider certificates need to be refreshed.
func (r *Reconciler) needProviderCertRefresh(secret *corev1.Secret, config ProviderCertConfig) (bool, error) {
	// If secret has no data, we need to generate certificates
	if secret.Data == nil {
		return true, nil
	}

	// Check if all required keys exist
	requiredKeys := []string{caCertName, caKeyName, certName, keyName, clientCertName, clientKeyName}
	for _, key := range requiredKeys {
		if _, ok := secret.Data[key]; !ok {
			return true, nil
		}
	}

	// Validate CA certificate
	if !r.validCACert(secret.Data[caCertName], secret.Data[caKeyName]) {
		return true, nil
	}

	// Validate server certificate
	dnsNames := r.getProviderDNSNames(config)
	if !r.validProviderCert(secret.Data[caCertName], secret.Data[certName], secret.Data[keyName], dnsNames) {
		return true, nil
	}

	// Validate client certificate
	if !r.validProviderClientCert(secret.Data[caCertName], secret.Data[clientCertName], secret.Data[clientKeyName]) {
		return true, nil
	}

	return false, nil
}

// refreshProviderCerts generates new certificates for the provider.
func (r *Reconciler) refreshProviderCerts(secret *corev1.Secret, config ProviderCertConfig) error {
	now := time.Now()
	begin := now.Add(-1 * time.Hour)
	end := now.Add(certValidityDuration)

	// Check if we need to generate a new CA or reuse existing
	var caArtifacts *KeyPairArtifacts
	var err error

	if secret.Data != nil && r.validCACert(secret.Data[caCertName], secret.Data[caKeyName]) {
		// Reuse existing CA
		caArtifacts, err = buildArtifactsFromSecret(secret)
		if err != nil {
			return fmt.Errorf("failed to load existing CA: %w", err)
		}
	} else {
		// Generate new CA
		caArtifacts, err = r.CreateCACert(begin, end)
		if err != nil {
			return fmt.Errorf("failed to create CA certificate: %w", err)
		}
	}

	// Generate server certificate
	serverCert, serverKey, err := r.createProviderServerCert(caArtifacts, config, begin, end)
	if err != nil {
		return fmt.Errorf("failed to create server certificate: %w", err)
	}

	// Generate client certificate
	clientCert, clientKey, err := r.createProviderClientCert(caArtifacts, begin, end)
	if err != nil {
		return fmt.Errorf("failed to create client certificate: %w", err)
	}

	// Populate secret
	if secret.Data == nil {
		secret.Data = make(map[string][]byte)
	}
	secret.Data[caCertName] = caArtifacts.CertPEM
	secret.Data[caKeyName] = caArtifacts.KeyPEM
	secret.Data[certName] = serverCert
	secret.Data[keyName] = serverKey
	secret.Data[clientCertName] = clientCert
	secret.Data[clientKeyName] = clientKey

	return nil
}

// createProviderServerCert generates a server certificate for the provider.
func (r *Reconciler) createProviderServerCert(
	caArtifacts *KeyPairArtifacts,
	config ProviderCertConfig,
	begin, end time.Time,
) ([]byte, []byte, error) {
	dnsNames := r.getProviderDNSNames(config)

	// Create certificate template
	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject: pkix.Name{
			CommonName:   fmt.Sprintf("%s-provider", config.Name),
			Organization: []string{r.CAOrganization},
		},
		DNSNames:    dnsNames,
		NotBefore:   begin,
		NotAfter:    end,
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	// Generate private key
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate private key: %w", err)
	}

	// Create certificate
	certDER, err := x509.CreateCertificate(rand.Reader, template, caArtifacts.Cert, &key.PublicKey, caArtifacts.Key)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create certificate: %w", err)
	}

	// Encode certificate
	certPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certDER,
	})

	// Encode private key
	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})

	return certPEM, keyPEM, nil
}

// createProviderClientCert generates a client certificate for the ESO controller.
func (r *Reconciler) createProviderClientCert(
	caArtifacts *KeyPairArtifacts,
	begin, end time.Time,
) ([]byte, []byte, error) {
	// Create certificate template
	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject: pkix.Name{
			CommonName:   "external-secrets-controller",
			Organization: []string{r.CAOrganization},
		},
		NotBefore:   begin,
		NotAfter:    end,
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}

	// Generate private key
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate private key: %w", err)
	}

	// Create certificate
	certDER, err := x509.CreateCertificate(rand.Reader, template, caArtifacts.Cert, &key.PublicKey, caArtifacts.Key)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create certificate: %w", err)
	}

	// Encode certificate
	certPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certDER,
	})

	// Encode private key
	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})

	return certPEM, keyPEM, nil
}

// getProviderDNSNames returns the DNS names for the provider service.
func (r *Reconciler) getProviderDNSNames(config ProviderCertConfig) []string {
	serviceName := config.ServiceName
	if serviceName == "" {
		serviceName = fmt.Sprintf("provider-%s", config.Name)
	}

	return []string{
		serviceName,
		fmt.Sprintf("%s.%s", serviceName, config.Namespace),
		fmt.Sprintf("%s.%s.svc", serviceName, config.Namespace),
		fmt.Sprintf("%s.%s.svc.cluster.local", serviceName, config.Namespace),
	}
}

// validProviderCert validates a provider server certificate.
func (r *Reconciler) validProviderCert(caCert, cert, key []byte, dnsNames []string) bool {
	if len(caCert) == 0 || len(cert) == 0 || len(key) == 0 {
		return false
	}

	// Parse CA certificate
	pool := x509.NewCertPool()
	caDer, _ := pem.Decode(caCert)
	if caDer == nil {
		return false
	}
	caCertParsed, err := x509.ParseCertificate(caDer.Bytes)
	if err != nil {
		return false
	}
	pool.AddCert(caCertParsed)

	// Parse server certificate
	certDer, _ := pem.Decode(cert)
	if certDer == nil {
		return false
	}
	certParsed, err := x509.ParseCertificate(certDer.Bytes)
	if err != nil {
		return false
	}

	// Verify certificate is signed by CA
	opts := x509.VerifyOptions{
		Roots:       pool,
		CurrentTime: lookaheadTime(),
		KeyUsages:   []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	if _, err := certParsed.Verify(opts); err != nil {
		return false
	}

	// Check DNS names are present
	for _, dnsName := range dnsNames {
		found := false
		for _, certDNS := range certParsed.DNSNames {
			if certDNS == dnsName {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	return true
}

// validProviderClientCert validates a provider client certificate.
func (r *Reconciler) validProviderClientCert(caCert, cert, key []byte) bool {
	if len(caCert) == 0 || len(cert) == 0 || len(key) == 0 {
		return false
	}

	// Parse CA certificate
	pool := x509.NewCertPool()
	caDer, _ := pem.Decode(caCert)
	if caDer == nil {
		return false
	}
	caCertParsed, err := x509.ParseCertificate(caDer.Bytes)
	if err != nil {
		return false
	}
	pool.AddCert(caCertParsed)

	// Parse client certificate
	certDer, _ := pem.Decode(cert)
	if certDer == nil {
		return false
	}
	certParsed, err := x509.ParseCertificate(certDer.Bytes)
	if err != nil {
		return false
	}

	// Verify certificate is signed by CA
	opts := x509.VerifyOptions{
		Roots:       pool,
		CurrentTime: lookaheadTime(),
		KeyUsages:   []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}

	if _, err := certParsed.Verify(opts); err != nil {
		return false
	}

	return true
}
