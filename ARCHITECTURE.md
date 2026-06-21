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
in the *same directory* (so the rename stays on one filesystem), `fsync`s,
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

| Layer       | File                              | What it covers                                              |
| ----------- | --------------------------------- | ----------------------------------------------------------- |
| Unit        | `internal/processor/*_test.go`    | pure helpers, file I/O on `t.TempDir`, state transitions    |
| Unit        | `internal/filter/filter_test.go`  | label/annotation matching                                   |
| Unit        | `internal/k8s/client_test.go`     | in-cluster vs kubeconfig branching via swappable seams      |
| Integration | `internal/watcher/e2e_test.go`    | full List→Watch→process→disk loop against the fake clientset |

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
