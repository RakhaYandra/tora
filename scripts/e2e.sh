#!/usr/bin/env bash
# End-to-end: build, validate, up, ps, logs, restart, down, no leftovers.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BIN="$ROOT/bin/tora"
WORK="$(mktemp -d)"
APPPORT=18081

cleanup() {
  "$BIN" down --project-dir "$WORK" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

go -C "$ROOT" build -o "$BIN" ./cmd/tora
mkdir -p "$WORK/app"
cp "$ROOT/tests/fixtures/health/main.go" "$WORK/app/main.go"
cat > "$WORK/tora.yaml" <<EOF
version: 1
project:
  name: torae2e
runtime:
  shutdown_timeout: 3s
  log_dir: .tora/logs
  state_dir: .tora
services:
  db:
    command: ["sleep", "300"]
  api:
    command: ["go", "run", "./main.go"]
    working_dir: ./app
    depends_on: ["db"]
    env:
      PORT: "$APPPORT"
EOF

"$BIN" config validate --project-dir "$WORK"
"$BIN" up --project-dir "$WORK" >"$WORK/up.out" 2>&1
grep -q "api.*running" "$WORK/up.out"
"$BIN" ps --project-dir "$WORK" | grep -q "RUNNING"
sleep 8 # go run compile + boot
curl -fsS "http://127.0.0.1:$APPPORT/health" | grep -q ok
"$BIN" logs api --tail 2 --project-dir "$WORK" | grep -q "listening on"
# Guard assertions: these commands exit non-zero by design; don't let
# pipefail mask the grep, so capture first.
"$BIN" restart db --project-dir "$WORK" >"$WORK/guard.out" 2>&1 || true
grep -q "still running" "$WORK/guard.out"
"$BIN" restart api --project-dir "$WORK" | grep -q "running"
"$BIN" stop api --project-dir "$WORK" | grep -q "stopped"
"$BIN" stop db --project-dir "$WORK" | grep -q "stopped"
"$BIN" down --project-dir "$WORK"
# No sleep 300 may survive (scan by exact comm, never pattern-match own shell).
LEAK=0
for p in $(pgrep -x sleep || true); do
  [ "$p" = "$$" ] && continue
  if tr '\0' ' ' < "/proc/$p/cmdline" 2>/dev/null | grep -q "sleep 300"; then
    echo "LEAKED sleep pid $p"
    LEAK=1
  fi
done
[ "$LEAK" = 0 ] || exit 1

echo "E2E OK"
