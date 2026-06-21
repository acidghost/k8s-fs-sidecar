# Architecture

`k8s-fs-sidecar` is a single-process Go service that mirrors a filtered subset
of Kubernetes `ConfigMap`s and `Secret`s onto a shared volume as plain files.

## Package layout

```
main.go                     entrypoint: load config, wire components, handle signals
internal/
  config/      config.go        env → *Config (validated)
  k8s/         client.go        builds *kubernetes.Clientset (in-cluster or kubeconfig)
  filter/      filter.go        label/annotation matching against metav1.ObjectMeta
  processor/   processor.go     owns per-resource state; writes/deletes files on disk
  watcher/     watcher.go       List → Watch loop, one goroutine per (resource, namespace)
  logger/      logger.go        zerolog init (level + json/logfmt)
```

Each package has a single responsibility and no cross-package coupling beyond
`config` (read by all) and `processor` (driven by `watcher`). This makes the
units independently testable.

## Component graph

```
                      ┌─────────────┐
                      │   config    │  env vars (FS_SIDECAR_*)
                      └──────┬──────┘
                             │  *Config
            ┌────────────────┼─────────────────┐
            ▼                ▼                 ▼
       ┌─────────┐     ┌──────────┐       ┌──────────┐
       │   k8s   │     │  logger  │       │ processor│
       │ client  │     │  (init)  │       │ NewProc  │
       └────┬────┘     └──────────┘       └────┬─────┘
            │ *kubernetes.Clientset            │ *Processor
            ▼                                  ▲
       ┌────────────────────┐    events        │  Process{ConfigMap,Secret}(obj, type)
       │      watcher       │──────────────────┘
       │ List → Watch loop  │
       └─────────┬──────────┘
                 │ kubernetes.Interface
                 ▼
        kube-apiserver  (ConfigMaps, Secrets)
```

## Runtime data flow

The entrypoint (`main.go`) constructs one `*Processor` and shares it across
every watcher goroutine:

1. **`config.LoadFromEnv()`** parses and validates env vars.
2. **`k8s.NewClient()`** picks in-cluster config when the service-account token
   is mounted, otherwise falls back to `~/.kube/config` (handy for local runs).
3. **`processor.NewProcessor(cfg)`** owns the in-memory `state` map and a mutex.
4. **`watcher.WatchConfigMaps` / `WatchSecrets`** spawn one goroutine per
   `(resource, namespace)` pair. Each runs the same loop (see below).
5. On `SIGINT`/`SIGTERM`, `signal.NotifyContext` cancels the shared context;
   watchers unblock and the process exits cleanly.

### The watch loop

Each per-namespace goroutine runs an infinite List → Watch → resume cycle,
threading the `resourceVersion` so no events are lost between a list and a
watch:

```
rv = ""
loop:
  if rv == "":
      rv = initialSync()          # List, process ADDED, return list.ResourceVersion
  rv, err = watch(rv)             # Watch(RV=rv, AllowWatchBookmarks=true)
  if err is ResourceExpired/Gone:
      rv = ""                     # force a single re-list on next iteration
  else:
      rv = latest observed RV     # resume from here (event or bookmark)
  backoff(ctx, 5s)                # cancellable sleep
```

Bookmarks update `rv` without touching disk. `ERROR` frames are surfaced as
errors rather than silently dropped. A closed result channel (clean
disconnect) returns the current `rv` so the loop resumes without re-listing.

### Processing an event

`Processor.ProcessConfigMap` / `ProcessSecret` (both behind a single mutex):

1. Compute the resource key (`<type>/<namespace>/<name>`).
2. On `DELETED`: delete every file tracked for that key, drop the entry.
3. Otherwise build the new `FileState{Folder, Files}`:
   - folder comes from the per-resource annotation, else `cfg.Folder`
   - files come from `Data`/`BinaryData` (ConfigMap) or `Data` (Secret)
4. Diff against the prior state:
   - if the folder changed, delete the old files from the old folder
   - write the new/changed files (hash-deduped, atomically — see below)
   - delete files for keys that are no longer present
5. Store the new state.

### Atomic file replacement

`WriteFile` never truncates the destination in place. It writes to a temp file
in the _same directory_ (so the rename stays on one filesystem), `fsync`s,
`chmod` to the configured file mode, closes, and then `rename`s over the
the target. A failure at any
step removes the temp file, so no partial state is observable by readers and
no temp files leak.

### Concurrency

`WatchConfigMaps` and `WatchSecrets` each spawn goroutines, and all of them
share one `*Processor`. Concurrent map access on `Processor.state` is
serialized by a `sync.Mutex` held across `updateResource` /
`handleDeletedResource`. The lock is method-scoped (not per-file) so the
on-disk state and the in-memory map stay mutually consistent — fine for this
workload, which is event-driven and low-frequency.

## Testing strategy

Three layers, all in-process and hermetic:

| Layer       | File                             | What it covers                                               |
| ----------- | -------------------------------- | ------------------------------------------------------------ |
| Unit        | `internal/processor/*_test.go`   | pure helpers, file I/O on `t.TempDir`, state transitions     |
| Unit        | `internal/filter/filter_test.go` | label/annotation matching                                    |
| Unit        | `internal/k8s/client_test.go`    | in-cluster vs kubeconfig branching via swappable seams       |
| Integration | `internal/watcher/e2e_test.go`   | full List→Watch→process→disk loop against the fake clientset |

The fake clientset (`k8s.io/client-go/kubernetes/fake`) honors
`ResourceVersion` and supports `WatchReactor`s, which lets the integration
tests prove both the resume-from-RV path and the expired-RV recovery path
without a real apiserver.

Run everything under the race detector — it's how the `Processor.state`
concurrency bug was originally caught:

```bash
just test-race
```

## Dependencies

Only the Kubernetes Go client (`k8s.io/api`, `apimachinery`, `client-go`) and
`zerolog` for structured logging. `testify` is test-only. The fake clientset
is part of `client-go` (no extra module). Dependencies are vendored.

## Extending it

- **New resource type** (e.g. to sync a CRD): add a `ProcessX` to the
  processor, a List/Watch pair to the watcher following the existing pattern,
  and a config knob to opt in. The fake-clientset integration test pattern
  transfers directly.
- **New configuration knob**: add it to `config.Config`, load + validate it in
  `config.LoadFromEnv`, and (if it affects output) thread it through the
  processor or watcher.
- **Server-side label selection** (future optimization): the watcher currently
  filters client-side; for label mode you could push a `labels.Require*`
  selector into `metav1.ListOptions.LabelSelector` to reduce wire bytes and
  narrow RBAC. Annotation mode genuinely requires client-side filtering.

## Release & distribution

The container image is the only distribution artifact. The `release.yaml`
workflow (`.github/workflows/release.yaml`) builds, signs, and publishes it,
and on tag creates a GitHub Release with auto-generated notes.

### Two trigger modes, one build path

| Trigger              | Image tags                     | GitHub Release |
| -------------------- | ------------------------------ | -------------- |
| `push` on a `v*` tag | semver (+ `latest` if not pre) | yes            |
| `workflow_dispatch`  | `dev-<sha>` only               | no             |

The build (checkout → buildx → login → metadata → build-push → cosign →
sign) is identical in both modes; only the tag rules and the optional
release step differ. `linux/amd64` is the only platform today.

### Image tags

- Tag mode produces `:1.0.0`, `:1.0`, `:1`, and `:latest`.
- Prereleases (`v1.0.0-rc.1`) get `:1.0.0-rc.1` only — they never claim
  `latest`, `:1`, or `:1.0`, so a release candidate cannot hijack the stable
  image. This is enforced via `enable={{!prerelease}}` on those tag rules.
- `:latest` is exclusively owned by tag-triggered non-prerelease runs; the
  manual `dev-<sha>` path never touches it, so there is no race between the
  two modes over the floating tag.

### Security model

The design assumes three classes of adversary: an unprivileged
collaborator, a compromised GitHub Action, and an attacker who later
repoints a tag. The controls, in defense-in-depth order:

1. **Job-split permissions.** Top-level `permissions: {}` denies everything;
   each job grants only what it needs. `build-and-push` has
   `packages: write` + `id-token: write` (cosign) but **no** `contents: write`;
   `release` has `contents: write` but **no** `packages: write`. No single job
   can both ship an image and forge a release for it.
2. **Sign by digest, not tag.** Tags are mutable; the registry digest is the
   image's immutable identity. `cosign sign ${IMAGE}@${DIGEST}` signs the
   exact bytes that were pushed, so a later tag repoint cannot silently swap
   the signed content.
3. **Keyless signing (OIDC).** `id-token: write` + `cosign sign --yes` uses
   a short-lived sigstore certificate bound to this workflow, repo, and ref.
   There is no long-lived signing key to leak or rotate.
4. **Provenance + SBOM.** `provenance: mode=max` and `sbom: true` attach
   in-toto SLSA provenance and a software bill of materials to the image, so
   consumers can audit what was built and from what source.
5. **No third-party release action.** The release is created with the
   pre-installed `gh` CLI (authed via `GITHUB_TOKEN`), shrinking the
   supply-chain surface versus depending on a release-publishing action.
6. **Tag-anchored release.** `--verify-tag` aborts if the remote tag doesn't
   match what the run thinks it's releasing; `--target $SHA` pins the release
   to the exact built commit. `--generate-notes` builds the changelog from the
   previous release, so a maintainer can't accidentally inject an arbitrary
   body.
7. **Non-cancelling concurrency.** `cancel-in-progress: false` ensures a
   release run is never killed mid-push by a stray concurrent run.

### Required repo settings (the workflow cannot enforce these)

These are enforced by GitHub itself and must be set once in the UI. The
workflow above is only half-secure without them:

- **Tag protection rule** on `v*` (Settings → Tags). Restricts who can push
  release tags. Without it, any collaborator with push access can mint a
  release.
- **Immutable releases** (release settings). Prevents a release's git tag and
  assets from being modified or deleted after publish — closes the
  force-push-a-moved-tag attack.
- **Default `GITHUB_TOKEN` read-only** (Settings → Actions → General →
  Workflow permissions). Belt-and-suspenders on top of the explicit
  `permissions:` grants; confirm it's not `read-and-write`.
- **Branch protection on `main`** with required CI checks — the foundation
  the release builds on.

### Verifying a published image

```bash
digest=$(crane digest ghcr.io/acidghost/k8s-fs-sidecar:1.0.0)
cosign verify ghcr.io/acidghost/k8s-fs-sidecar@${digest} \
  --certificate-identity-regexp "https://github.com/acidghost/k8s-fs-sidecar/.github/workflows/release.yaml@refs/tags/.*" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
cosign verify-attestation ghcr.io/acidghost/k8s-fs-sidecar@${digest} \
  --type slsaprovenance \
  --certificate-identity-regexp "https://github.com/acidghost/k8s-fs-sidecar/.github/workflows/release.yaml@refs/tags/.*" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```
