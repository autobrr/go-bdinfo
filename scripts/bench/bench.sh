#!/usr/bin/env bash
# Whole-disc benchmark: go-bdinfo vs bdinfo-rs vs official BDInfo.
# Go and Rust run REPS times each with alternated order; the official binary runs
# once per disc as the parity reference. The page cache of the disc is evicted
# before every run. See docs/benchmarks.md. For a Go-vs-official parity gate
# use scripts/speed_parity_loop.sh instead.
#
# Layout of $BENCH_DIR (default /tmp/bdinfo-bench):
#   bin/bdinfo-go  bin/bdinfo-rs   the two binaries under test
#   discs.txt                      one "name|path" per line; path is a disc folder or .iso
#   stat.py  evict.py              copied from this directory
# Output: results.tsv and out/<disc>/<tool>-<rep>/{report.txt,log}.
set -u
B=${BENCH_DIR:-/tmp/bdinfo-bench}
BIN=$B/bin; OUT=$B/out
OFF=${BDINFO_OFFICIAL_BIN:-/root/github/oss/bdinfo-official/bdinfo_linux_v2.0.5_extracted/BDInfo}
REPS=${REPS:-3}
RES=$B/results.tsv
printf 'disc\ttool\trep\twall_s\tuser_s\tsys_s\tmaxrss_kb\trc\n' > "$RES"

drop() { sync; python3 $B/evict.py "$1"; }
ts() { date '+%H:%M:%S'; }

run_go() {
  python3 $B/stat.py "$2/log" -- $BIN/bdinfo-go -p "$1" -o "$2/report.txt" \
    --filterloopingplaylists=true --filtershortplaylist=true --filtershortplaylistvalue=20 \
    --generatestreamdiagnostics=true --extendedstreamdiagnostics=false --includeversionandnotes=false
}
run_rs() {
  python3 $B/stat.py "$2/log" -- $BIN/bdinfo-rs --no-banner --whole "$1" "$2"
}
run_off() {
  python3 $B/stat.py "$2/log" -- "$OFF" -p "$1" -o "$2/report.txt" \
    -b true -y true -v 20 -l true -k false -g true -e false -j false -m true -q false
}

while IFS='|' read -r name path; do
  case "$name" in ""|\#*) continue;; esac
  for rep in $(seq 1 $REPS); do
    if (( rep % 2 == 1 )); then order="go rs"; else order="rs go"; fi
    for tool in $order; do
      d=$OUT/$name/$tool-$rep; mkdir -p "$d"
      drop "$path"
      echo "$(ts) start $name $tool $rep"
      line=$(run_$tool "$path" "$d")
      printf '%s\t%s\t%s\t%s\n' "$name" "$tool" "$rep" "$line" >> "$RES"
      echo "$(ts) end   $name $tool $rep  $line"
    done
  done
  case "$path" in *.iso|*.ISO) continue;; esac
  d=$OUT/$name/off-1; mkdir -p "$d"
  drop "$path"
  echo "$(ts) start $name off 1"
  line=$(run_off "$path" "$d")
  printf '%s\t%s\t%s\t%s\n' "$name" "off" "1" "$line" >> "$RES"
  echo "$(ts) end   $name off 1  $line"
done < $B/discs.txt
echo "$(ts) ALL DONE"
