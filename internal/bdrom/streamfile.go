package bdrom

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"strings"

	"github.com/autobrr/go-bdinfo/internal/codec"
	"github.com/autobrr/go-bdinfo/internal/fs"
	"github.com/autobrr/go-bdinfo/internal/settings"
	"github.com/autobrr/go-bdinfo/internal/stream"
)

const maxStreamData = 5 * 1024 * 1024

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
	lastDiff            int64
	codecData           []byte
	streamTag           string
	pesHeaderRemaining  int
	pesHeaderExtraKnown bool
	pesPacketRemaining  int
	pesHeaderBuf        []byte
	pesHeaderParsed     bool
	pesPtsDtsFlags      byte
	pesStarted          bool
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
	length := 0.0
	for _, pl := range playlists {
		for _, clip := range pl.StreamClips {
			if clip.StreamFile == s && clip.AngleIndex == 0 {
				if clip.Length > length {
					length = clip.Length
				}
			}
		}
	}
	s.Length = length

	scanSettings := settings.Settings{}
	if len(playlists) > 0 {
		scanSettings = playlists[0].Settings
	}

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

	reader := bufio.NewReaderSize(f, 1<<20)
	first := make([]byte, 192)
	if _, err := io.ReadFull(reader, first); err != nil {
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
	for pid := range s.Streams {
		states[pid] = &streamState{codecData: make([]byte, 0, maxStreamData), pesPacketRemaining: -2}
		if _, ok := s.StreamDiagnostics[pid]; !ok {
			s.StreamDiagnostics[pid] = nil
		}
	}

	firstTS := uint64(0)
	lastTS := uint64(0)

	processPacket := func(pkt []byte) {
		if len(pkt) <= syncOffset || pkt[syncOffset] != 0x47 {
			return
		}
		pid := (uint16(pkt[syncOffset+1]&0x1f) << 8) | uint16(pkt[syncOffset+2])
		state, ok := states[pid]
		if !ok {
			state = &streamState{pesPacketRemaining: -2}
			states[pid] = state
		}
		st, known := s.Streams[pid]
		isVideo := st != nil && st.Base().IsVideoStream()
		handleTimestamp := func(ts uint64) {
			if ts == 0 {
				return
			}
			if state.tsCount > 0 {
				diff := int64(ts) - int64(state.dtsPrev)
				state.lastDiff = diff
				if isVideo {
					s.updateStreamBitrates(playlists, states, pid, ts, diff)
					if firstTS == 0 || ts < firstTS {
						firstTS = ts
					}
					if ts > lastTS {
						lastTS = ts
					}
					if lastTS > firstTS {
						s.Length = float64(lastTS-firstTS) / 90000.0
					}
				}
			}
			state.dtsPrev = ts
			state.tsLast = ts
			state.tsCount++
		}
		parsePESHeaderTimestamp := func() {
			if !isVideo || state.pesHeaderParsed {
				return
			}
			switch state.pesPtsDtsFlags {
			case 2:
				if len(state.pesHeaderBuf) < 14 {
					return
				}
				pts := parsePTS(state.pesHeaderBuf[9:14])
				if pts > 0 {
					state.ptsLast = pts
				}
				if len(state.pesHeaderBuf) >= 19 && validTimestamp(state.pesHeaderBuf[14:19], 0x10) {
					dts := parsePTS(state.pesHeaderBuf[14:19])
					if dts > 0 {
						handleTimestamp(dts)
					} else {
						handleTimestamp(pts)
					}
				} else {
					handleTimestamp(pts)
				}
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
					handleTimestamp(dts)
				}
				state.pesHeaderParsed = true
			}
		}

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
			state.pesHeaderRemaining = 0
			state.pesHeaderExtraKnown = false
			state.pesPacketRemaining = -2
			state.pesHeaderBuf = nil
			state.pesHeaderParsed = false
			state.pesPtsDtsFlags = 0
			state.pesStarted = false
		}
		if payloadStart {
			state.pesStarted = true
			state.pesHeaderRemaining = 9
			state.pesHeaderExtraKnown = false
			state.pesHeaderParsed = false
			state.pesPtsDtsFlags = 0
			state.pesHeaderBuf = make([]byte, 0, 19)
			state.pesPacketRemaining = -2
		}

		for state.pesHeaderRemaining > 0 && len(payload) > 0 {
			headerTake := state.pesHeaderRemaining
			if headerTake > len(payload) {
				headerTake = len(payload)
			}
			if headerTake > 0 && state.pesHeaderBuf != nil {
				need := 19 - len(state.pesHeaderBuf)
				if need > 0 {
					take := headerTake
					if take > need {
						take = need
					}
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
						remaining := pesLen - (3 + hdrLen)
						if remaining < 0 {
							remaining = 0
						}
						state.pesPacketRemaining = remaining
					} else {
						state.pesPacketRemaining = -1
					}
				}
			}
			parsePESHeaderTimestamp()
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
		if state.pesPacketRemaining > 0 {
			state.pesPacketRemaining -= len(payload)
		}
		if state.codecData != nil && len(state.codecData) < maxStreamData && len(payload) > 0 {
			need := maxStreamData - len(state.codecData)
			if len(payload) > need {
				payload = payload[:need]
			}
			state.codecData = append(state.codecData, payload...)
		}
	}

	processPacket(first[:packetSize])
	buf := make([]byte, packetSize)
	for {
		_, err := io.ReadFull(reader, buf)
		if err != nil {
			break
		}
		processPacket(buf[:packetSize])
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
		data := states[pid].codecData
		switch concrete := st.(type) {
		case *stream.VideoStream:
			switch concrete.StreamType {
			case stream.StreamTypeAVCVideo:
				codec.ScanAVC(concrete, data, &states[pid].streamTag)
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
			if state.streamTag == "" {
				state.streamTag = "I"
			}
			streamInfo.Base().PacketSeconds += streamInterval
			s.StreamDiagnostics[pid] = append(s.StreamDiagnostics[pid], StreamDiagnostics{
				Marker:   streamTime,
				Interval: streamInterval,
				Bytes:    state.windowBytes,
				Packets:  state.windowPackets,
				Tag:      state.streamTag,
			})
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
