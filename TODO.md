# TODO

- perf: reduce allocations in stream scan (reuse slice pools per PID)
- perf: cap per-stream data by codec type (review values vs BDInfo)
- perf: skip CLPI parse when MPLS/CLPI already merged and stream metadata complete
- perf: optional "fast" mode (MPLS+CLPI only; skip M2TS)
- perf: add profile-guided worker auto-tune (disc size / stream count)
- perf: measure with multiple discs, keep bench history
- output: optional progress to stderr
- docs: add perf/bench section
