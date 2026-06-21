# Contributing

Thanks for considering a contribution. This is a small project with a narrow
scope (sync ConfigMaps/Secrets to files); changes that keep it small and
focused are preferred over feature creep.

## Scope

In scope:

- correctness and robustness of the existing sync loop
- support for new Kubernetes API behaviors that affect the core contract
- tests, docs, and tooling improvements

Probably out of scope (open an issue to discuss first):

- templating engines, decryption layers, or other transformations of the
  synced content
- alternative sinks (e.g. HTTP endpoints, sidecar RPCs) — this is a
  files-on-disk sidecar

## Setup

You need Go 1.26+ and [`just`](https://github.com/casey/just). Versions are
pinned in [`mise.toml`](mise.toml); if you use [mise](https://mise.jdx.dev),
`mise install` will fetch the right ones.

```bash
git clone <repo> && cd k8s-fs-sidecar
mise install          # optional, if you use mise
just build
just test
```

## Development workflow

The common commands (full list: `just help`):

```bash
just fmt         # apply formatters (driven by .golangci.yaml — currently goimports)
just lint        # golangci-lint, must be clean
just test        # go test ./...
just test-race   # go test -race ./...  (run before pushing)
just build       # current platform
just build-all   # darwin/arm64, linux/arm64, linux/amd64
just vendor      # go mod tidy && go mod vendor
```

CI (`.github/workflows/ci.yaml`) runs `lint`, `test`, `test-race`, and
`build` on every push and PR — all four must pass.

### Formatting and linting

Formatting and linting are both driven by `.golangci.yaml`:

- the `fmt` recipe runs `golangci-lint fmt` (applies the configured formatters)
- the `lint` recipe runs `golangci-lint run` (checks everything)

Keeping them on the same config means `fmt` fixes exactly what `lint`
checks. Don't run `go fmt` directly — it won't apply `goimports` grouping.

`gosec` findings are real (permissions, path traversal) and should be fixed
or suppressed with a `//nolint:gosec // <reason>` comment explaining *why*
the suppression is safe. Do not blanket-disable the linter.

### Vendoring

Dependencies are vendored. After changing `go.mod` or adding an import:

```bash
just vendor
```

Commit `go.mod`, `go.sum`, and the `vendor/` tree together.

## Testing strategy

| Kind         | Where                             | What                                                          |
| ------------ | --------------------------------- | ------------------------------------------------------------- |
| Unit         | `internal/processor/*_test.go`    | helpers, file I/O on `t.TempDir()`, state transitions         |
| Unit         | `internal/filter/filter_test.go`  | label/annotation matching                                     |
| Unit         | `internal/k8s/client_test.go`     | client construction branching (in-cluster vs kubeconfig)      |
| Integration  | `internal/watcher/e2e_test.go`    | full List→Watch→process→disk loop against the fake clientset  |

Guidelines:

- **Tests must be hermetic.** Use `t.TempDir()` for any disk state, and
  `t.Setenv` / `t.Cleanup` for env vars and global seams. No shared mutable
  state between cases.
- **Run `-race`.** The watcher is concurrent; the data race on `Processor.state`
  was originally caught this way. `just test-race` is in CI for a reason.
- **Prefer the fake clientset.** Integration tests use
  `k8s.io/client-go/kubernetes/fake`, which honors `resourceVersion` and
  supports `WatchReactor`s. No real apiserver, no Docker, no network.
- **Don't sleep-poll without a bound.** Use `require.Eventually` with a tight
  interval and a generous-but-bounded timeout for async assertions.

See [`ARCHITECTURE.md`](ARCHITECTURE.md) for how the pieces fit together.

## Making changes

A typical change touches one or two packages and stays under ~50 LOC plus tests:

1. **Add or change behavior** in `internal/...`. Keep packages single-purpose.
2. **Add or update tests** alongside the change. New behavior without a test
   will be sent back.
3. **Run the full gate locally** before pushing:

   ```bash
   just fmt && just lint && just test-race && just build
   ```

4. **Update docs** if the change is user-visible (`README.md`) or affects the
   package layout / data flow (`ARCHITECTURE.md`).
5. **Update `vendor/`** if you added or changed an import (`just vendor`).

## License

By contributing, you agree that your contributions are released into the
public domain under the [UNLICENSE](UNLICENSE).
