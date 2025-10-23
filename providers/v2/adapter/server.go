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

package adapter

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	genpb "github.com/external-secrets/external-secrets/proto/generator"
	pb "github.com/external-secrets/external-secrets/proto/provider"
	"github.com/external-secrets/external-secrets/providers/v2/adapter/generator"
	"github.com/external-secrets/external-secrets/providers/v2/adapter/store"
)

// Server is a unified gRPC server that implements both SecretStoreProvider and GeneratorProvider.
// It embeds both the store and generator servers to provide a single implementation.
type Server struct {
	pb.UnimplementedSecretStoreProviderServer
	genpb.UnimplementedGeneratorProviderServer
	storeServer     *store.Server
	generatorServer *generator.Server
}

// NewServer creates a new unified adapter server that wraps v1 providers and generators.
// It combines both store and generator functionality into a single gRPC server.
func NewServer(
	kubeClient client.Client,
	scheme *runtime.Scheme,
	providerMapping store.ProviderMapping,
	specMapper store.SpecMapper,
	generatorMapping generator.GeneratorMapping,
) *Server {
	return &Server{
		storeServer:     store.NewServer(kubeClient, providerMapping, specMapper),
		generatorServer: generator.NewServer(kubeClient, scheme, generatorMapping),
	}
}

// Ensure Server implements both interfaces
var _ pb.SecretStoreProviderServer = (*Server)(nil)
var _ genpb.GeneratorProviderServer = (*Server)(nil)

// Store methods - delegated to store.Server

func (s *Server) GetSecret(ctx context.Context, req *pb.GetSecretRequest) (*pb.GetSecretResponse, error) {
	return s.storeServer.GetSecret(ctx, req)
}

func (s *Server) PushSecret(ctx context.Context, req *pb.PushSecretRequest) (*pb.PushSecretResponse, error) {
	return s.storeServer.PushSecret(ctx, req)
}

func (s *Server) DeleteSecret(ctx context.Context, req *pb.DeleteSecretRequest) (*pb.DeleteSecretResponse, error) {
	return s.storeServer.DeleteSecret(ctx, req)
}

func (s *Server) SecretExists(ctx context.Context, req *pb.SecretExistsRequest) (*pb.SecretExistsResponse, error) {
	return s.storeServer.SecretExists(ctx, req)
}

func (s *Server) GetAllSecrets(ctx context.Context, req *pb.GetAllSecretsRequest) (*pb.GetAllSecretsResponse, error) {
	return s.storeServer.GetAllSecrets(ctx, req)
}

func (s *Server) Validate(ctx context.Context, req *pb.ValidateRequest) (*pb.ValidateResponse, error) {
	return s.storeServer.Validate(ctx, req)
}

func (s *Server) Capabilities(ctx context.Context, req *pb.CapabilitiesRequest) (*pb.CapabilitiesResponse, error) {
	return s.storeServer.Capabilities(ctx, req)
}

// Generator methods - delegated to generator.Server

func (s *Server) Generate(ctx context.Context, req *genpb.GenerateRequest) (*genpb.GenerateResponse, error) {
	return s.generatorServer.Generate(ctx, req)
}

func (s *Server) Cleanup(ctx context.Context, req *genpb.CleanupRequest) (*genpb.CleanupResponse, error) {
	return s.generatorServer.Cleanup(ctx, req)
}
