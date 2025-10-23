# Multi-stage build for AWS Provider
FROM golang:1.25-alpine AS builder

WORKDIR /workspace
COPY apis/ apis/
COPY providers/ providers/
COPY runtime/ runtime/
COPY generators/ generators/

# Build the provider binary
WORKDIR /workspace/providers/v2/aws
RUN go mod tidy && CGO_ENABLED=0 GOOS=linux go build -a -o provider-aws main.go

# Use distroless as minimal base image
FROM gcr.io/distroless/static:nonroot

WORKDIR /

# Copy the binary
COPY --from=builder /workspace/providers/v2/aws/provider-aws .

USER 65532:65532

ENTRYPOINT ["/provider-aws"]
