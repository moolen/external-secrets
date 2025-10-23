# Build stage
FROM golang:1.25-alpine AS builder

WORKDIR /workspace
COPY apis/ apis/
COPY providers/ providers/
COPY runtime/ runtime/
COPY generators/ generators/

# Build the provider binary from the providers/kubernetes directory
WORKDIR /workspace/providers/v2/kubernetes
RUN go mod tidy && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -o provider-kubernetes main.go

# Runtime stage
FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /workspace/providers/v2/kubernetes/provider-kubernetes .
USER 65532:65532

ENTRYPOINT ["/provider-kubernetes"]
