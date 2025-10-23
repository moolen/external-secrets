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
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"

	pb "github.com/external-secrets/external-secrets/proto/provider"
	"github.com/external-secrets/external-secrets/providers/v2/aws/pkg/server"
	providertls "github.com/external-secrets/external-secrets/providers/v2/aws/pkg/tls"
)

var (
	port      = flag.Int("port", 8080, "The server port")
	enableTLS = flag.Bool("enable-tls", true, "Enable TLS/mTLS for gRPC server")
)

func main() {
	flag.Parse()

	// Create listener
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", *port))
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	log.Printf("AWS Provider starting on port %d (TLS: %v)", *port, *enableTLS)

	// Prepare gRPC server options
	var grpcOpts []grpc.ServerOption

	// Configure TLS if enabled
	if *enableTLS {
		tlsConfig, err := providertls.LoadServerTLSConfig(providertls.DefaultServerConfig())
		if err != nil {
			log.Fatalf("Failed to load TLS config: %v", err)
		}
		grpcOpts = append(grpcOpts, grpc.Creds(credentials.NewTLS(tlsConfig)))
		log.Printf("TLS/mTLS enabled for provider")
	} else {
		log.Printf("WARNING: TLS disabled - not suitable for production")
	}

	// Create gRPC server
	grpcServer := grpc.NewServer(grpcOpts...)

	// Register our provider service
	providerServer := server.NewServer()
	pb.RegisterSecretStoreProviderServer(grpcServer, providerServer)

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
	log.Printf("AWS Provider listening on %s", lis.Addr().String())
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("Failed to serve: %v", err)
	}
}
