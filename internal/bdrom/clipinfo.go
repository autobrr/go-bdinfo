package bdrom

import (
	"fmt"
	"io"
	"strings"

	"github.com/autobrr/go-bdinfo/internal/fs"
	"github.com/autobrr/go-bdinfo/internal/stream"
)

type StreamClipFile struct {
	FileInfo fs.FileInfo
	Name     string
	FileType string
	IsValid  bool
	Streams  map[uint16]stream.Info
	// StreamOrder preserves CLPI stream table order for parity with official BDInfo.
	StreamOrder []uint16
}

func NewStreamClipFile(fileInfo fs.FileInfo) *StreamClipFile {
	return &StreamClipFile{
		FileInfo: fileInfo,
		Name:     strings.ToUpper(fileInfo.Name()),
		Streams:  make(map[uint16]stream.Info),
	}
}

func (s *StreamClipFile) Scan() error {
	if s.FileInfo == nil {
		return fmt.Errorf("clip info file missing")
	}
	f, err := s.FileInfo.OpenRead()
	if err != nil {
		return err
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		return err
	}
	if len(data) < 20 {
		return fmt.Errorf("clip info %s too short", s.Name)
	}

	fileType := string(data[:8])
	s.FileType = fileType
	if fileType != "HDMV0100" && fileType != "HDMV0200" && fileType != "HDMV0300" {
		return fmt.Errorf("clip info %s has unknown file type %s", s.Name, fileType)
	}

	clipIndex := int(uint32(data[12])<<24 | uint32(data[13])<<16 | uint32(data[14])<<8 | uint32(data[15]))
	if clipIndex+4 > len(data) {
		return fmt.Errorf("clip info %s invalid clip index", s.Name)
	}
	clipLength := int(uint32(data[clipIndex])<<24 | uint32(data[clipIndex+1])<<16 | uint32(data[clipIndex+2])<<8 | uint32(data[clipIndex+3]))
	if clipIndex+4+clipLength > len(data) {
		return fmt.Errorf("clip info %s invalid clip length", s.Name)
	}
	clipData := data[clipIndex+4 : clipIndex+4+clipLength]
	if len(clipData) < 12 {
		return fmt.Errorf("clip info %s invalid clip data", s.Name)
	}

	streamCount := int(clipData[8])
	offset := 10
	for range streamCount {
		if offset+4 > len(clipData) {
			break
		}
		pid := uint16(clipData[offset])<<8 | uint16(clipData[offset+1])
		offset += 2
		if offset+2 >= len(clipData) {
			break
		}
		streamType := stream.StreamType(clipData[offset+1])

		var st stream.Info
		switch streamType {
		case stream.StreamTypeHEVCVideo, stream.StreamTypeAVCVideo, stream.StreamTypeMPEG1Video, stream.StreamTypeMPEG2Video, stream.StreamTypeVC1Video:
			if offset+4 > len(clipData) {
				break
			}
			videoFormat := stream.VideoFormat(clipData[offset+2] >> 4)
			frameRate := stream.FrameRate(clipData[offset+2] & 0x0F)
			aspectRatio := stream.AspectRatio(clipData[offset+3] >> 4)
			vs := &stream.VideoStream{}
			vs.StreamType = streamType
			vs.SetVideoFormat(videoFormat)
			vs.SetFrameRate(frameRate)
			vs.AspectRatio = aspectRatio
			st = vs
		case stream.StreamTypeAC3Audio, stream.StreamTypeAC3PlusAudio, stream.StreamTypeAC3PlusSecondaryAudio,
			stream.StreamTypeAC3TrueHDAudio, stream.StreamTypeDTSAudio, stream.StreamTypeDTSHDAudio,
			stream.StreamTypeDTSHDMasterAudio, stream.StreamTypeDTSHDSecondaryAudio,
			stream.StreamTypeLPCMAudio, stream.StreamTypeMPEG1Audio, stream.StreamTypeMPEG2Audio,
			stream.StreamTypeMPEG2AACAudio, stream.StreamTypeMPEG4AACAudio:
			if offset+6 > len(clipData) {
				break
			}
			lang := string(clipData[offset+3 : offset+6])
			channelLayout := stream.ChannelLayout(clipData[offset+2] >> 4)
			sampleRate := stream.SampleRate(clipData[offset+2] & 0x0F)
			as := &stream.AudioStream{}
			as.StreamType = streamType
			as.ChannelLayout = channelLayout
			as.SampleRate = stream.ConvertSampleRate(sampleRate)
			as.SetLanguageCode(lang)
			st = as
		case stream.StreamTypeInteractiveGraphics, stream.StreamTypePresentationGraphics:
			if offset+5 > len(clipData) {
				break
			}
			lang := string(clipData[offset+2 : offset+5])
			gs := stream.NewGraphicsStream()
			gs.StreamType = streamType
			gs.SetLanguageCode(lang)
			st = gs
		case stream.StreamTypeSubtitle:
			if offset+6 > len(clipData) {
				break
			}
			lang := string(clipData[offset+3 : offset+6])
			ts := stream.NewTextStream()
			ts.StreamType = streamType
			ts.SetLanguageCode(lang)
			st = ts
		default:
			st = &stream.Stream{StreamType: streamType}
		}
		if st != nil {
			st.Base().PID = pid
			st.Base().StreamType = streamType
			if _, exists := s.Streams[pid]; !exists {
				s.StreamOrder = append(s.StreamOrder, pid)
			}
			s.Streams[pid] = st
		}
		if offset >= len(clipData) {
			break
		}
		offset += int(clipData[offset]) + 1
	}
	return nil
}
