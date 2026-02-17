package bdrom

import (
	"fmt"
	"io"
	"math"
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
	FileInfo          fs.FileInfo
	Name              string
	Size              int64
	Length            float64
	InterleavedFile   *InterleavedFile
	Streams           map[uint16]stream.Info
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
	pesHeaderRemaining  int
	pesHeaderExtraKnown bool
	pesPacketRemaining  int
	pesHeaderBuf        []byte
	pesHeaderParsed     bool
	pesPtsDtsFlags      byte
	pesStarted          bool
	collectDiagnostics  bool
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
	if s.FileInfo == nil {
		return nil
	}

	// ensure streams map populated from clip info
	if len(s.Streams) == 0 {
		for _, pl := range playlists {
			for _, clip := range pl.StreamClips {
				if clip.StreamFile == s && clip.StreamClipFile != nil {
					for pid, st := range clip.StreamClipFile.Streams {
						if _, ok := s.Streams[pid]; !ok {
							s.Streams[pid] = st.Clone()
						}
					}
				}
			}
		}
	}

	// compute length from playlists
	playlistLength := 0.0
	for _, pl := range playlists {
		for _, clip := range pl.StreamClips {
			if clip.StreamFile == s && clip.AngleIndex == 0 {
				if clip.Length > playlistLength {
					playlistLength = clip.Length
				}
			}
		}
	}
	s.Length = playlistLength

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

	states := make(map[uint16]*streamState)
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
		states[pid] = &streamState{
			codecData:          getCodecBuffer(dataCap),
			pesPacketRemaining: -2,
			collectDiagnostics: collectDiagnostics,
		}
		if collectDiagnostics {
			if _, ok := s.StreamDiagnostics[pid]; !ok {
				s.StreamDiagnostics[pid] = nil
			}
		}
	}
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

	processPacket := func(pkt []byte) {
		if len(pkt) <= syncOffset || pkt[syncOffset] != 0x47 {
			return
		}
		pid := (uint16(pkt[syncOffset+1]&0x1f) << 8) | uint16(pkt[syncOffset+2])
		state, ok := states[pid]
		if !ok {
			state = &streamState{pesPacketRemaining: -2, collectDiagnostics: collectDiagnostics}
			states[pid] = state
		}
		st, known := s.Streams[pid]
		isVideo := st != nil && st.Base().IsVideoStream()

		payloadStart := (pkt[syncOffset+1] & 0x40) != 0
		adaptation := (pkt[syncOffset+3] >> 4) & 0x3
		idx := syncOffset + 4
		state.windowPackets++
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
		if payloadStart {
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
			s.parsePESHeaderTimestamp(state, isVideo, playlists, states, pid, &firstTS, &lastTS)
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
		// The tag is derived from the codec bitstream and can be empty when the scan
		// does not encounter the expected marker in this transfer.
		if state.collectDiagnostics && isVideo && state.streamTag == "" && len(payload) > 0 {
			if vs, ok := st.(*stream.VideoStream); ok {
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
		s.updateStreamBitrates(playlists, states, pid, ptsLast, ptsDiff)
	}

	for pid, st := range s.Streams {
		state := states[pid]
		if state == nil {
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

	return nil
}

func (s *StreamFile) handleTimestamp(playlists []*PlaylistFile, states map[uint16]*streamState, pid uint16, state *streamState, ts uint64, dtsForLength uint64, isVideo bool, firstDTS *uint64, lastDTS *uint64) {
	if ts == 0 {
		return
	}
	if state.tsCount > 0 {
		diff := int64(ts) - int64(state.dtsPrev)
		state.lastDiff = diff
		if isVideo {
			s.updateStreamBitrates(playlists, states, pid, ts, diff)
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

func (s *StreamFile) parsePESHeaderTimestamp(state *streamState, isVideo bool, playlists []*PlaylistFile, states map[uint16]*streamState, pid uint16, firstTS *uint64, lastTS *uint64) {
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
		s.handleTimestamp(playlists, states, pid, state, pts, state.lastDTS, isVideo, firstTS, lastTS)
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
			s.handleTimestamp(playlists, states, pid, state, dts, dts, isVideo, firstTS, lastTS)
		}
		state.pesHeaderParsed = true
	}
}

func (s *StreamFile) updateStreamBitrates(playlists []*PlaylistFile, states map[uint16]*streamState, ptsPID uint16, pts uint64, ptsDiff int64) {
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
		s.updateStreamBitrate(playlists, pid, pts, ptsDiff, state)
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

func (s *StreamFile) updateStreamBitrate(playlists []*PlaylistFile, pid uint16, pts uint64, ptsDiff int64, state *streamState) {
	if playlists == nil || state == nil {
		return
	}
	streamTime := float64(pts) / 90000.0
	streamInterval := float64(ptsDiff) / 90000.0
	streamOffset := streamTime + streamInterval

	for _, playlist := range playlists {
		for _, clip := range playlist.StreamClips {
			if clip.Name != s.Name {
				continue
			}
			if streamTime != 0 && (streamTime < clip.TimeIn || streamTime > clip.TimeOut) {
				continue
			}
			clip.PayloadBytes += state.windowBytes
			clip.PacketCount += state.windowPackets

			if streamOffset > clip.TimeIn && streamOffset-clip.TimeIn > clip.PacketSeconds {
				clip.PacketSeconds = streamOffset - clip.TimeIn
			}

			playlistStreams := playlist.Streams
			if clip.AngleIndex > 0 && clip.AngleIndex <= len(playlist.AngleStreams) {
				playlistStreams = playlist.AngleStreams[clip.AngleIndex-1]
			}

			if playlistStreams != nil {
				if streamInfo, ok := playlistStreams[pid]; ok {
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
				// Match BDInfo: tag parsing state resets per transfer.
				state.streamTag = ""
				state.tagParse = 0
				state.avcAUDParse = 0
				state.vc1FrameHeaderParse = 0
				state.vc1SeqHeaderParse = 0
				state.vc1IsInterlaced = false
				state.mpeg2PictureParse = 0
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
