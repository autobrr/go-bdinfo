package bdrom

import (
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"sync"

	"github.com/autobrr/go-bdinfo/internal/codec"
	"github.com/autobrr/go-bdinfo/internal/fs"
	"github.com/autobrr/go-bdinfo/internal/settings"
	"github.com/autobrr/go-bdinfo/internal/stream"
)

const (
	maxStreamDataVideo = 5 * 1024 * 1024
	maxStreamDataAudio = 256 * 1024
	maxStreamDataOther = 128 * 1024
	maxTSPID           = 8192
	unknownStatePID    = uint16(0xFFFF)
)

var (
	videoBufPool = sync.Pool{New: func() any { return make([]byte, 0, maxStreamDataVideo) }}
	audioBufPool = sync.Pool{New: func() any { return make([]byte, 0, maxStreamDataAudio) }}
	otherBufPool = sync.Pool{New: func() any { return make([]byte, 0, maxStreamDataOther) }}
)

func getCodecBuffer(capacity int) []byte {
	switch capacity {
	case maxStreamDataVideo:
		return videoBufPool.Get().([]byte)[:0]
	case maxStreamDataAudio:
		return audioBufPool.Get().([]byte)[:0]
	case maxStreamDataOther:
		return otherBufPool.Get().([]byte)[:0]
	default:
		return make([]byte, 0, capacity)
	}
}

func putCodecBuffer(buf []byte) {
	if buf == nil {
		return
	}
	switch cap(buf) {
	case maxStreamDataVideo:
		videoBufPool.Put(buf[:0])
	case maxStreamDataAudio:
		audioBufPool.Put(buf[:0])
	case maxStreamDataOther:
		otherBufPool.Put(buf[:0])
	}
}

type psiAssembler struct {
	active bool
	needed int
	buf    []byte
}

func (a *psiAssembler) appendPayload(payload []byte, payloadStart bool) ([]byte, bool) {
	if payloadStart {
		if len(payload) == 0 {
			return nil, false
		}
		pointer := int(payload[0])
		start := 1 + pointer
		if start > len(payload) {
			return nil, false
		}
		a.buf = append(a.buf[:0], payload[start:]...)
		a.needed = 0
		a.active = true
	} else {
		if !a.active || len(payload) == 0 {
			return nil, false
		}
		a.buf = append(a.buf, payload...)
	}
	if len(a.buf) >= 3 && a.needed == 0 {
		sectionLen := int(a.buf[1]&0x0F)<<8 | int(a.buf[2])
		a.needed = 3 + sectionLen
	}
	if a.needed > 0 && len(a.buf) >= a.needed {
		section := make([]byte, a.needed)
		copy(section, a.buf[:a.needed])
		a.active = false
		a.buf = a.buf[:0]
		a.needed = 0
		return section, true
	}
	return nil, false
}

func parsePATPMTPIDSection(section []byte) (uint16, bool) {
	if len(section) < 12 {
		return 0, false
	}
	if section[0] != 0x00 {
		return 0, false
	}
	sectionLen := int(section[1]&0x0F)<<8 | int(section[2])
	total := 3 + sectionLen
	if total > len(section) || total < 12 {
		return 0, false
	}
	end := total - 4 // exclude CRC32
	var fallbackPMTPID uint16
	hasFallback := false
	for i := 8; i+4 <= end; i += 4 {
		program := uint16(section[i])<<8 | uint16(section[i+1])
		pmtPID := uint16(section[i+2]&0x1F)<<8 | uint16(section[i+3])
		if program == 1 {
			return pmtPID, true
		}
		if program != 0 && !hasFallback {
			fallbackPMTPID = pmtPID
			hasFallback = true
		}
	}
	if hasFallback {
		return fallbackPMTPID, true
	}
	return 0, false
}

func detectPMTStreamOrder(fileInfo fs.FileInfo) ([]uint16, bool) {
	if fileInfo == nil {
		return nil, false
	}
	f, err := fileInfo.OpenRead()
	if err != nil {
		return nil, false
	}
	defer f.Close()

	first := make([]byte, 192)
	if _, err := io.ReadFull(f, first); err != nil {
		return nil, false
	}

	packetSize := 192
	syncOffset := 4
	if first[0] == 0x47 {
		packetSize = 188
		syncOffset = 0
	} else if first[4] == 0x47 {
		packetSize = 192
		syncOffset = 4
	} else {
		return nil, false
	}

	chunkSize := 5 * 1024 * 1024
	chunkSize -= chunkSize % packetSize
	if chunkSize < packetSize {
		chunkSize = packetSize
	}
	buf := make([]byte, chunkSize+packetSize)
	carryLen := len(first) - packetSize
	if carryLen > 0 {
		copy(buf, first[packetSize:])
	}

	pmtPID := uint16(0xFFFF)
	var patAssembler psiAssembler
	var pmtAssembler psiAssembler
	pmtSections := make(map[byte][]pmtStreamEntry)
	pmtLastSection := byte(0xFF)

	consumePMTSection := func(section []byte) {
		sectionNumber, lastSectionNumber, entries, ok := parsePMTSection(section)
		if !ok {
			return
		}
		if pmtLastSection == 0xFF {
			pmtLastSection = lastSectionNumber
		}
		if _, exists := pmtSections[sectionNumber]; !exists {
			pmtSections[sectionNumber] = entries
		}
	}
	processPacket := func(pkt []byte) {
		if len(pkt) <= syncOffset || pkt[syncOffset] != 0x47 {
			return
		}
		pid := (uint16(pkt[syncOffset+1]&0x1F) << 8) | uint16(pkt[syncOffset+2])
		adaptation := (pkt[syncOffset+3] >> 4) & 0x3
		if adaptation == 0 || adaptation == 2 {
			return
		}
		idx := syncOffset + 4
		if adaptation == 3 {
			if idx >= len(pkt) {
				return
			}
			idx += 1 + int(pkt[idx])
		}
		if idx >= len(pkt) {
			return
		}
		payload := pkt[idx:]
		if len(payload) == 0 {
			return
		}
		payloadStart := (pkt[syncOffset+1] & 0x40) != 0
		if payloadStart {
			if pid == 0 {
				if section, ok := patAssembler.appendPayload(payload, true); ok {
					if discoveredPMTPID, ok := parsePATPMTPIDSection(section); ok {
						pmtPID = discoveredPMTPID
					}
				}
			} else if pid == pmtPID {
				if section, ok := pmtAssembler.appendPayload(payload, true); ok {
					consumePMTSection(section)
				}
			}
		} else if pid == 0 {
			if section, ok := patAssembler.appendPayload(payload, false); ok {
				if discoveredPMTPID, ok := parsePATPMTPIDSection(section); ok {
					pmtPID = discoveredPMTPID
				}
			}
		} else if pid == pmtPID {
			if section, ok := pmtAssembler.appendPayload(payload, false); ok {
				consumePMTSection(section)
			}
		}
	}

	// First packet already read.
	processPacket(first[:packetSize])

	readCount := 0
	for {
		n, err := f.Read(buf[carryLen : carryLen+chunkSize])
		if n == 0 && err != nil {
			break
		}
		n += carryLen
		aligned := n - (n % packetSize)
		for i := 0; i+packetSize <= aligned; i += packetSize {
			processPacket(buf[i : i+packetSize])
		}
		carryLen = n - aligned
		if carryLen > 0 {
			copy(buf, buf[aligned:n])
		}
		readCount++
		if pmtLastSection != 0xFF && len(pmtSections) >= int(pmtLastSection)+1 {
			break
		}
		// PMT appears near the start; avoid scanning large portions when probing.
		if readCount >= 32 {
			break
		}
		if err != nil {
			break
		}
	}

	if pmtLastSection == 0xFF || len(pmtSections) < int(pmtLastSection)+1 {
		return nil, false
	}
	order := make([]uint16, 0, 8)
	seen := make(map[uint16]struct{}, 8)
	for sec := byte(0); sec <= pmtLastSection; sec++ {
		entries, ok := pmtSections[sec]
		if !ok {
			return nil, false
		}
		for _, entry := range entries {
			if _, exists := seen[entry.PID]; exists {
				continue
			}
			seen[entry.PID] = struct{}{}
			order = append(order, entry.PID)
		}
	}
	if len(order) == 0 {
		return nil, false
	}
	return order, true
}

type pmtStreamEntry struct {
	PID        uint16
	StreamType byte
}

func parsePMTSection(section []byte) (sectionNumber byte, lastSectionNumber byte, entries []pmtStreamEntry, ok bool) {
	if len(section) < 16 {
		return 0, 0, nil, false
	}
	if section[0] != 0x02 {
		return 0, 0, nil, false
	}
	sectionLen := int(section[1]&0x0F)<<8 | int(section[2])
	total := 3 + sectionLen
	if total > len(section) || total < 16 {
		return 0, 0, nil, false
	}
	sectionNumber = section[6]
	lastSectionNumber = section[7]
	programInfoLen := int(section[10]&0x0F)<<8 | int(section[11])
	idx := 12 + programInfoLen
	end := total - 4 // exclude CRC32
	if idx > end {
		return 0, 0, nil, false
	}
	entries = make([]pmtStreamEntry, 0, 8)
	for idx+5 <= end {
		streamType := section[idx]
		pid := uint16(section[idx+1]&0x1F)<<8 | uint16(section[idx+2])
		esInfoLen := int(section[idx+3]&0x0F)<<8 | int(section[idx+4])
		entries = append(entries, pmtStreamEntry{PID: pid, StreamType: streamType})
		idx += 5 + esInfoLen
	}
	if len(entries) == 0 {
		return 0, 0, nil, false
	}
	return sectionNumber, lastSectionNumber, entries, true
}

type InterleavedFile struct {
	FileInfo fs.FileInfo
	Name     string
	Size     int64
}

type StreamDiagnostics struct {
	Bytes    uint64
	Packets  uint64
	Marker   float64
	Interval float64
	Tag      string
}

type StreamFile struct {
	FileInfo        fs.FileInfo
	Name            string
	Size            int64
	Length          float64
	InterleavedFile *InterleavedFile
	Streams         map[uint16]stream.Info
	// StreamOrder preserves stream insertion order for diagnostics parity.
	StreamOrder       []uint16
	StreamDiagnostics map[uint16][]StreamDiagnostics
}

type streamState struct {
	windowPackets       uint64
	windowBytes         uint64
	dtsPrev             uint64
	tsCount             uint64
	tsLast              uint64
	ptsLast             uint64
	lastDTS             uint64
	lastDiff            int64
	codecData           []byte
	streamTag           string
	tagParse            uint32
	avcAUDParse         byte
	vc1FrameHeaderParse byte
	vc1SeqHeaderParse   byte
	vc1IsInterlaced     bool
	mpeg2PictureParse   byte
	hevcTagBuf          []byte
	hevcTagState        codec.HEVCTagState
	hevcTagInitialized  bool
	pesHeaderRemaining  int
	pesHeaderExtraKnown bool
	pesPacketRemaining  int
	pesHeaderBuf        []byte
	pesHeaderParsed     bool
	pesPtsDtsFlags      byte
	pesStarted          bool
	pesStartCount       uint64
	collectDiagnostics  bool
}

type scanClipTarget struct {
	clip    *StreamClip
	streams map[uint16]stream.Info
}

// clipTargetCursor narrows clip-target scans to time-overlapping clips while
// preserving original target iteration order for deterministic updates.
type clipTargetCursor struct {
	targets     []scanClipTarget
	startOrder  []int
	allOrder    []int
	activeOrder []int
	nextStart   int
	lastTime    float64
	hasLast     bool
}

func buildClipTargets(playlists []*PlaylistFile, streamName string) []scanClipTarget {
	if playlists == nil {
		return nil
	}
	targets := make([]scanClipTarget, 0, 16)
	for _, playlist := range playlists {
		for _, clip := range playlist.StreamClips {
			if clip == nil || clip.Name != streamName {
				continue
			}
			playlistStreams := playlist.Streams
			if clip.AngleIndex > 0 && clip.AngleIndex <= len(playlist.AngleStreams) {
				playlistStreams = playlist.AngleStreams[clip.AngleIndex-1]
			}
			targets = append(targets, scanClipTarget{
				clip:    clip,
				streams: playlistStreams,
			})
		}
	}
	return targets
}

func newClipTargetCursor(targets []scanClipTarget) *clipTargetCursor {
	if len(targets) == 0 {
		return nil
	}
	startOrder := make([]int, len(targets))
	allOrder := make([]int, len(targets))
	for i := range targets {
		startOrder[i] = i
		allOrder[i] = i
	}
	sort.SliceStable(startOrder, func(i, j int) bool {
		left := targets[startOrder[i]].clip
		right := targets[startOrder[j]].clip
		if left.TimeIn == right.TimeIn {
			return startOrder[i] < startOrder[j]
		}
		return left.TimeIn < right.TimeIn
	})
	return &clipTargetCursor{
		targets:    targets,
		startOrder: startOrder,
		allOrder:   allOrder,
	}
}

func (c *clipTargetCursor) activeIndices(streamTime float64) []int {
	if c == nil || len(c.targets) == 0 {
		return nil
	}
	if streamTime == 0 {
		return c.allOrder
	}
	if !c.hasLast || streamTime < c.lastTime {
		c.reset(streamTime)
	} else {
		c.advance(streamTime)
	}
	c.lastTime = streamTime
	c.hasLast = true
	return c.activeOrder
}

func (c *clipTargetCursor) advance(streamTime float64) {
	for c.nextStart < len(c.startOrder) {
		idx := c.startOrder[c.nextStart]
		if c.targets[idx].clip.TimeIn > streamTime {
			break
		}
		c.activate(idx)
		c.nextStart++
	}
	c.prune(streamTime)
}

func (c *clipTargetCursor) reset(streamTime float64) {
	c.nextStart = sort.Search(len(c.startOrder), func(i int) bool {
		idx := c.startOrder[i]
		return c.targets[idx].clip.TimeIn > streamTime
	})
	c.activeOrder = c.activeOrder[:0]
	for i := 0; i < c.nextStart; i++ {
		idx := c.startOrder[i]
		if streamTime <= c.targets[idx].clip.TimeOut {
			c.activate(idx)
		}
	}
}

func (c *clipTargetCursor) activate(idx int) {
	pos := sort.SearchInts(c.activeOrder, idx)
	if pos < len(c.activeOrder) && c.activeOrder[pos] == idx {
		return
	}
	c.activeOrder = append(c.activeOrder, 0)
	copy(c.activeOrder[pos+1:], c.activeOrder[pos:])
	c.activeOrder[pos] = idx
}

func (c *clipTargetCursor) prune(streamTime float64) {
	if len(c.activeOrder) == 0 {
		return
	}
	kept := c.activeOrder[:0]
	for _, idx := range c.activeOrder {
		if streamTime <= c.targets[idx].clip.TimeOut {
			kept = append(kept, idx)
		}
	}
	c.activeOrder = kept
}

func NewStreamFile(fileInfo fs.FileInfo) *StreamFile {
	streamFile := &StreamFile{
		FileInfo:          fileInfo,
		Name:              strings.ToUpper(fileInfo.Name()),
		Streams:           make(map[uint16]stream.Info),
		StreamDiagnostics: make(map[uint16][]StreamDiagnostics),
	}
	if fileInfo != nil {
		streamFile.Size = fileInfo.Length()
	}
	return streamFile
}

func (s *StreamFile) DisplayName(settings settings.Settings) string {
	if settings.EnableSSIF && s.InterleavedFile != nil {
		return s.InterleavedFile.Name
	}
	return s.Name
}

func (s *StreamFile) Scan(playlists []*PlaylistFile, full bool) error {
	return s.ScanWithProgress(playlists, full, nil)
}

func (s *StreamFile) ScanWithProgress(playlists []*PlaylistFile, full bool, onBytesProcessed func(uint64)) error {
	if s.FileInfo == nil {
		return nil
	}

	// Match BDInfo: TSStreamFile.Length is derived from parsed timestamps (DTS-based) and
	// starts at 0. Do not seed it from playlist clip lengths (can differ for tiny/partial captures).
	s.Length = 0

	// ensure streams map populated from clip info
	if len(s.Streams) == 0 {
		for _, pl := range playlists {
			for _, clip := range pl.StreamClips {
				if clip.StreamFile == s && clip.StreamClipFile != nil {
					pids := clip.StreamClipFile.StreamOrder
					if len(pids) == 0 {
						pids = make([]uint16, 0, len(clip.StreamClipFile.Streams))
						for pid := range clip.StreamClipFile.Streams {
							pids = append(pids, pid)
						}
						sort.Slice(pids, func(i, j int) bool { return pids[i] < pids[j] })
					}
					for _, pid := range pids {
						st, ok := clip.StreamClipFile.Streams[pid]
						if !ok || st == nil {
							continue
						}
						if _, exists := s.Streams[pid]; exists {
							continue
						}
						s.Streams[pid] = st.Clone()
						s.StreamOrder = append(s.StreamOrder, pid)
					}
				}
			}
		}
	}

	scanSettings := settings.Settings{}
	if len(playlists) > 0 {
		scanSettings = playlists[0].Settings
	}
	// Match BDInfo: stream diagnostics data is needed for chapter stats even when the
	// report omits the STREAM DIAGNOSTICS table.
	collectDiagnostics := true

	fileInfo := s.FileInfo
	if scanSettings.EnableSSIF && s.InterleavedFile != nil && s.InterleavedFile.FileInfo != nil {
		fileInfo = s.InterleavedFile.FileInfo
	}
	if fileInfo == nil {
		return fmt.Errorf("missing stream file info")
	}
	initialPMTOrder, _ := detectPMTStreamOrder(fileInfo)

	f, err := fileInfo.OpenRead()
	if err != nil {
		return err
	}
	defer f.Close()

	s.Size = fileInfo.Length()

	first := make([]byte, 192)
	if _, err := io.ReadFull(f, first); err != nil {
		return err
	}

	packetSize := 192
	syncOffset := 4
	if first[0] == 0x47 {
		packetSize = 188
		syncOffset = 0
	} else if first[4] == 0x47 {
		packetSize = 192
		syncOffset = 4
	} else {
		return fmt.Errorf("invalid TS sync for %s", s.Name)
	}

	states := make(map[uint16]*streamState, len(s.Streams)+1)
	var stateByPID [maxTSPID]*streamState
	var streamByPID [maxTSPID]stream.Info
	for pid, st := range s.Streams {
		dataCap := maxStreamDataOther
		if st != nil {
			switch {
			case st.Base().IsVideoStream():
				dataCap = maxStreamDataVideo
			case st.Base().IsAudioStream():
				dataCap = maxStreamDataAudio
			}
		}
		state := &streamState{
			codecData:          getCodecBuffer(dataCap),
			pesPacketRemaining: -2,
			collectDiagnostics: collectDiagnostics,
		}
		states[pid] = state
		if int(pid) < maxTSPID {
			streamByPID[int(pid)] = st
			stateByPID[int(pid)] = state
		}
		if collectDiagnostics {
			if _, ok := s.StreamDiagnostics[pid]; !ok {
				s.StreamDiagnostics[pid] = nil
			}
		}
	}
	var unknownState *streamState
	seenStreamOrder := make(map[uint16]struct{}, len(s.Streams))
	scanStreamOrder := make([]uint16, 0, len(s.Streams))
	pmtStreamOrder := append([]uint16(nil), initialPMTOrder...)
	defer func() {
		for _, state := range states {
			if state == nil || state.codecData == nil {
				continue
			}
			putCodecBuffer(state.codecData)
			state.codecData = nil
		}
	}()

	firstTS := uint64(0)
	lastTS := uint64(0)
	clipTargets := buildClipTargets(playlists, s.Name)
	clipCursor := newClipTargetCursor(clipTargets)

	processPacket := func(pkt []byte) {
		if len(pkt) <= syncOffset || pkt[syncOffset] != 0x47 {
			return
		}
		pid := (uint16(pkt[syncOffset+1]&0x1f) << 8) | uint16(pkt[syncOffset+2])
		pidIdx := int(pid)
		var state *streamState
		var st stream.Info
		if pidIdx < maxTSPID {
			state = stateByPID[pidIdx]
			st = streamByPID[pidIdx]
		}
		if state == nil {
			if unknownState == nil {
				unknownState = &streamState{pesPacketRemaining: -2, collectDiagnostics: collectDiagnostics}
				states[unknownStatePID] = unknownState
			}
			state = unknownState
		}
		known := st != nil
		if known {
			if _, ok := seenStreamOrder[pid]; !ok {
				seenStreamOrder[pid] = struct{}{}
				scanStreamOrder = append(scanStreamOrder, pid)
			}
		}
		isVideo := st != nil && st.Base().IsVideoStream()

		payloadStart := (pkt[syncOffset+1] & 0x40) != 0
		adaptation := (pkt[syncOffset+3] >> 4) & 0x3
		idx := syncOffset + 4
		state.windowPackets++
		if !known {
			return
		}
		if adaptation == 0 || adaptation == 2 {
			return
		}
		if adaptation == 3 {
			if idx >= len(pkt) {
				return
			}
			adapLen := int(pkt[idx])
			idx += 1 + adapLen
		}
		if idx >= len(pkt) {
			return
		}
		payload := pkt[idx:]
		if len(payload) == 0 {
			return
		}
		// Payload unit start is a hint; validate PES start code to avoid false starts (BDInfo scans for 0x000001).
		isPESStart := payloadStart && len(payload) >= 4 && payload[0] == 0x00 && payload[1] == 0x00 && payload[2] == 0x01
		if isPESStart && isVideo {
			streamID := payload[3]
			// Match BDInfo header detection: video accepts 0xFD or 0xE0-0xEF.
			if !(streamID == 0xFD || (streamID >= 0xE0 && streamID <= 0xEF)) {
				isPESStart = false
			}
		}

		if isPESStart {
			state.pesStartCount++

			// Match BDInfo: HEVC per-transfer tags are derived from the previous PES transfer
			// (ScanStream runs when a new payload starts, ending the prior transfer).
			if state.collectDiagnostics && isVideo {
				if vs, ok := st.(*stream.VideoStream); ok && vs.StreamType == stream.StreamTypeHEVCVideo {
					// Avoid stale tags: if we don't have any bytes for the prior transfer, treat it as no tag.
					if state.hevcTagBuf == nil {
						state.streamTag = ""
					} else {
						state.streamTag = codec.HEVCFrameTagFromTransfer(&state.hevcTagState, state.hevcTagBuf, state.hevcTagInitialized)
						state.hevcTagBuf = state.hevcTagBuf[:0]
					}
					// Match BDInfo: HEVC tag scan switches to "initialized" behavior once an SPS has been seen.
					if !state.hevcTagInitialized && state.hevcTagState.HasSPS() {
						state.hevcTagInitialized = true
						// After init, we only need a small prefix to find the first slice tag.
						state.hevcTagBuf = make([]byte, 0, 64<<10)
					}
				}
			}

			state.pesStarted = true
			state.pesHeaderRemaining = 9
			state.pesHeaderExtraKnown = false
			state.pesHeaderParsed = false
			state.pesPtsDtsFlags = 0
			if state.pesHeaderBuf == nil {
				state.pesHeaderBuf = make([]byte, 0, 19)
			} else {
				state.pesHeaderBuf = state.pesHeaderBuf[:0]
			}
			state.pesPacketRemaining = -2
		}

		for state.pesHeaderRemaining > 0 && len(payload) > 0 {
			headerTake := min(state.pesHeaderRemaining, len(payload))
			if headerTake > 0 && state.pesHeaderBuf != nil {
				need := 19 - len(state.pesHeaderBuf)
				if need > 0 {
					take := min(headerTake, need)
					state.pesHeaderBuf = append(state.pesHeaderBuf, payload[:take]...)
				}
			}
			payload = payload[headerTake:]
			state.pesHeaderRemaining -= headerTake

			if !state.pesHeaderExtraKnown && len(state.pesHeaderBuf) >= 9 {
				hdrLen := int(state.pesHeaderBuf[8])
				state.pesPtsDtsFlags = (state.pesHeaderBuf[7] >> 6) & 0x03
				state.pesHeaderRemaining += hdrLen
				state.pesHeaderExtraKnown = true
				if state.pesPacketRemaining == -2 && len(state.pesHeaderBuf) >= 6 {
					pesLen := int(state.pesHeaderBuf[4])<<8 | int(state.pesHeaderBuf[5])
					if pesLen > 0 {
						remaining := max(pesLen-(3+hdrLen), 0)
						state.pesPacketRemaining = remaining
					} else {
						state.pesPacketRemaining = -1
					}
				}
			}
			s.parsePESHeaderTimestamp(state, isVideo, playlists, clipTargets, clipCursor, states, pid, &firstTS, &lastTS)
		}
		if len(payload) == 0 {
			return
		}
		if !state.pesStarted {
			return
		}
		if state.pesPacketRemaining == 0 {
			return
		}
		if state.pesPacketRemaining > 0 && len(payload) > state.pesPacketRemaining {
			payload = payload[:state.pesPacketRemaining]
		}
		if known {
			state.windowBytes += uint64(len(payload))
		}

		// Match BDInfo: capture per-transfer stream tag for chapter/frame stats.
		// HEVC tags are derived from slice headers and depend on SPS/PPS state; collect a bounded
		// prefix of the current transfer and resolve when the next payload starts.
		if state.collectDiagnostics && isVideo && len(payload) > 0 {
			if vs, ok := st.(*stream.VideoStream); ok {
				if vs.StreamType == stream.StreamTypeHEVCVideo {
					if state.hevcTagBuf == nil {
						// Match BDInfo: before initialization, TSStreamBuffer captures up to 5MB
						// and HEVC tag selection can depend on later slices overwriting earlier ones.
						if state.hevcTagInitialized {
							state.hevcTagBuf = make([]byte, 0, 64<<10)
						} else {
							state.hevcTagBuf = make([]byte, 0, 5*1024*1024)
						}
					}
					if len(state.hevcTagBuf) < cap(state.hevcTagBuf) {
						need := cap(state.hevcTagBuf) - len(state.hevcTagBuf)
						if len(payload) > need {
							state.hevcTagBuf = append(state.hevcTagBuf, payload[:need]...)
						} else {
							state.hevcTagBuf = append(state.hevcTagBuf, payload...)
						}
					}
				} else if state.streamTag == "" {
					switch vs.StreamType {
					case stream.StreamTypeAVCVideo:
						for i := 0; i < len(payload) && state.streamTag == ""; i++ {
							state.tagParse = (state.tagParse << 8) | uint32(payload[i])
							if state.avcAUDParse > 0 {
								state.avcAUDParse--
								if state.avcAUDParse == 0 {
									switch (state.tagParse & 0xFF) >> 5 {
									case 0, 3, 5:
										state.streamTag = "I"
									case 1, 4, 6:
										state.streamTag = "P"
									case 2, 7:
										state.streamTag = "B"
									}
								}
								continue
							}
							if state.tagParse == 0x00000109 {
								state.avcAUDParse = 1
							}
						}
					case stream.StreamTypeMPEG2Video:
						for i := 0; i < len(payload) && state.streamTag == ""; i++ {
							state.tagParse = (state.tagParse << 8) | uint32(payload[i])
							if state.tagParse == 0x00000100 {
								state.mpeg2PictureParse = 2
								continue
							}
							if state.mpeg2PictureParse > 0 {
								state.mpeg2PictureParse--
								if state.mpeg2PictureParse == 0 {
									switch (state.tagParse & 0x38) >> 3 {
									case 1:
										state.streamTag = "I"
									case 2:
										state.streamTag = "P"
									case 3:
										state.streamTag = "B"
									}
								}
							}
						}
					case stream.StreamTypeVC1Video:
						for i := 0; i < len(payload) && state.streamTag == ""; i++ {
							state.tagParse = (state.tagParse << 8) | uint32(payload[i])
							if state.tagParse == 0x0000010D {
								state.vc1FrameHeaderParse = 4
								continue
							}
							if state.vc1FrameHeaderParse > 0 {
								state.vc1FrameHeaderParse--
								if state.vc1FrameHeaderParse == 0 {
									parse := state.tagParse
									var pictureType uint32
									if state.vc1IsInterlaced {
										if (parse & 0x80000000) == 0 {
											pictureType = (parse & 0x78000000) >> 13
										} else {
											pictureType = (parse & 0x3c000000) >> 12
										}
									} else {
										pictureType = (parse & 0xf0000000) >> 14
									}
									switch {
									case (pictureType & 0x20000) == 0:
										state.streamTag = "P"
									case (pictureType & 0x10000) == 0:
										state.streamTag = "B"
									case (pictureType & 0x8000) == 0:
										state.streamTag = "I"
									case (pictureType & 0x4000) == 0:
										state.streamTag = "BI"
									default:
										// Leave empty (null in the official output).
										state.streamTag = ""
									}
								}
								continue
							}
							if state.tagParse == 0x0000010F {
								state.vc1SeqHeaderParse = 6
								continue
							}
							if state.vc1SeqHeaderParse > 0 {
								state.vc1SeqHeaderParse--
								if state.vc1SeqHeaderParse == 0 {
									state.vc1IsInterlaced = (state.tagParse&0x40)>>6 > 0
								}
							}
						}
					}
				}
			}
		}
		if state.pesPacketRemaining > 0 {
			state.pesPacketRemaining -= len(payload)
		}
		if state.codecData != nil && len(payload) > 0 {
			dataCap := cap(state.codecData)
			if len(state.codecData) < dataCap {
				need := dataCap - len(state.codecData)
				if len(payload) > need {
					payload = payload[:need]
				}
				state.codecData = append(state.codecData, payload...)
			}
		}
	}

	processPacket(first[:packetSize])
	if onBytesProcessed != nil {
		onBytesProcessed(uint64(packetSize))
	}

	// Match official BDInfo behavior/perf: read large chunks and then walk packets.
	// (Official uses ~5MB chunks; keep ours aligned to TS packet size.)
	const targetChunk = 5 * 1024 * 1024
	chunkSize := targetChunk - (targetChunk % packetSize)
	if chunkSize < packetSize {
		chunkSize = packetSize * 256
	}

	// First read grabs 192 bytes to detect sync/packet size. For 188-byte TS packets this
	// includes the first 4 bytes of the next packet; carry those forward.
	carryLen := len(first) - packetSize

	// Buffer includes room for carry bytes and a remainder (up to packetSize-1).
	buf := make([]byte, chunkSize+packetSize)
	if carryLen > 0 {
		copy(buf, first[packetSize:])
	}
	for {
		n, err := f.Read(buf[carryLen : carryLen+chunkSize])
		if n == 0 && err != nil {
			break
		}

		n += carryLen
		aligned := n - (n % packetSize)
		for i := 0; i+packetSize <= aligned; i += packetSize {
			processPacket(buf[i : i+packetSize])
		}
		if onBytesProcessed != nil && aligned > 0 {
			onBytesProcessed(uint64(aligned))
		}

		// Preserve remainder bytes for next read.
		carryLen = n - aligned
		if carryLen > 0 {
			copy(buf, buf[aligned:n])
		}
		if err != nil {
			break
		}
	}

	// flush remaining window bytes based on last video PTS
	ptsLast := uint64(0)
	ptsDiff := int64(0)
	for pid, st := range s.Streams {
		if st == nil || !st.Base().IsVideoStream() {
			continue
		}
		state := states[pid]
		if state == nil {
			continue
		}
		if state.ptsLast > ptsLast {
			ptsLast = state.ptsLast
			ptsDiff = int64(ptsLast) - int64(state.dtsPrev)
		}
		s.updateStreamBitrates(playlists, clipTargets, clipCursor, states, pid, ptsLast, ptsDiff)
	}

	for pid, st := range s.Streams {
		state := states[pid]
		if state == nil {
			continue
		}
		// Match BDInfo: codec analyzers run on completed PES transfers (ScanStream). A tiny/cutoff
		// stream file can contain a single PES transfer that never terminates (no next PES start and
		// no bounded packet length), in which case BDInfo leaves codec fields uninitialized.
		//
		// Approximation: require at least one completed transfer (2x PES starts), or an explicit
		// bounded PES length that reached 0.
		canScanCodec := full || state.pesStartCount >= 2 || (state.pesStarted && state.pesPacketRemaining == 0)
		if !canScanCodec {
			continue
		}

		data := state.codecData
		switch concrete := st.(type) {
		case *stream.VideoStream:
			switch concrete.StreamType {
			case stream.StreamTypeAVCVideo:
				var tag *string
				if state.collectDiagnostics {
					tag = &state.streamTag
				}
				codec.ScanAVC(concrete, data, tag)
			case stream.StreamTypeHEVCVideo:
				codec.ScanHEVC(concrete, data, scanSettings)
			case stream.StreamTypeMPEG2Video:
				codec.ScanMPEG2(concrete, data)
			case stream.StreamTypeVC1Video:
				codec.ScanVC1(concrete, data)
			}
		case *stream.AudioStream:
			switch concrete.StreamType {
			case stream.StreamTypeAC3Audio, stream.StreamTypeAC3PlusAudio, stream.StreamTypeAC3PlusSecondaryAudio:
				codec.ScanAC3(concrete, data)
			case stream.StreamTypeAC3TrueHDAudio:
				codec.ScanTrueHD(concrete, data)
			case stream.StreamTypeDTSAudio:
				codec.ScanDTS(concrete, data, int64(concrete.BitRate))
			case stream.StreamTypeDTSHDAudio, stream.StreamTypeDTSHDMasterAudio, stream.StreamTypeDTSHDSecondaryAudio:
				codec.ScanDTSHD(concrete, data, int64(concrete.BitRate))
			case stream.StreamTypeLPCMAudio:
				codec.ScanLPCM(concrete, data)
			case stream.StreamTypeMPEG2AACAudio, stream.StreamTypeMPEG4AACAudio:
				codec.ScanAAC(concrete, data)
			}
		case *stream.GraphicsStream:
			codec.ScanPGS(concrete, data)
		}
	}

	s.finalizePlaylistVBR(playlists)
	if len(pmtStreamOrder) == 0 {
		if detectedOrder, ok := detectPMTStreamOrder(fileInfo); ok {
			pmtStreamOrder = detectedOrder
		}
	}
	if len(s.StreamOrder) > 0 || len(scanStreamOrder) > 0 || len(pmtStreamOrder) > 0 {
		order := make([]uint16, 0, len(s.Streams))
		seen := make(map[uint16]struct{}, len(s.Streams))
		appendIfKnown := func(pid uint16) {
			if _, ok := s.Streams[pid]; !ok {
				return
			}
			if _, ok := seen[pid]; ok {
				return
			}
			seen[pid] = struct{}{}
			order = append(order, pid)
		}
		// Prefer PMT-declared stream order (official BDInfo creates streams from PMT parsing).
		for _, pid := range pmtStreamOrder {
			appendIfKnown(pid)
		}
		// Then use observed scan order for any streams not present in PMT.
		for _, pid := range scanStreamOrder {
			appendIfKnown(pid)
		}
		// CLPI order is fallback when PMT/scan did not cover all streams.
		for _, pid := range s.StreamOrder {
			appendIfKnown(pid)
		}
		if len(order) < len(s.Streams) {
			remaining := make([]uint16, 0, len(s.Streams)-len(order))
			for pid := range s.Streams {
				if _, ok := seen[pid]; !ok {
					remaining = append(remaining, pid)
				}
			}
			sort.Slice(remaining, func(i, j int) bool { return remaining[i] < remaining[j] })
			order = append(order, remaining...)
		}
		s.StreamOrder = order
	}

	return nil
}

func (s *StreamFile) handleTimestamp(playlists []*PlaylistFile, clipTargets []scanClipTarget, clipCursor *clipTargetCursor, states map[uint16]*streamState, pid uint16, state *streamState, ts uint64, dtsForLength uint64, isVideo bool, firstDTS *uint64, lastDTS *uint64) {
	if ts == 0 {
		return
	}
	if state.tsCount > 0 {
		diff := int64(ts) - int64(state.dtsPrev)
		state.lastDiff = diff
		if isVideo {
			s.updateStreamBitrates(playlists, clipTargets, clipCursor, states, pid, ts, diff)
			// BDInfo computes TSStreamFile.Length using DTS (when present). For PES packets that
			// only include PTS, BDInfo continues to use the last seen DTS and does not extend
			// the file duration based on PTS-only timestamps.
			if dtsForLength > 0 {
				if *firstDTS == 0 || dtsForLength < *firstDTS {
					*firstDTS = dtsForLength
				}
				if dtsForLength > *lastDTS {
					*lastDTS = dtsForLength
				}
				if *lastDTS > *firstDTS {
					s.Length = float64(*lastDTS-*firstDTS) / 90000.0
				}
			}
		}
	}
	state.dtsPrev = ts
	state.tsLast = ts
	state.tsCount++
}

func (s *StreamFile) parsePESHeaderTimestamp(state *streamState, isVideo bool, playlists []*PlaylistFile, clipTargets []scanClipTarget, clipCursor *clipTargetCursor, states map[uint16]*streamState, pid uint16, firstTS *uint64, lastTS *uint64) {
	if !isVideo || state.pesHeaderParsed {
		return
	}
	switch state.pesPtsDtsFlags {
	case 2:
		// PTS only (no DTS present).
		if len(state.pesHeaderBuf) < 14 {
			return
		}
		pts := parsePTS(state.pesHeaderBuf[9:14])
		if pts > 0 {
			state.ptsLast = pts
		}
		// For duration calculation, keep using the last DTS observed for this stream.
		s.handleTimestamp(playlists, clipTargets, clipCursor, states, pid, state, pts, state.lastDTS, isVideo, firstTS, lastTS)
		state.pesHeaderParsed = true
	case 3:
		if len(state.pesHeaderBuf) < 19 {
			return
		}
		pts := parsePTS(state.pesHeaderBuf[9:14])
		if pts > state.ptsLast {
			state.ptsLast = pts
		}
		dts := parsePTS(state.pesHeaderBuf[14:19])
		if dts == 0 {
			dts = pts
		}
		if dts > 0 {
			state.lastDTS = dts
			s.handleTimestamp(playlists, clipTargets, clipCursor, states, pid, state, dts, dts, isVideo, firstTS, lastTS)
		}
		state.pesHeaderParsed = true
	}
}

func (s *StreamFile) updateStreamBitrates(playlists []*PlaylistFile, clipTargets []scanClipTarget, clipCursor *clipTargetCursor, states map[uint16]*streamState, ptsPID uint16, pts uint64, ptsDiff int64) {
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

func (s *StreamFile) finalizePlaylistVBR(playlists []*PlaylistFile) {
	if playlists == nil {
		return
	}
	for _, playlist := range playlists {
		packetSeconds := 0.0
		for _, clip := range playlist.StreamClips {
			if clip.AngleIndex == 0 {
				packetSeconds += clip.PacketSeconds
			}
		}
		if packetSeconds <= 0 {
			continue
		}
		for _, playlistStream := range playlist.SortedStreams {
			if playlistStream.Base().IsVBR {
				playlistStream.Base().BitRate = int64(math.RoundToEven(float64(playlistStream.Base().PayloadBytes) * 8.0 / packetSeconds))
			}
		}
	}
}

func (s *StreamFile) updateStreamBitrate(clipTargets []scanClipTarget, clipCursor *clipTargetCursor, pid uint16, pts uint64, ptsDiff int64, state *streamState) {
	if state == nil {
		return
	}
	streamTime := float64(pts) / 90000.0
	streamInterval := float64(ptsDiff) / 90000.0
	streamOffset := streamTime + streamInterval

	if clipCursor != nil {
		for _, idx := range clipCursor.activeIndices(streamTime) {
			target := clipTargets[idx]
			clip := target.clip
			if streamTime != 0 && (streamTime < clip.TimeIn || streamTime > clip.TimeOut) {
				continue
			}
			clip.PayloadBytes += state.windowBytes
			clip.PacketCount += state.windowPackets

			if streamOffset > clip.TimeIn && streamOffset-clip.TimeIn > clip.PacketSeconds {
				clip.PacketSeconds = streamOffset - clip.TimeIn
			}

			if target.streams != nil {
				if streamInfo, ok := target.streams[pid]; ok {
					streamInfo.Base().PayloadBytes += state.windowBytes
					streamInfo.Base().PacketCount += state.windowPackets

					if streamInfo.Base().IsVideoStream() {
						streamInfo.Base().PacketSeconds += streamInterval
						if streamInfo.Base().PacketSeconds > 0 {
							streamInfo.Base().ActiveBitRate = int64(math.RoundToEven(float64(streamInfo.Base().PayloadBytes) * 8.0 / streamInfo.Base().PacketSeconds))
						}
					}
					if streamInfo.Base().StreamType == stream.StreamTypeAC3TrueHDAudio {
						if audio, ok := streamInfo.(*stream.AudioStream); ok && audio.CoreStream != nil {
							streamInfo.Base().ActiveBitRate -= audio.CoreStream.BitRate
						}
					}
				}
			}
		}
	} else {
		for _, target := range clipTargets {
			clip := target.clip
			if streamTime != 0 && (streamTime < clip.TimeIn || streamTime > clip.TimeOut) {
				continue
			}
			clip.PayloadBytes += state.windowBytes
			clip.PacketCount += state.windowPackets

			if streamOffset > clip.TimeIn && streamOffset-clip.TimeIn > clip.PacketSeconds {
				clip.PacketSeconds = streamOffset - clip.TimeIn
			}

			if target.streams != nil {
				if streamInfo, ok := target.streams[pid]; ok {
					streamInfo.Base().PayloadBytes += state.windowBytes
					streamInfo.Base().PacketCount += state.windowPackets

					if streamInfo.Base().IsVideoStream() {
						streamInfo.Base().PacketSeconds += streamInterval
						if streamInfo.Base().PacketSeconds > 0 {
							streamInfo.Base().ActiveBitRate = int64(math.RoundToEven(float64(streamInfo.Base().PayloadBytes) * 8.0 / streamInfo.Base().PacketSeconds))
						}
					}
					if streamInfo.Base().StreamType == stream.StreamTypeAC3TrueHDAudio {
						if audio, ok := streamInfo.(*stream.AudioStream); ok && audio.CoreStream != nil {
							streamInfo.Base().ActiveBitRate -= audio.CoreStream.BitRate
						}
					}
				}
			}
		}
	}

	if streamInfo, ok := s.Streams[pid]; ok {
		streamInfo.Base().PayloadBytes += state.windowBytes
		streamInfo.Base().PacketCount += state.windowPackets
		if streamInfo.Base().IsVideoStream() {
			if state.collectDiagnostics {
				streamInfo.Base().PacketSeconds += streamInterval
				s.StreamDiagnostics[pid] = append(s.StreamDiagnostics[pid], StreamDiagnostics{
					Marker:   streamTime,
					Interval: streamInterval,
					Bytes:    state.windowBytes,
					Packets:  state.windowPackets,
					Tag:      state.streamTag,
				})
				// Match the existing parity behavior: reset tag parsing state after emitting
				// a diagnostics row, but keep HEVC tags until the next transfer boundary.
				if v, ok := streamInfo.(*stream.VideoStream); !ok || v.StreamType != stream.StreamTypeHEVCVideo {
					state.streamTag = ""
					state.tagParse = 0
					state.avcAUDParse = 0
					state.vc1FrameHeaderParse = 0
					state.vc1SeqHeaderParse = 0
					state.vc1IsInterlaced = false
					state.mpeg2PictureParse = 0
				}
			} else {
				streamInfo.Base().PacketSeconds += streamInterval
			}
		}
	}

	state.windowPackets = 0
	state.windowBytes = 0
}

func parsePTS(data []byte) uint64 {
	if len(data) < 5 {
		return 0
	}
	pts := uint64(data[0]&0x0E) << 29
	pts |= uint64(data[1]) << 22
	pts |= uint64(data[2]&0xFE) << 14
	pts |= uint64(data[3]) << 7
	pts |= uint64(data[4]) >> 1
	return pts
}

func validTimestamp(data []byte, prefix byte) bool {
	if len(data) < 5 {
		return false
	}
	if data[0]&0xF0 != prefix {
		return false
	}
	if data[0]&0x01 != 0x01 || data[2]&0x01 != 0x01 || data[4]&0x01 != 0x01 {
		return false
	}
	return true
}
