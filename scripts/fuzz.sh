#!/usr/bin/env bash
#
# scripts/fuzz.sh — run the Go fuzz targets for the scheduled fuzz job.
#
# Why this exists: when a `-fuzztime` window ends, Go's fuzzing coordinator
# waits for its workers to stop within an internal deadline. On loaded / shared
# CI runners a worker can miss that deadline and the run fails with a bare
#
#     --- FAIL: FuzzXxx (120.10s)
#         context deadline exceeded
#
# That is a shutdown race in the test harness, not a defect in the code under
# test: no reproducer is written and nothing is reproducible. This wrapper fails
# the job only on a *real* finding (a written crasher / panic / other failure)
# and tolerates a bare shutdown deadline — but only after a deterministic corpus
# replay confirms there is nothing reproducible to find.
#
# Usage:
#   scripts/fuzz.sh              # fuzz every target with the default window
#   FUZZTIME=30s scripts/fuzz.sh # shorter window (handy locally)
#
set -uo pipefail

FUZZTIME="${FUZZTIME:-2m}"
# The deterministic replay re-runs the seed corpus once. Give it its own,
# generous timeout so a genuinely slow input trips the replay (a real bug)
# rather than being mistaken for the harness shutdown race.
REPLAY_TIMEOUT="${REPLAY_TIMEOUT:-3m}"

# pkg|FuzzFunc — every target is fuzzed uniformly.
TARGETS=(
  "./internal/buffer|FuzzBitReader"
  "./internal/codec|FuzzScanAVC"
  "./internal/codec|FuzzScanHEVC"
  "./internal/codec|FuzzHEVCFrameTagFromTransfer"
  "./internal/bdrom|FuzzStreamClipFileScan"
  "./internal/bdrom|FuzzParsePTSAndValidateTimestamp"
)

# crasher_written reports whether a failed run produced a real reproducer:
# either Go logged that it wrote a failing input, or an untracked file appeared
# under a testdata/fuzz/<fn>/ corpus directory.
crasher_written() {
  local fn="$1" log="$2"
  if grep -q "Failing input written to" "$log"; then
    return 0
  fi
  git status --porcelain --untracked-files=all 2>/dev/null \
    | grep -Eq "testdata/fuzz/${fn}/"
}

# classify inspects a failed run's log (no side effects) and prints:
#   real  — a reproducer was written / panic-style finding
#   flake — only a bare "context deadline exceeded" shutdown race
#   other — anything else (build failure, unexpected error)
classify() {
  local fn="$1" log="$2"
  if crasher_written "$fn" "$log"; then
    echo real
  elif grep -q "context deadline exceeded" "$log"; then
    echo flake
  else
    echo other
  fi
}

# run_target fuzzes one target and returns 0 (clean / tolerated) or 1 (real bug).
run_target() {
  local pkg="$1" fn="$2" log rc
  log="$(mktemp)"

  echo "::group::fuzz ${fn} (${pkg}, fuzztime=${FUZZTIME})"
  go test "$pkg" -run='^$' -fuzz="^${fn}\$" -fuzztime="$FUZZTIME" 2>&1 | tee "$log"
  rc="${PIPESTATUS[0]}"
  echo "::endgroup::"

  if [ "$rc" -eq 0 ]; then
    echo "✅ ${fn}: clean"
    rm -f "$log"
    return 0
  fi

  case "$(classify "$fn" "$log")" in
    real)
      echo "❌ ${fn}: reproducer found — real finding"
      rm -f "$log"
      return 1
      ;;
    flake)
      echo "::warning title=fuzz shutdown race::${fn} ended with 'context deadline exceeded'; replaying corpus deterministically to rule out a real bug"
      if go test "$pkg" -run="^${fn}\$" -count=1 -timeout="$REPLAY_TIMEOUT"; then
        echo "✅ ${fn}: corpus replay clean — tolerated harness shutdown race"
        rm -f "$log"
        return 0
      fi
      echo "❌ ${fn}: corpus replay failed — real bug, not a flake"
      rm -f "$log"
      return 1
      ;;
    *)
      echo "❌ ${fn}: failed (exit ${rc}) for a non-tolerated reason"
      rm -f "$log"
      return 1
      ;;
  esac
}

main() {
  local overall=0 t
  for t in "${TARGETS[@]}"; do
    # Keep going after a real finding so the daily run surfaces them all.
    run_target "${t%%|*}" "${t##*|}" || overall=1
  done
  if [ "$overall" -ne 0 ]; then
    echo "fuzz: one or more targets reported a real finding"
  fi
  return "$overall"
}

# Only run when executed directly; sourcing (e.g. for tests) exposes the
# functions without kicking off the fuzz loop.
if [ "${BASH_SOURCE[0]}" = "${0}" ]; then
  main
  exit $?
fi
