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

package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	v1 "github.com/external-secrets/external-secrets/apis/externalsecrets/v1"
	genv1alpha1 "github.com/external-secrets/external-secrets/apis/generators/v1alpha1"
	awsv2alpha1 "github.com/external-secrets/external-secrets/apis/provider/aws/v2alpha1"
	genpb "github.com/external-secrets/external-secrets/proto/generator"
	pb "github.com/external-secrets/external-secrets/proto/provider"
	"github.com/external-secrets/external-secrets/providers/v2/adapter"
	adaptergenerator "github.com/external-secrets/external-secrets/providers/v2/adapter/generator"
	adapterstore "github.com/external-secrets/external-secrets/providers/v2/adapter/store"
	"github.com/external-secrets/external-secrets/providers/v2/aws/generator"
	"github.com/external-secrets/external-secrets/providers/v2/aws/store"
	grpcserver "github.com/external-secrets/external-secrets/providers/v2/common/grpc/server"
)

var (
	port      = flag.Int("port", 8080, "The server port")
	enableTLS = flag.Bool("enable-tls", true, "Enable TLS/mTLS for gRPC server")
	verbose   = flag.Bool("verbose", false, "Enable verbose connection-level debugging")
)

func main() {
	flag.Parse()

	log.Printf("starting on port %d (TLS: %v, Verbose: %v)", *port, *enableTLS, *verbose)

	// Create Kubernetes client (required by adapter)
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = awsv2alpha1.AddToScheme(scheme)
	_ = genv1alpha1.AddToScheme(scheme)

	cfg, err := config.GetConfig()
	if err != nil {
		log.Fatalf("Failed to get kubeconfig: %v", err)
	}

	kubeClient, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		log.Fatalf("Failed to create Kubernetes client: %v", err)
	}

	// Setup v1 provider
	v1Provider := store.NewProvider()
	providerMapping := adapterstore.ProviderMapping{
		schema.GroupVersionKind{
			Group:   awsv2alpha1.GroupVersion.Group,
			Version: awsv2alpha1.GroupVersion.Version,
			Kind:    awsv2alpha1.SecretsManagerKind,
		}: v1Provider,
	}

	specMapper := func(ref *pb.ProviderReference) (*v1.SecretStoreSpec, error) {
		if ref.Kind != awsv2alpha1.SecretsManagerKind {
			return nil, fmt.Errorf("unsupported provider kind: %s", ref.Kind)
		}
		var awsProvider awsv2alpha1.SecretsManager
		err := kubeClient.Get(context.Background(), client.ObjectKey{
			Namespace: ref.Namespace,
			Name:      ref.Name,
		}, &awsProvider)
		if err != nil {
			return nil, err
		}
		return &v1.SecretStoreSpec{
			Provider: &v1.SecretStoreProvider{
				AWS: &v1.AWSProvider{
					Service:           v1.AWSServiceSecretsManager,
					Auth:              awsProvider.Spec.Auth,
					Role:              awsProvider.Spec.Role,
					Region:            awsProvider.Spec.Region,
					AdditionalRoles:   awsProvider.Spec.AdditionalRoles,
					ExternalID:        awsProvider.Spec.ExternalID,
					SecretsManager:    awsProvider.Spec.SecretsManager,
					SessionTags:       awsProvider.Spec.SessionTags,
					TransitiveTagKeys: awsProvider.Spec.TransitiveTagKeys,
					Prefix:            awsProvider.Spec.Prefix,
				},
			},
		}, nil
	}

	// Setup v1 generators
	generatorMapping := adaptergenerator.GeneratorMapping{
		schema.GroupVersionKind{
			Group:   genv1alpha1.Group,
			Version: genv1alpha1.Version,
			Kind:    string(genv1alpha1.GeneratorKindECRAuthorizationToken),
		}: generator.NewECRGenerator(),
		schema.GroupVersionKind{
			Group:   genv1alpha1.Group,
			Version: genv1alpha1.Version,
			Kind:    string(genv1alpha1.GeneratorKindSTSSessionToken),
		}: generator.NewSTSGenerator(),
	}

	adapterServer := adapter.NewServer(kubeClient, scheme, providerMapping, specMapper, generatorMapping)

	log.Printf("[PROVIDER] Using v1 AWS provider with ECR and STS generators wrapped with v2 adapter")
	grpcServer, err := grpcserver.NewGRPCServer(grpcserver.ServerOptions{
		EnableTLS: *enableTLS,
		Verbose:   *verbose,
	})
	if err != nil {
		log.Fatalf("Failed to create gRPC server: %v", err)
	}

	// Register services
	pb.RegisterSecretStoreProviderServer(grpcServer, adapterServer)
	genpb.RegisterGeneratorProviderServer(grpcServer, adapterServer)

	// Register health service
	healthServer := health.NewServer()
	grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)

	// Register reflection service for debugging
	reflection.Register(grpcServer)

	// Start listening
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", *port))
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	// Handle graceful shutdown
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		sig := <-sigChan
		log.Printf("Received signal: %v, shutting down gracefully...", sig)
		grpcServer.GracefulStop()
	}()

	// Start serving
	log.Printf("AWS Provider listening on %s", lis.Addr().String())
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("Failed to serve: %v", err)
	}
}
