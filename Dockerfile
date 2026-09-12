# syntax=docker/dockerfile:1@sha256:ecfaec9ed6d810b56388c508f4121597bfbba70d41a6dfeee4d8cad5f295fc32

FROM golang:1.26.6-alpine@sha256:3889b425f035be855a72fb4755265311293b6d414521f0a519d819df32222d83 AS builder
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
