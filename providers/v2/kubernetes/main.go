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
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/tap"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	pb "github.com/external-secrets/external-secrets/proto/provider"
	"github.com/external-secrets/external-secrets/providers/v2/kubernetes/pkg/server"
	providertls "github.com/external-secrets/external-secrets/providers/v2/kubernetes/pkg/tls"
)

var (
	port      = flag.Int("port", 8080, "The server port")
	enableTLS = flag.Bool("enable-tls", true, "Enable TLS/mTLS for gRPC server")
	verbose   = flag.Bool("verbose", false, "Enable verbose connection-level debugging")
)

// connectionTapHandler logs every connection attempt with detailed TLS information
func connectionTapHandler(ctx context.Context, info *tap.Info) (context.Context, error) {
	log.Printf("[CONNECTION] New connection attempt: %+v", info)
	return ctx, nil
}

// logTLSConnectionInfo logs detailed TLS handshake information
func logTLSConnectionInfo(p *peer.Peer) {
	if p == nil {
		log.Printf("[TLS] No peer information available")
		return
	}

	log.Printf("[TLS] Connection from: %s", p.Addr.String())

	authInfo := p.AuthInfo
	if authInfo == nil {
		log.Printf("[TLS] WARNING: No auth info - connection may not be using TLS")
		return
	}

	tlsInfo, ok := authInfo.(credentials.TLSInfo)
	if !ok {
		log.Printf("[TLS] WARNING: Auth info is not TLS type: %T", authInfo)
		return
	}

	state := tlsInfo.State
	log.Printf("[TLS] Handshake complete: version=0x%04x (%s), cipher=0x%04x, resumed=%v",
		state.Version, tlsVersionName(state.Version), state.CipherSuite, state.DidResume)

	if state.ServerName != "" {
		log.Printf("[TLS] SNI server name: %s", state.ServerName)
	}

	// Log peer certificates
	if len(state.PeerCertificates) > 0 {
		log.Printf("[TLS] Peer presented %d certificate(s)", len(state.PeerCertificates))
		for i, cert := range state.PeerCertificates {
			log.Printf("[TLS]   Cert %d: Subject=%s, Issuer=%s, NotBefore=%s, NotAfter=%s",
				i, cert.Subject, cert.Issuer, cert.NotBefore, cert.NotAfter)
			if len(cert.DNSNames) > 0 {
				log.Printf("[TLS]   Cert %d: DNS names=%v", i, cert.DNSNames)
			}
		}
	} else {
		log.Printf("[TLS] WARNING: No peer certificates - mTLS may not be working")
	}

	// Log verified chains
	if len(state.VerifiedChains) > 0 {
		log.Printf("[TLS] Successfully verified %d certificate chain(s)", len(state.VerifiedChains))
	} else {
		log.Printf("[TLS] WARNING: No verified certificate chains")
	}
}

func tlsVersionName(version uint16) string {
	switch version {
	case tls.VersionTLS10:
		return "TLS 1.0"
	case tls.VersionTLS11:
		return "TLS 1.1"
	case tls.VersionTLS12:
		return "TLS 1.2"
	case tls.VersionTLS13:
		return "TLS 1.3"
	default:
		return "Unknown"
	}
}

// loggingUnaryInterceptor logs all RPC calls with connection details
func loggingUnaryInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	start := time.Now()

	// Get peer information
	p, ok := peer.FromContext(ctx)
	var peerAddr string
	if ok {
		peerAddr = p.Addr.String()
		if *verbose {
			logTLSConnectionInfo(p)
		}
	} else {
		peerAddr = "unknown"
	}

	log.Printf("[RPC] --> %s from %s", info.FullMethod, peerAddr)

	// Call the handler
	resp, err := handler(ctx, req)

	duration := time.Since(start)
	if err != nil {
		log.Printf("[RPC] <-- %s failed in %v: %v", info.FullMethod, duration, err)
	} else {
		log.Printf("[RPC] <-- %s succeeded in %v", info.FullMethod, duration)
	}

	return resp, err
}

func main() {
	flag.Parse()

	// Create Kubernetes client for in-cluster operations
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)

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

	log.Printf("Kubernetes Provider starting on port %d (TLS: %v, Verbose: %v)", *port, *enableTLS, *verbose)

	// Prepare gRPC server options
	var grpcOpts []grpc.ServerOption

	// Add keepalive parameters for better connection diagnostics
	grpcOpts = append(grpcOpts,
		grpc.KeepaliveParams(keepalive.ServerParameters{
			MaxConnectionIdle:     15 * time.Minute,
			MaxConnectionAge:      30 * time.Minute,
			MaxConnectionAgeGrace: 5 * time.Minute,
			Time:                  5 * time.Minute,
			Timeout:               1 * time.Minute,
		}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             1 * time.Minute,
			PermitWithoutStream: true,
		}),
	)

	if *verbose {
		log.Printf("[CONFIG] Keepalive configured: idle=15m, age=30m, time=5m, timeout=1m")
	}

	// Add connection tap handler for verbose mode
	if *verbose {
		grpcOpts = append(grpcOpts, grpc.InTapHandle(connectionTapHandler))
		log.Printf("[CONFIG] Connection tap handler enabled")
	}

	// Add RPC logging interceptor
	grpcOpts = append(grpcOpts, grpc.UnaryInterceptor(loggingUnaryInterceptor))
	log.Printf("[CONFIG] RPC logging interceptor enabled")

	// Configure TLS if enabled
	if *enableTLS {
		log.Printf("[TLS] Loading TLS configuration...")
		tlsConfig, err := providertls.LoadServerTLSConfig(providertls.DefaultServerConfig())
		if err != nil {
			log.Fatalf("[TLS] FATAL: Failed to load TLS config: %v", err)
		}

		// Log TLS configuration details
		log.Printf("[TLS] Configuration loaded successfully")
		log.Printf("[TLS]   Min TLS version: 0x%04x (%s)", tlsConfig.MinVersion, tlsVersionName(tlsConfig.MinVersion))
		log.Printf("[TLS]   Max TLS version: 0x%04x (%s)", tlsConfig.MaxVersion, tlsVersionName(tlsConfig.MaxVersion))
		log.Printf("[TLS]   Client auth required: %v", tlsConfig.ClientAuth == tls.RequireAndVerifyClientCert)

		if tlsConfig.ClientCAs != nil {
			subjects := tlsConfig.ClientCAs.Subjects()
			log.Printf("[TLS]   Client CA pool has %d certificate(s)", len(subjects))
			if *verbose {
				for i, subject := range subjects {
					log.Printf("[TLS]     CA %d: %s", i, string(subject))
				}
			}
		} else {
			log.Printf("[TLS]   WARNING: No client CA pool configured")
		}

		if len(tlsConfig.Certificates) > 0 {
			log.Printf("[TLS]   Server has %d certificate(s)", len(tlsConfig.Certificates))
			for i, cert := range tlsConfig.Certificates {
				if len(cert.Certificate) > 0 {
					log.Printf("[TLS]     Cert %d: raw certificate data present", i)
				}
			}
		} else {
			log.Printf("[TLS]   WARNING: No server certificates configured")
		}

		grpcOpts = append(grpcOpts, grpc.Creds(credentials.NewTLS(tlsConfig)))
		log.Printf("[TLS] mTLS enabled for provider server")
	} else {
		log.Printf("[SECURITY] WARNING: TLS DISABLED - NOT SUITABLE FOR PRODUCTION")
		log.Printf("[SECURITY] All traffic will be transmitted in PLAINTEXT")
	}

	// Create gRPC server
	grpcServer := grpc.NewServer(grpcOpts...)

	// Register our provider service
	providerServer := server.NewServer(kubeClient)
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
	log.Printf("Kubernetes Provider listening on %s", lis.Addr().String())
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("Failed to serve: %v", err)
	}
}
