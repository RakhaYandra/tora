# Changelog

## v0.3.0

P1 batch:

- `restart --all`: stop everything in reverse dependency order, then start
  again with Up rollback semantics.
- `logs --grep PATTERN` (`-i` case-insensitive): substring filter on raw
  lines, static and `--follow` modes, single and `--all`.
- `watch [SERVICE]`: fsnotify recursive workdir monitoring, default ignores
  (incl. `.tora`), 500ms debounce, guarded restarts under per-action lock,
  no lock while idle, SIGINT exits 130 with services running.

Fix: stopping an already-dead (EXITED/FAILED) service records STOPPED
instead of leaving stale FAILED in `ps`.

Limits: no daemon, dependency is order not readiness, no health checks,
no resource metering, config edits unwatched.

## v0.2.0

P1: interactive `tui` dashboard (Bubble Tea, isolated in `internal/tui`):
live status table, log pane with follow, filter, start/stop/restart actions.

Fix: lingering groups after SIGKILL (or lapsed ownership mid-shutdown)
return explicit errors instead of silent stopped.

## v0.1.1

Fix: `Stop` returns loud linger errors when a group outlives SIGKILL or
ownership lapses mid-shutdown. Regression test for identity refusal.

## v0.1.0

Initial release. P0 developer process lifecycle on Linux:

- `init/up/down/ps/start/stop/restart/logs/config validate/version`,
  exits 0/1/2/130, stable `ps` JSON.
- Strict `tora.yaml` schema, Kahn dependency ordering with cycle rejection,
  parallel per-level startup with rollback.
- Verified process ownership (PID + kernel starttime + PGID, never bare
  PID), per-service process groups, SIGTERM-wait-SIGKILL shutdown,
  dependent guard on stop/restart, flock project lock.
- Log capture with 10 MiB rotation, tail/follow, display-only prefixes.
- Bounded on-failure restarts reconciled on invocation (no daemon).

Limits: Linux only, no health checks, no resource metering, no daemon,
no TUI, no restart --all, no log search, no watch mode.
