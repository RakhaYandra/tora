# Tora — Developer Process Orchestrator (v0.1)

One project. One command. Full control over your development processes.

```bash
tora init
tora up
tora ps
tora logs api --follow
tora restart worker
tora down
```

Tora runs your local dev processes (API, frontend, worker, scheduler, helper
services) from a single `tora.yaml`: dependency-ordered startup, status, logs,
restart, and coordinated graceful shutdown. Linux-first, CLI-only in v0.1.

## Install

Requires Linux and Go 1.27+ to build.

```bash
git clone https://github.com/RakhaYandra/tora
cd tora
make install   # builds to ~/.local/bin/tora (ensure it is in PATH)
```

Or `make build` (`./bin/tora`) / `make release` (`dist/tora-linux-*` + SHA256SUMS).

## Quickstart

```bash
mkdir demo && cd demo
tora init --name pulse
tora config validate   # no side effects, exit 2 when invalid
tora up
tora ps
tora logs api
tora restart api
tora down
```

## Configuration

`tora.yaml` in the project dir (discovery: cwd → parents; override `-c PATH`,
root override `--project-dir PATH`). Only `version: 1`. Strict: unknown fields
rejected. Full example: `tora.yaml.example`.

- `command` is an argv array, never a shell string. No implicit shell.
- `working_dir` default `.`, resolved against the project dir; must exist.
- `depends_on` means **startup order**, not readiness (see Limits).
- `restart`: `no` (default) or `on-failure` (bounded, best-effort — see Limits).
- Service env = parent environment overridden by service `env`. Values are
  never printed by any command.

## Commands

- `tora init [--name X] [--force]` — never overwrites without `--force`; adds `.tora/` to `.gitignore`.
- `tora up [SERVICE]` — validate → start in dependency order (parallel per
  level); with SERVICE, only it plus its dependencies. Failed startup rolls
  back services started by the call. Partial `up` while dependents run is safe.
- `tora down` — reverse dependency order, SIGTERM → wait `shutdown_timeout` → SIGKILL fallback.
- `tora ps [--json]` — live status verified against `/proc`, not just state.
- `tora start|stop|restart SERVICE` — `stop`/`restart` refuse while a
  dependent is still RUNNING (explicit error; use `down` for full shutdown).
- `tora logs SERVICE [--tail N] [--follow]`, `tora logs --all [--follow]`.
- `tora config validate` — exit 2 on invalid config, spawns nothing.
- `tora version` — no config required.

Global flags: `-c/--config`, `--project-dir`, `--no-color`, `--json`, `-v/--verbose`.

Exit codes: `0` ok, `1` runtime/operation failure (incl. project lock held),
`2` invalid config/args, `130` cancelled via SIGINT/SIGTERM.

## How it works

- Each service leads its own Linux process group (`Setpgid`); signals go to
  the verified group, never to a bare PID.
- Ownership = PID alive **and** same kernel starttime tick **and** same PGID.
  Stale entries are pruned; foreign PIDs are never signaled.
- State in `<project>/.tora/state.json` (0600, atomic tmp+rename, corrupt file
  backed up as `state.json.corrupt.<unix>`). Mutations hold an `flock`
  exclusive lock (`.tora/lock`); a second manager exits 1 with a clear message.
- Logs in `<project>/.tora/logs/<service>.log` (stdout+stderr appended,
  rotation at 10 MiB keeping 3 backups). Display adds `[service]`/timestamp
  prefixes; stored bytes are untouched.
- `on-failure` restart (max 5, backoff 1s→30s) is reconciled on `up`/`start`
  invocations — there is no background daemon in v0.1.

## Limits (v0.1, honest)

- Dependency = startup order, not app readiness. A `docker compose up <svc>`
  child that exits immediately is reported as EXITED/FAILED as observed; Tora
  does not infer container health.
- No daemon: if no `tora` invocation is running, nothing restarts dead
  processes. Unexpected exits surface as FAILED/EXITED on the next command
  (exit code of reparented processes is unknowable; recorded as -1).
- `ps` shows SERVICE/STATUS/PID/STARTED. Tora does not claim port ownership
  and performs no health checks or resource metering.
- `restart --all`, TUI, log search, watch mode: not in v0.1.
- Linux only.

## Security

No shell interpolation, no sudo, no remote execution. Only verified process
groups are signaled. Env values never logged. State/lock files 0600/0700.
`tora.yaml` is trusted input: commands run with your UID — review it like code.

## Troubleshooting

- `another tora instance is managing this project` — a mutating command is
  already running; wait or check for a stuck `tora up/down`.
- Service FAILED at startup — read `.tora/logs/<service>.log`; common causes:
  binary missing (`command not found`), bad `working_dir`, immediate non-zero
  exit (reported with the code).
- `refusing to ... identity mismatch` — state points at a recycled PID; run
  any command to let `ps`/reconcile prune it, then retry.
- `dependency cycle detected` — fix `depends_on` (e.g. api↔worker).

## Development

```bash
make build
make vet
make test        # unit, no root needed
make race
go test -count=1 ./tests/integration/   # real processes, Linux
```

Ecosystem: Kitsune (env setup) · Ookami (machine health) · Tanuki (runtime &
dependency provisioning) · **Tora (process lifecycle)**. Tanuki prepares the
project; Tora runs it.
