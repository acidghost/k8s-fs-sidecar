# syntax=docker/dockerfile:1.27.1@sha256:4edf897a3ffa55b89f906fc8cc78afdb3f1834cc9c7083565e611a8a7d5fe99e

FROM golang:1.27.1-alpine@sha256:8a5910f31396cd4d89662f56c68b3ae31d374308270a1c3bd96672ee5ed43414 AS builder
RUN apk add --no-cache ca-certificates just
WORKDIR /src
COPY go.mod go.sum ./
COPY justfile ./
COPY vendor/ vendor/
COPY main.go ./
COPY internal/ internal/
ARG BUILD_VERSION=0.0.0
ARG BUILD_COMMIT=unknown
ARG TARGETOS=linux
ARG TARGETARCH
RUN just version="${BUILD_VERSION}" commit_sha="${BUILD_COMMIT}" build "${TARGETOS}" "${TARGETARCH}" \
    && mv "build/k8s-fs-sidecar-${TARGETOS}-${TARGETARCH}" /usr/local/bin/k8s-fs-sidecar

FROM scratch
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/local/bin/k8s-fs-sidecar /usr/local/bin/k8s-fs-sidecar
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/k8s-fs-sidecar"]
