program := 'k8s-fs-sidecar'

version := 'SNAPSHOT-'+`git describe --tags --always --dirty 2>/dev/null || printf 'unknown'`
commit_sha := `(git rev-parse HEAD 2>/dev/null || printf 'unknown') | tr -d '\n'`
build_time := `date -u '+%Y-%m-%d_%H:%M:%S'`

container_engine := 'docker'
container_registry := 'ghcr.io'
container_image := container_registry + '/acidghost/' + program

ldflags := '-s -w -X main.buildVersion='+version \
        +' -X main.buildCommit='+commit_sha \
        +' -X main.buildDate='+build_time

goos := if os() == 'macos' { 'darwin' } else { os() }
goarch := if arch() == 'aarch64' { 'arm64' } else if arch() == 'x86_64' { 'amd64' } else { arch() }

alias b := build
alias r := run

build-all: (build 'darwin' 'arm64') (build 'linux' 'arm64') (build 'linux' 'amd64')

build-image platform=goarch:
    {{container_engine}} build \
        --platform 'linux/{{platform}}' \
        --build-arg BUILD_VERSION='{{version}}' \
        --build-arg BUILD_COMMIT='{{commit_sha}}' \
        -t '{{container_image}}' .

build os=goos arch=goarch: build-dir
    CGO_ENABLED=0 GOOS={{os}} GOARCH={{arch}} \
        go build \
            -ldflags '{{ldflags}}' \
            -o build/{{program}}-{{os}}-{{arch}}

build-dir:
    mkdir -p build

run *args: build
    ./build/{{program}}-{{goos}}-{{goarch}} {{args}}

vendor:
    go mod tidy
    go mod vendor

fmt:
    golangci-lint fmt

test:
    go test ./...

test-verbose:
    go test -v ./...

test-race:
    go test -race ./...

test-coverage:
    go test -coverprofile=coverage.out ./...
    go tool cover -func=coverage.out

test-coverage-html:
    go test -coverprofile=coverage.out ./...
    go tool cover -html=coverage.out

test-unit:
    go test ./internal/...

test-unit-verbose:
    go test -v ./internal/...

lint:
    golangci-lint run

install: build
    cp -v './build/{{program}}-{{goos}}-{{goarch}}' "$(go env GOBIN)/{{program}}"

clean:
    rm -rf build

help:
    @just --list
