# Repository Guidelines

## Start With Existing Docs

This repository already has contributor-facing guidance. Before changing code,
read `CONTRIBUTING.md` for setup, commands, formatting, linting, vendoring, and
test strategy. Read `ARCHITECTURE.md` to unserstand package boundaries, the
watch loop, processor state, or disk-write behavior. Use `README.md` for
user-facing configuration and examples.

## Validation Loop

Use the narrowest useful validation while iterating, then broaden before
handoff:

- For package-local changes: `go test ./internal/<package>`.
- For watcher or concurrency changes: `just test-race`.
- For final verification when practical: `just fmt && just lint && just test`.
- For dependency changes: `just vendor` followed by relevant tests.

If a command cannot be run, report the exact command and reason.

## Testing Practices

New behavior should have nearby tests. Keep tests hermetic: use `t.TempDir()`,
`t.Setenv()`, `t.Cleanup()`, and the fake Kubernetes clientset instead of real
cluster or filesystem assumptions. For async watcher assertions, use bounded
checks such as `require.Eventually`.

## Feedback & Handoff

Final responses should summarize what changed, name the validation performed,
and call out residual risk. If you discover a pre-existing issue while working,
separate it from the requested change and avoid fixing it unless it blocks the
task.

## Security-Sensitive Areas

Treat file paths, permissions, atomic writes, and Secret material as
security-sensitive. Do not weaken default file or directory modes without a
clear user-facing reason and documentation update.
