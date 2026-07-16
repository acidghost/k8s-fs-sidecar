# syntax=docker/dockerfile:1@sha256:87999aa3d42bdc6bea60565083ee17e86d1f3339802f543c0d03998580f9cb89

FROM golang:1.26.5-alpine@sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2 AS builder
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
