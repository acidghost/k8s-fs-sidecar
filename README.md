# k8s-fs-sidecar

A small Go sidecar container that watches Kubernetes `ConfigMap`s and `Secret`s
and materializes their contents as files in a shared volume. Use it to deliver
configuration to applications that read files (not the Kubernetes API) — without
restarting the pod on every change.

A lighter, focused alternative to [kiwigrid/k8s-sidecar](https://github.com/kiwigrid/k8s-sidecar).

## Features

- **Live sync**: initial list + streaming watch; updates land on disk within seconds
- **Label or annotation filtering**: match resources by label or annotation, with optional value match
- **ConfigMap `data` and `binaryData`**: text and binary keys both supported
- **Secret `data`**: each key becomes a file
- **Per-resource folder override**: redirect a resource's files via an annotation
- **Multi-namespace**: comma-separated list of namespaces
- **Hash-based dedup**: only rewrites files when their content actually changes
- **Atomic writes**: temp-file + rename, so consumers never see a partial file
- **Watch resume**: threads `resourceVersion` to avoid event gaps; re-lists only on `410 Gone`
- **Tiny image**: static Go binary on `scratch`, runs as non-root uid `65532`

## Quick start

Apply the example manifest (Deployment + ConfigMaps + Secrets + RBAC):

```bash
kubectl apply -f examples/example.yaml
```

It runs an `alpine` container printing the shared folder every 5s, alongside
this sidecar writing `ConfigMap`/`Secret` data into `/etc/config`.

## Configuration

All configuration is via environment variables, prefixed `FS_SIDECAR_`.

| Variable                       | Required | Default                        | Description                                                             |
| ------------------------------ | -------- | ------------------------------ | ----------------------------------------------------------------------- |
| `FS_SIDECAR_LABEL`             | Yes\*    | -                              | Label or annotation key used for filtering                              |
| `FS_SIDECAR_LABEL_VALUE`       | No       | -                              | If set, the key must equal this value; if unset, key presence is enough |
| `FS_SIDECAR_LABEL_ANNOTATION`  | No       | `label`                        | Filter by `label` or `annotation`                                       |
| `FS_SIDECAR_FOLDER`            | Yes      | -                              | Destination directory for materialized files                            |
| `FS_SIDECAR_FOLDER_ANNOTATION` | No       | `k8s-sidecar-target-directory` | Per-resource annotation that overrides the destination folder           |
| `FS_SIDECAR_NAMESPACE`         | No       | current namespace              | Comma-separated namespaces to watch                                     |
| `FS_SIDECAR_RESOURCE`          | No       | `both`                         | `configmap`, `secret`, or `both`                                        |
| `FS_SIDECAR_LOG_LEVEL`         | No       | `info`                         | `trace`, `debug`, `info`, `warn`, `error`                               |
| `FS_SIDECAR_LOG_FORMAT`        | No       | `json`                         | `json` or `logfmt`                                                      |
| `FS_SIDECAR_FILE_MODE`         | No       | `0600`                         | Octal permission mode for synced files (e.g. `0644` for world-readable) |
| `FS_SIDECAR_DIR_MODE`          | No       | `0700`                         | Octal permission mode for created directories (e.g. `0755`)             |

\*`FS_SIDECAR_LABEL` is always required.

### Folder override

Redirect a specific resource's files to a custom location by annotating it:

```yaml
metadata:
  annotations:
    k8s-sidecar-target-directory: "/custom/path"
```

Absolute paths are used as-is; relative paths are resolved under `FS_SIDECAR_FOLDER`.

### Required RBAC

The sidecar's `ServiceAccount` needs `get`, `list`, and `watch` on the
resources it syncs. The example manifest includes a matching `Role` +
`RoleBinding`; for cluster-wide use, switch to a `ClusterRole`/`ClusterRoleBinding`:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: k8s-fs-sidecar
rules:
  - apiGroups: [""]
    resources: ["configmaps", "secrets"]
    verbs: ["get", "watch", "list"]
```

## File permissions

Files are written `0600` and directories `0700` by default (owner-only).
The published image runs as uid `65532`, so for a consuming container running
a **different** uid, you have two options:

1. **Share the uid** — set a pod-level `securityContext.runAsUser: 65532` so
   both containers run as the same user. This is the most secure option
   because secret material stays owner-only.
2. **Widen the file permissions** — set `FS_SIDECAR_FILE_MODE=0644` and
   `FS_SIDECAR_DIR_MODE=0755` so the files are world-readable. Convenient
   when you don't control the consuming container's uid, at the cost of
   exposing synced Secrets to every user on the node.

```yaml
env:
- name: FS_SIDECAR_FILE_MODE
  value: "0644"
- name: FS_SIDECAR_DIR_MODE
  value: "0755"
```

Modes are parsed as octal; setuid/setgid/sticky bits (`>0777`) are rejected.

## How it works

1. **Initial sync** — list matching resources, write their files
2. **Watch** — open a `Watch` from the list's `resourceVersion` (no gap)
3. **Events** — `ADDED`/`MODIFIED` write/update files; `DELETED` removes them
4. **Dedup** — SHA-256 comparison skips writes when content is unchanged
5. **Atomic writes** — temp file + `rename`, so readers never observe a partial file
6. **Reconnect** — on a dropped or expired (`410 Gone`) watch, resume from the last observed `resourceVersion`, or re-list if it has expired

See [`ARCHITECTURE.md`](ARCHITECTURE.md) for the package layout and data flow.

## Development

See [`CONTRIBUTING.md`](CONTRIBUTING.md) for conventions, the test strategy,
and how to add a new resource type or a new configuration knob.

## Container image

Pre-built images are published to the GitHub Container Registry on demand
via the _Publish Image_ workflow:

```
ghcr.io/acidghost/k8s-fs-sidecar:latest
```

The image is signed with cosign and ships with provenance + SBOM.

## License

Released into the public domain under the terms of the [UNLICENSE](UNLICENSE).
