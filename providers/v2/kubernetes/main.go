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
	k8sv2alpha1 "github.com/external-secrets/external-secrets/apis/provider/kubernetes/v2alpha1"
	pb "github.com/external-secrets/external-secrets/proto/provider"
	k8sv1 "github.com/external-secrets/external-secrets/providers/v1/kubernetes"
	adapterstore "github.com/external-secrets/external-secrets/providers/v2/adapter/store"
	grpcserver "github.com/external-secrets/external-secrets/providers/v2/common/grpc/server"
)

var (
	port      = flag.Int("port", 8080, "The server port")
	enableTLS = flag.Bool("enable-tls", true, "Enable TLS/mTLS for gRPC server")
	verbose   = flag.Bool("verbose", false, "Enable verbose connection-level debugging")
)

func main() {
	flag.Parse()

	log.Printf("Kubernetes Provider starting on port %d (TLS: %v, Verbose: %v)", *port, *enableTLS, *verbose)

	// Create Kubernetes client for in-cluster operations
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = k8sv2alpha1.AddToScheme(scheme)

	cfg, err := config.GetConfig()
	if err != nil {
		log.Fatalf("Failed to get kubeconfig: %v", err)
	}

	kubeClient, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		log.Fatalf("Failed to create Kubernetes client: %v", err)
	}

	// Create listener
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", *port))
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	// Create gRPC server with common configuration
	grpcServer, err := grpcserver.NewGRPCServer(grpcserver.ServerOptions{
		EnableTLS: *enableTLS,
		Verbose:   *verbose,
	})
	if err != nil {
		log.Fatalf("Failed to create gRPC server: %v", err)
	}

	// Register our provider service using the adapter
	// This wraps the v1 Kubernetes provider and exposes it as a v2 gRPC service
	v1Provider := k8sv1.NewProvider()
	adapterServer := adapterstore.NewServer(kubeClient, adapterstore.ProviderMapping{
		schema.GroupVersionKind{
			Group:   k8sv2alpha1.GroupVersion.Group,
			Version: k8sv2alpha1.GroupVersion.Version,
			Kind:    k8sv2alpha1.Kind,
		}: v1Provider,
	}, func(ref *pb.ProviderReference) (*v1.SecretStoreSpec, error) {
		var kubernetesProvider k8sv2alpha1.Kubernetes
		err := kubeClient.Get(context.Background(), client.ObjectKey{
			Namespace: ref.Namespace,
			Name:      ref.Name,
		}, &kubernetesProvider)
		if err != nil {
			return nil, err
		}
		return &v1.SecretStoreSpec{
			Provider: &v1.SecretStoreProvider{
				Kubernetes: &kubernetesProvider.Spec,
			},
		}, nil
	})
	pb.RegisterSecretStoreProviderServer(grpcServer, adapterServer)

	log.Printf("[PROVIDER] Using v1 Kubernetes provider wrapped with v2 adapter")

	// Register health check service
	healthServer := health.NewServer()
	grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)

	// Register reflection service for debugging
	reflection.Register(grpcServer)

	// Handle graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
		<-sigCh
		log.Println("Shutting down gracefully...")
		grpcServer.GracefulStop()
	}()

	// Start serving
	log.Printf("Kubernetes Provider listening on %s", lis.Addr().String())
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("Failed to serve: %v", err)
	}
}
