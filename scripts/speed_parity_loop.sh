#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  scripts/speed_parity_loop.sh --disc <path> [--reps N] [--official <path>] [--outdir <dir>] [--timeout-sec N]

Notes:
  - Runs official BDInfo and go-bdinfo with matched default toggles.
  - Alternates run order per rep to reduce cache/order bias.
  - Verifies parity (`diff -u --text`) each rep.
  - Prints median wall-time ratio (ours/off).
EOF
}

DISC=""
REPS=3
OFFICIAL="${BDINFO_OFFICIAL_BIN:-/root/github/oss/bdinfo-official/bdinfo_linux_v2.0.5_extracted/BDInfo}"
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
if [[ ! -x "$OFFICIAL" ]]; then
  echo "official binary missing or not executable: $OFFICIAL" >&2
  exit 2
fi
if ! command -v timeout >/dev/null 2>&1; then
  echo "timeout command required" >&2
  exit 2
fi

mkdir -p "$OUTDIR"
OURS_BIN="$OUTDIR/bdinfo"

echo "building ours -> $OURS_BIN"
go build -o "$OURS_BIN" ./cmd/bdinfo

ms_now() {
  date +%s%3N
}

run_timed() {
  local log="$1"
  shift
  local start end
  start="$(ms_now)"
  timeout "${TIMEOUT_SEC}s" "$@" >"$log" 2>&1
  end="$(ms_now)"
  echo $((end - start))
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

declare -a off_times=()
declare -a ours_times=()

for rep in $(seq 1 "$REPS"); do
  rep_dir="$OUTDIR/rep-$rep"
  mkdir -p "$rep_dir"

  off_out="$rep_dir/official.txt"
  ours_out="$rep_dir/ours.txt"
  off_log="$rep_dir/official.log"
  ours_log="$rep_dir/ours.log"

  echo "rep $rep/$REPS"
  if (( rep % 2 == 1 )); then
    off_t="$(run_timed "$off_log" "$OFFICIAL" "${off_args[@]}" -o "$off_out")"
    ours_t="$(run_timed "$ours_log" "$OURS_BIN" "${ours_args[@]}" -o "$ours_out")"
  else
    ours_t="$(run_timed "$ours_log" "$OURS_BIN" "${ours_args[@]}" -o "$ours_out")"
    off_t="$(run_timed "$off_log" "$OFFICIAL" "${off_args[@]}" -o "$off_out")"
  fi

  off_times+=("$off_t")
  ours_times+=("$ours_t")

  diff_file="$rep_dir/diff.txt"
  if ! diff -u --text "$off_out" "$ours_out" >"$diff_file"; then
    echo "parity mismatch on rep $rep (see $diff_file)" >&2
    exit 1
  fi

  awk -v o="$off_t" -v r="$ours_t" 'BEGIN { printf "  official=%dms ours=%dms ratio=%.4f\n", o, r, r/o }'
done

off_med="$(median_ms "${off_times[@]}")"
ours_med="$(median_ms "${ours_times[@]}")"
ratio="$(awk -v o="$off_med" -v r="$ours_med" 'BEGIN { printf "%.4f", r/o }')"

echo
echo "summary (median of $REPS reps)"
echo "  official=${off_med}ms"
echo "  ours=${ours_med}ms"
echo "  ratio(ours/off)=${ratio}"
echo "  output-dir=$OUTDIR"
