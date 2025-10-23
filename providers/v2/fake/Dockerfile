FROM golang:1.25-alpine AS builder
WORKDIR /workspace
COPY apis/ apis/
COPY providers/ providers/
COPY runtime/ runtime/
COPY generators/ generators/

WORKDIR /workspace/providers/v2/fake
RUN go mod tidy && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -o provider-fake main.go

FROM gcr.io/distroless/static:nonroot
COPY --from=builder /workspace/providers/v2/fake/provider-fake .
ENTRYPOINT ["./provider-fake"]

