#!/usr/bin/env bash
# Drive the daystrip prototype TUI in an isolated tmux server and record evidence.
# Usage: daystrip.sh start [cols] [rows] | doctor | keys <key>... | screen | snap <name>
#        | resize <cols> <rows> | stop
# Every command except `start` needs DAYSTRIP_RUN (printed by `start`).
set -euo pipefail

REPO=$(git -C "$(dirname "$0")" rev-parse --show-toplevel)
cmd=${1:-}; shift || true

if [ "$cmd" = start ]; then
  DAYSTRIP_RUN=$(date +%Y%m%d-%H%M%S)-$$
fi
: "${DAYSTRIP_RUN:?set DAYSTRIP_RUN to the id printed by 'daystrip.sh start'}"

SOCK="verify-daystrip-$DAYSTRIP_RUN"                       # our own tmux server: never touches yours
SCRATCH="${TMPDIR:-/tmp}/verify-daystrip-$DAYSTRIP_RUN"     # binary + build stamp, removed by stop
EVID="$REPO/.verify/daystrip/$DAYSTRIP_RUN"                 # evidence, survives stop
T() { tmux -L "$SOCK" "$@"; }

plain() { T capture-pane -p -t app; }

# Wait until the screen stops changing (the app redraws on each key, no timers).
settle() {
  local a b i
  a=$(plain)
  for i in $(seq 1 40); do
    sleep 0.1; b=$(plain)
    [ "$a" = "$b" ] && return 0
    a=$b
  done
  echo "screen never settled" >&2; return 1
}

log() { mkdir -p "$EVID"; printf '%s %s\n' "$(date +%H:%M:%S)" "$*" >> "$EVID/actions.log"; }

case "$cmd" in
start)
  cols=${1:-120}; rows=${2:-36}
  mkdir -p "$SCRATCH" "$EVID"
  (cd "$REPO" && go build -o "$SCRATCH/daystrip" ./cmd/prototype-daystrip)
  rev="$(git -C "$REPO" rev-parse --short HEAD)$([ -z "$(git -C "$REPO" status --porcelain -- cmd go.mod go.sum)" ] || echo -dirty)"
  echo "$rev" > "$SCRATCH/build-rev"
  T new-session -d -s app -x "$cols" -y "$rows" \
    "env TERM=xterm-256color COLORTERM=truecolor '$SCRATCH/daystrip'; echo '[daystrip exited' \$?']'; sleep 86400"
  for i in $(seq 1 100); do
    s=$(plain)
    if grep -q 'Tue 10 Nov · draft' <<<"$s" || grep -q 'PROTOTYPE: needs' <<<"$s"; then break; fi
    sleep 0.1
  done
  settle
  log "start ${cols}x${rows} build=$rev"
  echo "export DAYSTRIP_RUN=$DAYSTRIP_RUN"
  echo "evidence: $EVID"
  ;;
doctor)
  ok=1
  T has-session -t app 2>/dev/null || { echo "FAIL no tmux session on socket $SOCK"; exit 1; }
  pid=$(T display -p -t app '#{pane_pid}')
  if pgrep -P "$pid" -f "$SCRATCH/daystrip" >/dev/null; then echo "ok   app process running (pane pid $pid)"
  else echo "FAIL app process not running (exited? see screen)"; ok=0; fi
  built=$(cat "$SCRATCH/build-rev" 2>/dev/null || echo unknown)
  now="$(git -C "$REPO" rev-parse --short HEAD)$([ -z "$(git -C "$REPO" status --porcelain -- cmd go.mod go.sum)" ] || echo -dirty)"
  if [ "$built" = "$now" ] && [ "${now%-dirty}" = "$now" ]; then echo "ok   build $built matches checkout"
  elif [ "$built" = "$now" ]; then echo "warn build $built: source has uncommitted edits; restart after further edits"
  else echo "FAIL build $built != checkout $now: stop and start again"; ok=0; fi
  echo "info size $(T display -p -t app '#{window_width}x#{window_height}')"
  if plain | grep -q 'Tue 10 Nov · draft'; then echo "ok   main screen drawn"
  elif plain | grep -q 'PROTOTYPE: needs'; then echo "warn size guard shown (needs 96x30+)"
  else echo "FAIL neither main screen nor size guard on screen"; ok=0; fi
  [ $ok = 1 ]
  ;;
keys)
  [ $# -gt 0 ] || { echo "keys needs at least one tmux key name" >&2; exit 2; }
  for k in "$@"; do T send-keys -t app -- "$k"; done
  settle
  log "keys $*"
  ;;
screen) plain ;;
snap)
  name=${1:?snap needs a name}
  mkdir -p "$EVID"
  n=$(printf '%02d' $(( $(ls "$EVID"/*.txt 2>/dev/null | wc -l) + 1 )))
  plain > "$EVID/$n-$name.txt"
  T capture-pane -p -e -t app > "$EVID/$n-$name.ansi"
  log "snap $n-$name"
  echo "$EVID/$n-$name.txt"
  ;;
resize)
  T resize-window -t app -x "${1:?cols}" -y "${2:?rows}"
  settle
  log "resize $1x$2"
  ;;
stop)
  T kill-server 2>/dev/null || true
  rm -rf "$SCRATCH"
  log "stop"
  echo "stopped $DAYSTRIP_RUN; evidence kept in $EVID"
  ;;
*) sed -n '2,5p' "$0"; exit 2 ;;
esac
