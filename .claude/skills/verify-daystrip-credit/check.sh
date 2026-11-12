#!/usr/bin/env bash
# Run the daystrip credit verification and sort each result into:
#   PASS, KNOWN FINDING (still failing the way the skill records), FIXED? (a
#   known finding no longer fails: update the skill), or FAIL (new break).
# Exit 0 unless a must-pass check fails or a known finding fails differently.
set -uo pipefail
REPO=$(git -C "$(dirname "$0")" rev-parse --show-toplevel)
cd "$REPO"
PKG=./cmd/prototype-daystrip
rc=0

run() { go test -tags verify -count=1 -v -run "^$1\$" "$PKG" 2>&1; }

for t in TestCreditSeededDay TestCreditGeneratedDays TestColumnsSeededDay TestCreditOverlappingMeetings; do
  out=$(run "$t")
  if grep -q -- "^--- PASS: $t" <<<"$out"; then
    echo "PASS          $t $(grep -o 'checked [0-9]* reachable states' <<<"$out")"
  else
    echo "FAIL          $t  (must pass: a real break, or a modelling error; see the skill)"; echo "$out" | sed 's/^/    /'; rc=1
  fi
done

# name|signature that the recorded counterexample prints
known=()
for k in ${known[@]+"${known[@]}"}; do
  IFS='|' read -r t id sig <<<"$k"
  out=$(run "$t")
  if grep -q -- "^--- PASS: $t" <<<"$out"; then
    echo "FIXED?        $t ($id no longer reproduces: confirm the fix, then move it to the must-pass list in check.sh and the skill)"
  elif grep -qF -- "$sig" <<<"$out"; then
    echo "KNOWN FINDING $t ($id)"
  else
    echo "FAIL          $t ($id fails differently than recorded)"; echo "$out" | sed 's/^/    /'; rc=1
  fi
done
exit $rc
