# V2 Provider Migration Guide

This guide explains how to migrate providers from `providers/v1` to the new v2 architecture using the provider generator.

## Background

The v2 provider architecture introduces:
- **gRPC-based communication** between controller and providers
- **Separate container deployments** for each provider
- **Code generation** to eliminate boilerplate

Previously, each v2 provider required a `main.go` file with ~150 lines of mostly identical boilerplate code. With 40+ providers to migrate, maintaining these files would become messy as they diverge over time.

## Solution: Code Generation

We've implemented a code generator that:
1. Takes a simple YAML configuration file (`provider.yaml`)
2. Validates it against a JSON schema
3. Generates `main.go` and `Dockerfile` with all the boilerplate
4. Leaves provider-specific logic (spec mapping) in hand-written `config.go`

## Migration Steps

### 1. Understand Your Provider

Before migrating, identify:
- **Provider name**: e.g., `vault`, `gcp`, `azure`
- **Store implementations**: How many? What CRD kinds?
- **Generator implementations**: Does it have any? What kinds?
- **Special requirements**: Custom auth, multiple stores, etc.

### 2. Create Directory Structure

```bash
cd providers/v2
mkdir -p myprovider/{store,generator}  # adjust based on your needs
```

### 3. Create `provider.yaml`

This is the configuration file that drives code generation.

**Example for a simple provider (one store, no generators):**

```yaml
provider:
  name: myprovider
  displayName: "My Provider"
  v2Package: "github.com/external-secrets/external-secrets/apis/provider/myprovider/v2alpha1"

stores:
  - gvk:
      group: "provider.external-secrets.io"
      version: "v2alpha1"
      kind: "MyProvider"
    v1Provider: "github.com/external-secrets/external-secrets/providers/v1/myprovider"
    v1ProviderFunc: "NewProvider"

configPackage: "."
```

**Example for a complex provider (one store, multiple generators):**

See `providers/v2/aws/provider.yaml` as a reference.

### 4. Create `config.go`

This file contains the spec mapper function that converts v2 CRD to v1 SecretStoreSpec.

**Template:**

```go
/*
Licensed under the Apache License, Version 2.0 (the "License");
...
*/

package main

import (
    "context"
    "sigs.k8s.io/controller-runtime/pkg/client"
    v1 "github.com/external-secrets/external-secrets/apis/externalsecrets/v1"
    myproviderv2alpha1 "github.com/external-secrets/external-secrets/apis/provider/myprovider/v2alpha1"
    pb "github.com/external-secrets/external-secrets/proto/provider"
)

// GetSpecMapper returns the spec mapper function for the provider.
// This function converts v2 ProviderReference to v1 SecretStoreSpec.
func GetSpecMapper(kubeClient client.Client) func(*pb.ProviderReference) (*v1.SecretStoreSpec, error) {
    return func(ref *pb.ProviderReference) (*v1.SecretStoreSpec, error) {
        var provider myproviderv2alpha1.MyProvider
        err := kubeClient.Get(context.Background(), client.ObjectKey{
            Namespace: ref.Namespace,
            Name:      ref.Name,
        }, &provider)
        if err != nil {
            return nil, err
        }
        return &v1.SecretStoreSpec{
            Provider: &v1.SecretStoreProvider{
                MyProvider: &provider.Spec,
            },
        }, nil
    }
}
```

**For providers with validation logic:**

```go
func GetSpecMapper(kubeClient client.Client) func(*pb.ProviderReference) (*v1.SecretStoreSpec, error) {
    return func(ref *pb.ProviderReference) (*v1.SecretStoreSpec, error) {
        // Validate the kind if provider supports multiple
        if ref.Kind != myproviderv2alpha1.ExpectedKind {
            return nil, fmt.Errorf("unsupported provider kind: %s", ref.Kind)
        }

        var provider myproviderv2alpha1.MyProvider
        err := kubeClient.Get(context.Background(), client.ObjectKey{
            Namespace: ref.Namespace,
            Name:      ref.Name,
        }, &provider)
        if err != nil {
            return nil, err
        }

        // Map fields from v2 to v1
        return &v1.SecretStoreSpec{
            Provider: &v1.SecretStoreProvider{
                MyProvider: &v1.MyProviderProvider{
                    Auth:      provider.Spec.Auth,
                    Region:    provider.Spec.Region,
                    // ... other fields
                },
            },
        }, nil
    }
}
```

### 5. Implement v1 Provider/Generator Wrappers

If you're wrapping existing v1 providers:
- Store implementation goes in `store/`
- Generator implementation goes in `generator/`
- Each should export a `NewProvider()` or `New*Generator()` function

Example structure:
```
providers/v2/myprovider/
├── provider.yaml
├── config.go
├── store/
│   └── store.go           # exports NewProvider()
└── generator/
    ├── mygen.go          # exports NewMyGenerator()
    └── mygen_test.go
```

### 6. Generate Files

```bash
make generate-providers
```

This will create:
- `providers/v2/myprovider/main.go` (generated)
- `providers/v2/myprovider/Dockerfile` (generated)

### 7. Verify Compilation

```bash
cd providers/v2/myprovider
go mod init  # if needed
go mod tidy
go build
```

### 8. Add to Makefile

Update the root `Makefile` to include build targets for your provider:

```makefile
.PHONY: docker.build.provider.myprovider
docker.build.provider.myprovider: ## Build MyProvider provider image
	@$(INFO) $(DOCKER) build MyProvider provider
	@DOCKER_BUILDKIT=1 $(DOCKER) build \
		-f providers/v2/myprovider/Dockerfile \
		. \
		$(DOCKER_BUILD_ARGS) \
		-t $(IMAGE_REGISTRY)/external-secrets/provider-myprovider:$(IMAGE_TAG)
	@$(OK) $(DOCKER) build MyProvider provider
```

And add it to the aggregate targets:
```makefile
docker.build.providers: ... docker.build.provider.myprovider
```

### 9. Test

1. **Unit tests**: Test your store/generator implementations
2. **Integration tests**: Deploy and test end-to-end
3. **E2E tests**: Add tests to `e2e/` directory

## Real-World Examples

### Kubernetes Provider (Simple)
- **Location**: `providers/v2/kubernetes/`
- **Characteristics**: Single store, no generators
- **See**: `kubernetes/provider.yaml` and `kubernetes/config.go`

### Fake Provider (Medium)
- **Location**: `providers/v2/fake/`
- **Characteristics**: Single store, single generator
- **See**: `fake/provider.yaml` and `fake/config.go`

### AWS Provider (Complex)
- **Location**: `providers/v2/aws/`
- **Characteristics**: Single store, two generators (ECR, STS)
- **See**: `aws/provider.yaml` and `aws/config.go`

## Common Patterns

### Multiple Stores from Same Provider

If your provider supports multiple secret backends (e.g., AWS has SecretsManager and ParameterStore):

```yaml
stores:
  - gvk:
      group: "provider.external-secrets.io"
      version: "v2alpha1"
      kind: "SecretsManager"
    v1Provider: "github.com/.../providers/v2/aws/store"
    v1ProviderFunc: "NewProvider"
  - gvk:
      group: "provider.external-secrets.io"
      version: "v2alpha1"
      kind: "ParameterStore"
    v1Provider: "github.com/.../providers/v2/aws/store"
    v1ProviderFunc: "NewProvider"
```

Then in `config.go`, handle both kinds:
```go
func GetSpecMapper(kubeClient client.Client) func(*pb.ProviderReference) (*v1.SecretStoreSpec, error) {
    return func(ref *pb.ProviderReference) (*v1.SecretStoreSpec, error) {
        switch ref.Kind {
        case "SecretsManager":
            // Handle SecretsManager
        case "ParameterStore":
            // Handle ParameterStore
        default:
            return nil, fmt.Errorf("unsupported kind: %s", ref.Kind)
        }
    }
}
```

### Multiple Generators from Same Package

The generator automatically handles this. Both will use the same import alias:

```yaml
generators:
  - gvk:
      kind: "GeneratorA"
    v1Generator: "github.com/.../providers/v2/myprovider/generator"
    v1GeneratorFunc: "NewGeneratorA"
  - gvk:
      kind: "GeneratorB"
    v1Generator: "github.com/.../providers/v2/myprovider/generator"
    v1GeneratorFunc: "NewGeneratorB"
```

Generated code will correctly use the same alias for both.

## Troubleshooting

### "Schema validation failed"
- Check your `provider.yaml` against the schema in `hack/schema/provider-config.schema.json`
- Ensure you have at least one of `stores` or `generators`
- Verify all required fields are present

### "cannot find package"
- Check that import paths in `provider.yaml` are correct
- Run `go mod tidy` in your provider directory
- Verify the v1 provider package exists

### "undefined: GetSpecMapper"
- Ensure you created `config.go` with the `GetSpecMapper` function
- Check that the function signature matches exactly
- Verify `configPackage` in `provider.yaml` is correct (usually ".")

### Generated code doesn't match my needs
- For custom logic, modify `config.go` (not generated)
- For structural changes, update `provider.yaml` and regenerate
- For common patterns across all providers, consider updating the templates in `hack/templates/`

## Best Practices

1. **Keep config.go clean**: Only spec mapping logic should be here
2. **Use descriptive names**: Make your GVK kinds clear and unambiguous
3. **Test thoroughly**: Generate, compile, and test before committing
4. **Version your API**: Use v2alpha1 consistently
5. **Document special cases**: If your provider needs special handling, document it

## Questions?

- Check `providers/v2/hack/README.md` for generator documentation
- Look at existing providers for examples
- Ask in the #external-secrets Slack channel

