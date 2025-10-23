# Build stage
FROM golang:1.25-alpine AS builder

WORKDIR /workspace

# Copy the entire repo (needed for replace directive and shared packages)
COPY . .

# Build the provider binary from the providers/kubernetes directory
WORKDIR /workspace/providers/v2/kubernetes
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -o provider-kubernetes main.go

# Runtime stage
FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /workspace/providers/v2/kubernetes/provider-kubernetes .
USER 65532:65532

ENTRYPOINT ["/provider-kubernetes"]
