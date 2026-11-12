#!/usr/bin/env bash
# sandbox.sh [command...]: try this checkout of sift without touching your
# real data. It builds sift into a sandbox directory, points XDG_DATA_HOME
# and XDG_CONFIG_HOME there, and runs the command (default: a shell) with
# that sift first on PATH and `claude` loading this checkout's plugin.
#
#   scripts/sandbox.sh                 # a shell; type sift, claude, exit
#   scripts/sandbox.sh sift demo       # one command
#   SIFT_SANDBOX=~/sift-try scripts/sandbox.sh   # keep the sandbox between runs
#
# Without SIFT_SANDBOX the sandbox is a fresh temp directory, removed on exit.
set -euo pipefail

root=$(git -C "$(dirname "$0")" rev-parse --show-toplevel)
box=${SIFT_SANDBOX:-}
if [ -z "$box" ]; then
  box=$(mktemp -d "${TMPDIR:-/tmp}/sift-sandbox.XXXXXX")
  trap 'rm -rf "$box"' EXIT
fi
mkdir -p "$box/bin" "$box/data" "$box/config"

(cd "$root" && go build -ldflags "-X github.com/rockylabsdev/sift/internal/cli.Version=$(git describe --tags --always --dirty 2>/dev/null || echo dev)" -o "$box/bin/sift" ./cmd/sift)

# claude, with this checkout's plugin loaded, for this sandbox only
real_claude=$(command -v claude || true)
if [ -n "$real_claude" ]; then
  printf '#!/bin/sh\nexec "%s" --plugin-dir "%s/plugin" "$@"\n' "$real_claude" "$root" > "$box/bin/claude"
  chmod +x "$box/bin/claude"
fi

export PATH="$box/bin:$PATH" XDG_DATA_HOME="$box/data" XDG_CONFIG_HOME="$box/config" SIFT_SANDBOX="$box"
echo "sift sandbox: $box ($(sift version))" >&2
[ -n "$real_claude" ] || echo "sift sandbox: claude is not on PATH, so the plugin is not loaded" >&2

if [ $# -eq 0 ]; then
  PS1="(sift sandbox) \w \$ " exec "${SHELL:-bash}" -i
fi
"$@"
