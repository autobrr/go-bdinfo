#!/usr/bin/env bash
set -euo pipefail
# bash `time` prints the decimal point of the locale, and awk reads only ".".
export LC_ALL=C

usage() {
  cat <<'EOF'
Usage:
  scripts/speed_parity_loop.sh --disc <path> [--reps N] [--official <path> | --baseline <bin>] [--ours <bin>] [--outdir <dir>] [--timeout-sec N]

Notes:
  - Runs a reference and go-bdinfo with matched default toggles. The reference
    is official BDInfo, or with --baseline another go-bdinfo binary, for
    example one built from main.
  - --ours uses a prebuilt binary and skips the build, for a host with no Go
    toolchain.
  - Alternates run order per rep to reduce cache/order bias.
  - Verifies parity (`diff -u --text`) each rep.
  - Prints median CPU time (user + system) and wall time, and their ratios
    (ours/reference).
EOF
}

DISC=""
REPS=3
OFFICIAL="${BDINFO_OFFICIAL_BIN:-/root/github/oss/bdinfo-official/bdinfo_linux_v2.0.5_extracted/BDInfo}"
BASELINE=""
OURS_BIN=""
OUTDIR="/tmp/bdinfo-speed-loop"
TIMEOUT_SEC=3600

while [[ $# -gt 0 ]]; do
  case "$1" in
    --disc)
      DISC="$2"
      shift 2
      ;;
    --reps)
      REPS="$2"
      shift 2
      ;;
    --official)
      OFFICIAL="$2"
      shift 2
      ;;
    --baseline)
      BASELINE="$2"
      shift 2
      ;;
    --ours)
      OURS_BIN="$2"
      shift 2
      ;;
    --outdir)
      OUTDIR="$2"
      shift 2
      ;;
    --timeout-sec)
      TIMEOUT_SEC="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown arg: $1" >&2
      usage
      exit 2
      ;;
  esac
done

if [[ -z "$DISC" ]]; then
  echo "--disc is required" >&2
  usage
  exit 2
fi
if [[ ! -e "$DISC" ]]; then
  echo "disc path missing: $DISC" >&2
  exit 2
fi
if [[ -n "$BASELINE" ]]; then
  REF_BIN="$BASELINE"
  REF_NAME="baseline"
else
  REF_BIN="$OFFICIAL"
  REF_NAME="official"
fi
if [[ ! -x "$REF_BIN" ]]; then
  echo "$REF_NAME binary missing or not executable: $REF_BIN" >&2
  exit 2
fi
if [[ -n "$OURS_BIN" && ! -x "$OURS_BIN" ]]; then
  echo "ours binary missing or not executable: $OURS_BIN" >&2
  exit 2
fi
if ! command -v timeout >/dev/null 2>&1; then
  echo "timeout command required" >&2
  exit 2
fi

mkdir -p "$OUTDIR"
if [[ -z "$OURS_BIN" ]]; then
  OURS_BIN="$OUTDIR/bdinfo"
  echo "building ours -> $OURS_BIN"
  go build -o "$OURS_BIN" ./cmd/bdinfo
fi

# Prints "<wall ms> <cpu ms>" for one run. CPU is the user plus system time of
# the run and its child processes.
run_timed() {
  local log="$1"
  shift
  local TIMEFORMAT='%3R %3U %3S' t
  t="$( { time timeout "${TIMEOUT_SEC}s" "$@" >"$log" 2>&1; } 2>&1 )" || {
    echo "run failed or timed out: $1 (see $log)" >&2
    return 1
  }
  awk '{ printf "%d %d\n", $1 * 1000, ($2 + $3) * 1000 }' <<<"$t"
}

median_ms() {
  if [[ $# -eq 0 ]]; then
    echo 0
    return
  fi
  local sorted n mid a b
  sorted="$(printf '%s\n' "$@" | sort -n)"
  n="$#"
  if (( n % 2 == 1 )); then
    mid=$((n / 2 + 1))
    echo "$sorted" | sed -n "${mid}p"
  else
    mid=$((n / 2))
    a="$(echo "$sorted" | sed -n "${mid}p")"
    b="$(echo "$sorted" | sed -n "$((mid + 1))p")"
    awk -v x="$a" -v y="$b" 'BEGIN { printf "%.0f\n", (x+y)/2 }'
  fi
}

off_args=(
  -p "$DISC"
  -b true
  -y true
  -v 20
  -l false
  -k false
  -g true
  -e false
  -j false
  -m true
  -q false
)

ours_args=(
  -p "$DISC"
  --enablessif=true
  --filtershortplaylist=true
  --filtershortplaylistvalue=20
  --filterloopingplaylists=false
  --keepstreamorder=false
  --generatestreamdiagnostics=true
  --extendedstreamdiagnostics=false
  --groupbytime=false
  --generatetextsummary=true
  --includeversionandnotes=false
)

if [[ -n "$BASELINE" ]]; then
  ref_args=("${ours_args[@]}")
else
  ref_args=("${off_args[@]}")
fi

declare -a ref_wall=() ref_cpu=() ours_wall=() ours_cpu=()

for rep in $(seq 1 "$REPS"); do
  rep_dir="$OUTDIR/rep-$rep"
  mkdir -p "$rep_dir"

  ref_out="$rep_dir/$REF_NAME.txt"
  ours_out="$rep_dir/ours.txt"
  ref_log="$rep_dir/$REF_NAME.log"
  ours_log="$rep_dir/ours.log"

  echo "rep $rep/$REPS"
  # An assignment, so a failed or timed-out run stops the loop.
  if (( rep % 2 == 1 )); then
    ref_t="$(run_timed "$ref_log" "$REF_BIN" "${ref_args[@]}" -o "$ref_out")"
    ours_t="$(run_timed "$ours_log" "$OURS_BIN" "${ours_args[@]}" -o "$ours_out")"
  else
    ours_t="$(run_timed "$ours_log" "$OURS_BIN" "${ours_args[@]}" -o "$ours_out")"
    ref_t="$(run_timed "$ref_log" "$REF_BIN" "${ref_args[@]}" -o "$ref_out")"
  fi
  read -r rw rc <<<"$ref_t"
  read -r ow oc <<<"$ours_t"

  ref_wall+=("$rw")
  ref_cpu+=("$rc")
  ours_wall+=("$ow")
  ours_cpu+=("$oc")

  diff_file="$rep_dir/diff.txt"
  if ! diff -u --text "$ref_out" "$ours_out" >"$diff_file"; then
    echo "parity mismatch on rep $rep (see $diff_file)" >&2
    exit 1
  fi

  awk -v n="$REF_NAME" -v rc="$rc" -v oc="$oc" -v rw="$rw" -v ow="$ow" \
    'BEGIN { printf "  cpu %s=%dms ours=%dms ratio=%.4f | wall %s=%dms ours=%dms ratio=%.4f\n", n, rc, oc, oc/rc, n, rw, ow, ow/rw }'
done

ref_cpu_med="$(median_ms "${ref_cpu[@]}")"
ours_cpu_med="$(median_ms "${ours_cpu[@]}")"
ref_wall_med="$(median_ms "${ref_wall[@]}")"
ours_wall_med="$(median_ms "${ours_wall[@]}")"

echo
echo "summary (median of $REPS reps)"
awk -v n="$REF_NAME" -v r="$ref_cpu_med" -v o="$ours_cpu_med" 'BEGIN { printf "  cpu  %s=%dms ours=%dms ratio(ours/%s)=%.4f\n", n, r, o, n, o/r }'
awk -v n="$REF_NAME" -v r="$ref_wall_med" -v o="$ours_wall_med" 'BEGIN { printf "  wall %s=%dms ours=%dms ratio(ours/%s)=%.4f\n", n, r, o, n, o/r }'
echo "  output-dir=$OUTDIR"
