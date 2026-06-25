package bdrom

import (
	"reflect"
	"testing"

	"github.com/autobrr/go-bdinfo/internal/stream"
)

// updateStreamBitratesMapRef is a verbatim copy of the pre-optimization map-ranging
// implementation of updateStreamBitrates. It is kept here purely to differentially
// validate that the activeStates-slice version visits the same set of (pid, state)
// entries and produces byte-identical output regardless of iteration order. Map
// iteration is randomized, so the original code already had to be order-independent;
// this test pins that property and proves the slice version preserves it.
func (s *StreamFile) updateStreamBitratesMapRef(playlists []*PlaylistFile, clipTargets []scanClipTarget, clipCursor *clipTargetCursor, states map[uint16]*streamState, ptsPID uint16, pts uint64, ptsDiff int64) {
	if playlists == nil {
		return
	}
	for pid, state := range states {
		if state.windowPackets == 0 {
			continue
		}
		if base, ok := s.Streams[pid]; ok {
			if base.Base().IsVideoStream() && pid != ptsPID {
				continue
			}
		}
		s.updateStreamBitrate(clipTargets, clipCursor, pid, pts, ptsDiff, state)
	}
}

type bitrateSeed struct {
	pid           uint16
	known         bool // present in s.Streams (and clip target streams)
	video         bool
	windowBytes   uint64
	windowPackets uint64
}

func newBitrateStreamInfo(pid uint16, video bool) stream.Info {
	if video {
		return &stream.VideoStream{Stream: stream.Stream{PID: pid, StreamType: stream.StreamTypeAVCVideo}}
	}
	return &stream.AudioStream{Stream: stream.Stream{PID: pid, StreamType: stream.StreamTypeAC3Audio}}
}

// buildBitrateWorld constructs an independent StreamFile + clip targets + states from
// the seeds. Each call returns a fresh, deeply-independent world so the map-ref and
// slice versions can run side by side without sharing mutable accumulators.
func buildBitrateWorld(seeds []bitrateSeed) (*StreamFile, []scanClipTarget, map[uint16]*streamState) {
	s := &StreamFile{
		Streams:           map[uint16]stream.Info{},
		StreamDiagnostics: map[uint16][]StreamDiagnostics{},
	}
	targetStreams := map[uint16]stream.Info{}
	states := map[uint16]*streamState{}
	for _, seed := range seeds {
		if seed.known {
			s.Streams[seed.pid] = newBitrateStreamInfo(seed.pid, seed.video)
			targetStreams[seed.pid] = newBitrateStreamInfo(seed.pid, seed.video)
		}
		states[seed.pid] = &streamState{
			windowBytes:        seed.windowBytes,
			windowPackets:      seed.windowPackets,
			streamTag:          "I",
			collectDiagnostics: true,
		}
	}
	clip := &StreamClip{Name: "00000.m2ts", TimeIn: 0, TimeOut: 1e9}
	clipTargets := []scanClipTarget{{clip: clip, streams: targetStreams}}
	return s, clipTargets, states
}

func activeStatesFromSeeds(seeds []bitrateSeed, states map[uint16]*streamState) []activeStreamEntry {
	out := make([]activeStreamEntry, 0, len(seeds))
	for _, seed := range seeds {
		out = append(out, activeStreamEntry{
			pid:     seed.pid,
			state:   states[seed.pid],
			isVideo: seed.known && seed.video,
		})
	}
	return out
}

type bitrateSnapshot struct {
	clipPayload   uint64
	clipPackets   uint64
	clipSeconds   float64
	streamPayload map[uint16]uint64
	streamPackets map[uint16]uint64
	streamSeconds map[uint16]float64
	streamBitrate map[uint16]int64
	targetPayload map[uint16]uint64
	targetPackets map[uint16]uint64
	diagnostics   map[uint16][]StreamDiagnostics
	windowPackets map[uint16]uint64
}

func snapshotBitrateWorld(s *StreamFile, clipTargets []scanClipTarget, states map[uint16]*streamState) bitrateSnapshot {
	snap := bitrateSnapshot{
		streamPayload: map[uint16]uint64{},
		streamPackets: map[uint16]uint64{},
		streamSeconds: map[uint16]float64{},
		streamBitrate: map[uint16]int64{},
		targetPayload: map[uint16]uint64{},
		targetPackets: map[uint16]uint64{},
		diagnostics:   map[uint16][]StreamDiagnostics{},
		windowPackets: map[uint16]uint64{},
	}
	clip := clipTargets[0].clip
	snap.clipPayload = clip.PayloadBytes
	snap.clipPackets = clip.PacketCount
	snap.clipSeconds = clip.PacketSeconds
	for pid, info := range s.Streams {
		b := info.Base()
		snap.streamPayload[pid] = b.PayloadBytes
		snap.streamPackets[pid] = b.PacketCount
		snap.streamSeconds[pid] = b.PacketSeconds
		snap.streamBitrate[pid] = b.ActiveBitRate
	}
	for pid, info := range clipTargets[0].streams {
		b := info.Base()
		snap.targetPayload[pid] = b.PayloadBytes
		snap.targetPackets[pid] = b.PacketCount
	}
	for pid, d := range s.StreamDiagnostics {
		if len(d) > 0 {
			snap.diagnostics[pid] = d
		}
	}
	for pid, st := range states {
		snap.windowPackets[pid] = st.windowPackets
	}
	return snap
}

// TestUpdateStreamBitrates_SliceMatchesMap proves the activeStates-slice version of
// updateStreamBitrates is output-equivalent to the original map-ranging version across
// every branch (ptsPID video, non-ptsPID video skip, audio, zero-window skip, and an
// unknown PID not in s.Streams) and independent of iteration order.
func TestUpdateStreamBitrates_SliceMatchesMap(t *testing.T) {
	seeds := []bitrateSeed{
		{pid: 0x1011, known: true, video: true, windowBytes: 4096, windowPackets: 30}, // ptsPID video -> processed
		{pid: 0x1012, known: true, video: true, windowBytes: 2048, windowPackets: 15}, // other video -> skipped
		{pid: 0x1100, known: true, video: false, windowBytes: 800, windowPackets: 6},  // audio -> processed
		{pid: 0x1101, known: true, video: false, windowBytes: 0, windowPackets: 0},    // zero window -> skipped
		{pid: 0xFFFF, known: false, video: false, windowBytes: 512, windowPackets: 4}, // unknown PID -> clip-only
	}
	const ptsPID = uint16(0x1011)
	const pts = uint64(180000)
	const ptsDiff = int64(3000)
	playlists := []*PlaylistFile{} // non-nil so updateStreamBitrates does not short-circuit

	// Reference world: original map-ranging implementation.
	sRef, ctRef, statesRef := buildBitrateWorld(seeds)
	sRef.updateStreamBitratesMapRef(playlists, ctRef, nil, statesRef, ptsPID, pts, ptsDiff)
	want := snapshotBitrateWorld(sRef, ctRef, statesRef)

	// Sanity: the unknown PID and both processed streams must have flowed into the clip
	// totals (otherwise the test would pass vacuously).
	if want.clipPayload != 4096+800+512 || want.clipPackets != 30+6+4 {
		t.Fatalf("reference world clip totals unexpected: payload=%d packets=%d", want.clipPayload, want.clipPackets)
	}

	// The new slice implementation must match the map reference for every permutation
	// of the active-states order.
	perms := [][]int{
		{0, 1, 2, 3, 4},
		{4, 3, 2, 1, 0},
		{2, 4, 0, 3, 1},
		{1, 3, 0, 4, 2},
		{3, 0, 4, 2, 1},
	}
	for pi, perm := range perms {
		ordered := make([]bitrateSeed, len(perm))
		for i, idx := range perm {
			ordered[i] = seeds[idx]
		}
		s2, ct2, states2 := buildBitrateWorld(seeds)
		active := activeStatesFromSeeds(ordered, states2)
		s2.updateStreamBitrates(playlists, ct2, nil, active, ptsPID, pts, ptsDiff)
		got := snapshotBitrateWorld(s2, ct2, states2)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("permutation %d %v: slice impl diverged from map reference\n got=%+v\nwant=%+v", pi, perm, got, want)
		}
	}
}
