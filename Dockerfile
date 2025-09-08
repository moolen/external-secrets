FROM gcr.io/distroless/static@sha256:f2ff10a709b0fd153997059b698ada702e4870745b6077eff03a5f4850ca91b6
ARG TARGETOS
ARG TARGETARCH
COPY bin/external-secrets-${TARGETOS}-${TARGETARCH} /bin/external-secrets

# Run as UID for nobody
USER 65534

ENTRYPOINT ["/bin/external-secrets"]
