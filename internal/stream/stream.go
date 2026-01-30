package stream

import (
	"fmt"
	"strings"

	"github.com/autobrr/go-bdinfo/internal/lang"
)

// Stream is the base transport stream entry.
type Stream struct {
	PID        uint16
	StreamType StreamType
	Descriptors []Descriptor
	BitRate    int64
	ActiveBitRate int64
	IsVBR      bool
	IsInitialized bool
	LanguageName  string
	IsHidden      bool

	PayloadBytes uint64
	PacketCount  uint64
	PacketSeconds float64
	AngleIndex    int

	BaseView *bool

	languageCode string
}

type Info interface {
	Base() *Stream
	Description() string
	Clone() Info
}

func (s *Stream) String() string {
	return fmt.Sprintf("%s (%d)", s.CodecShortName(), s.PID)
}

func (s *Stream) LanguageCode() string {
	return s.languageCode
}

func (s *Stream) SetLanguageCode(code string) {
	s.languageCode = code
	s.LanguageName = lang.CodeName(code)
}

func (s *Stream) PacketSize() uint64 {
	return s.PacketCount * 192
}

func (s *Stream) IsVideoStream() bool {
	switch s.StreamType {
	case StreamTypeMPEG1Video, StreamTypeMPEG2Video, StreamTypeAVCVideo, StreamTypeMVCVideo, StreamTypeVC1Video, StreamTypeHEVCVideo:
		return true
	default:
		return false
	}
}

func (s *Stream) IsAudioStream() bool {
	switch s.StreamType {
	case StreamTypeMPEG1Audio, StreamTypeMPEG2Audio, StreamTypeMPEG2AACAudio, StreamTypeMPEG4AACAudio,
		StreamTypeLPCMAudio, StreamTypeAC3Audio, StreamTypeAC3PlusAudio, StreamTypeAC3PlusSecondaryAudio,
		StreamTypeAC3TrueHDAudio, StreamTypeDTSAudio, StreamTypeDTSHDAudio, StreamTypeDTSHDSecondaryAudio, StreamTypeDTSHDMasterAudio:
		return true
	default:
		return false
	}
}

func (s *Stream) IsGraphicsStream() bool {
	switch s.StreamType {
	case StreamTypePresentationGraphics, StreamTypeInteractiveGraphics:
		return true
	default:
		return false
	}
}

func (s *Stream) IsTextStream() bool {
	switch s.StreamType {
	case StreamTypeSubtitle:
		return true
	default:
		return false
	}
}

func (s *Stream) CodecName() string {
	switch s.StreamType {
	case StreamTypeMPEG1Video:
		return "MPEG-1 Video"
	case StreamTypeMPEG2Video:
		return "MPEG-2 Video"
	case StreamTypeAVCVideo:
		return "MPEG-4 AVC Video"
	case StreamTypeMVCVideo:
		return "MPEG-4 MVC Video"
	case StreamTypeHEVCVideo:
		return "MPEG-H HEVC Video"
	case StreamTypeVC1Video:
		return "VC-1 Video"
	case StreamTypeMPEG1Audio:
		return "MPEG-1 Audio"
	case StreamTypeMPEG2Audio:
		return "MPEG-2 Audio"
	case StreamTypeMPEG2AACAudio:
		return "MPEG-2 AAC Audio"
	case StreamTypeMPEG4AACAudio:
		return "MPEG-4 AAC Audio"
	case StreamTypeLPCMAudio:
		return "LPCM Audio"
	case StreamTypeAC3Audio:
		return "Dolby Digital Audio"
	case StreamTypeAC3PlusAudio:
		return "Dolby Digital Plus Audio"
	case StreamTypeAC3PlusSecondaryAudio:
		return "Dolby Digital Plus Audio"
	case StreamTypeAC3TrueHDAudio:
		return "Dolby TrueHD Audio"
	case StreamTypeDTSAudio:
		return "DTS Audio"
	case StreamTypeDTSHDAudio:
		return "DTS-HD High-Res Audio"
	case StreamTypeDTSHDSecondaryAudio:
		return "DTS Express"
	case StreamTypeDTSHDMasterAudio:
		return "DTS-HD Master Audio"
	case StreamTypePresentationGraphics:
		return "Presentation Graphics"
	case StreamTypeInteractiveGraphics:
		return "Interactive Graphics"
	case StreamTypeSubtitle:
		return "Subtitle"
	default:
		return "UNKNOWN"
	}
}

// CodecNameForInfo returns codec name with audio extensions applied.
func CodecNameForInfo(info Info) string {
	if info == nil {
		return "UNKNOWN"
	}
	base := info.Base()
	if audio, ok := info.(*AudioStream); ok {
		switch audio.StreamType {
		case StreamTypeAC3Audio:
			if audio.AudioMode == AudioModeExtended {
				return "Dolby Digital EX Audio"
			}
			return "Dolby Digital Audio"
		case StreamTypeAC3PlusAudio:
			if audio.HasExtensions {
				return "Dolby Digital Plus/Atmos Audio"
			}
			return "Dolby Digital Plus Audio"
		case StreamTypeAC3PlusSecondaryAudio:
			return "Dolby Digital Plus Audio"
		case StreamTypeAC3TrueHDAudio:
			if audio.HasExtensions {
				return "Dolby TrueHD/Atmos Audio"
			}
			return "Dolby TrueHD Audio"
		case StreamTypeDTSAudio:
			if audio.AudioMode == AudioModeExtended {
				return "DTS-ES Audio"
			}
			return "DTS Audio"
		case StreamTypeDTSHDAudio:
			if audio.HasExtensions {
				return "DTS:X High-Res Audio"
			}
			return "DTS-HD High-Res Audio"
		case StreamTypeDTSHDSecondaryAudio:
			return "DTS Express"
		case StreamTypeDTSHDMasterAudio:
			if audio.HasExtensions {
				return "DTS:X Master Audio"
			}
			return "DTS-HD Master Audio"
		}
	}
	return base.CodecName()
}

func (s *Stream) CodecAltName() string {
	switch s.StreamType {
	case StreamTypeMPEG1Video:
		return "MPEG-1"
	case StreamTypeMPEG2Video:
		return "MPEG-2"
	case StreamTypeAVCVideo:
		return "AVC"
	case StreamTypeMVCVideo:
		return "MVC"
	case StreamTypeHEVCVideo:
		return "HEVC"
	case StreamTypeVC1Video:
		return "VC-1"
	case StreamTypeMPEG1Audio:
		return "MP1"
	case StreamTypeMPEG2Audio:
		return "MP2"
	case StreamTypeMPEG2AACAudio:
		return "MPEG-2 AAC"
	case StreamTypeMPEG4AACAudio:
		return "MPEG-4 AAC"
	case StreamTypeLPCMAudio:
		return "LPCM"
	case StreamTypeAC3Audio:
		return "DD AC3"
	case StreamTypeAC3PlusAudio, StreamTypeAC3PlusSecondaryAudio:
		return "DD AC3+"
	case StreamTypeAC3TrueHDAudio:
		return "Dolby TrueHD"
	case StreamTypeDTSAudio:
		return "DTS"
	case StreamTypeDTSHDAudio:
		return "DTS-HD Hi-Res"
	case StreamTypeDTSHDSecondaryAudio:
		return "DTS Express"
	case StreamTypeDTSHDMasterAudio:
		return "DTS-HD Master"
	case StreamTypePresentationGraphics:
		return "PGS"
	case StreamTypeInteractiveGraphics:
		return "IGS"
	case StreamTypeSubtitle:
		return "SUB"
	default:
		return "UNKNOWN"
	}
}

// CodecAltNameForInfo returns alternate codec name with audio extensions applied.
func CodecAltNameForInfo(info Info) string {
	if info == nil {
		return "UNKNOWN"
	}
	base := info.Base()
	if audio, ok := info.(*AudioStream); ok {
		switch audio.StreamType {
		case StreamTypeAC3TrueHDAudio:
			if audio.HasExtensions {
				return "Dolby Atmos"
			}
			return "Dolby TrueHD"
		case StreamTypeDTSHDAudio:
			if audio.HasExtensions {
				return "DTS:X Hi-Res"
			}
			return "DTS-HD Hi-Res"
		case StreamTypeDTSHDMasterAudio:
			if audio.HasExtensions {
				return "DTS:X Master"
			}
			return "DTS-HD Master"
		}
	}
	return base.CodecAltName()
}

func (s *Stream) CodecShortName() string {
	switch s.StreamType {
	case StreamTypeMPEG1Video:
		return "MPEG-1"
	case StreamTypeMPEG2Video:
		return "MPEG-2"
	case StreamTypeAVCVideo:
		return "AVC"
	case StreamTypeMVCVideo:
		return "MVC"
	case StreamTypeHEVCVideo:
		return "HEVC"
	case StreamTypeVC1Video:
		return "VC-1"
	case StreamTypeMPEG1Audio:
		return "MP1"
	case StreamTypeMPEG2Audio:
		return "MP2"
	case StreamTypeMPEG2AACAudio:
		return "MPEG-2 AAC"
	case StreamTypeMPEG4AACAudio:
		return "MPEG-4 AAC"
	case StreamTypeLPCMAudio:
		return "LPCM"
	case StreamTypeAC3Audio:
		return "AC3"
	case StreamTypeAC3PlusAudio, StreamTypeAC3PlusSecondaryAudio:
		return "AC3+"
	case StreamTypeAC3TrueHDAudio:
		return "TrueHD"
	case StreamTypeDTSAudio:
		return "DTS"
	case StreamTypeDTSHDAudio:
		return "DTS-HD HR"
	case StreamTypeDTSHDSecondaryAudio:
		return "DTS Express"
	case StreamTypeDTSHDMasterAudio:
		return "DTS-HD MA"
	case StreamTypePresentationGraphics:
		return "PGS"
	case StreamTypeInteractiveGraphics:
		return "IGS"
	case StreamTypeSubtitle:
		return "SUB"
	default:
		return "UNKNOWN"
	}
}

// CodecShortNameForInfo returns short codec name with audio extensions applied.
func CodecShortNameForInfo(info Info) string {
	if info == nil {
		return "UNKNOWN"
	}
	base := info.Base()
	if audio, ok := info.(*AudioStream); ok {
		switch audio.StreamType {
		case StreamTypeAC3Audio:
			if audio.AudioMode == AudioModeExtended {
				return "AC3-EX"
			}
			return "AC3"
		case StreamTypeAC3TrueHDAudio:
			if audio.HasExtensions {
				return "Atmos"
			}
			return "TrueHD"
		case StreamTypeDTSAudio:
			if audio.AudioMode == AudioModeExtended {
				return "DTS-ES"
			}
			return "DTS"
		case StreamTypeDTSHDAudio:
			if audio.HasExtensions {
				return "DTS:X HR"
			}
			return "DTS-HD HR"
		case StreamTypeDTSHDMasterAudio:
			if audio.HasExtensions {
				return "DTS:X MA"
			}
			return "DTS-HD MA"
		}
	}
	return base.CodecShortName()
}

func (s *Stream) Description() string {
	return ""
}

func (s *Stream) Base() *Stream {
	return s
}

func (s *Stream) Clone() Info {
	clone := *s
	if s.Descriptors != nil {
		clone.Descriptors = make([]Descriptor, len(s.Descriptors))
		for i, d := range s.Descriptors {
			clone.Descriptors[i] = d.Clone()
		}
	}
	return &clone
}

// VideoStream adds video metadata.
type VideoStream struct {
	Stream

	Width  int
	Height int
	IsInterlaced bool
	FrameRateEnum int
	FrameRateDen  int
	AspectRatio   AspectRatio
	EncodingProfile string
	ExtendedData any

	videoFormat VideoFormat
	frameRate FrameRate
}

func (v *VideoStream) VideoFormat() VideoFormat {
	return v.videoFormat
}

func (v *VideoStream) SetVideoFormat(format VideoFormat) {
	v.videoFormat = format
	switch format {
	case VideoFormat480i:
		v.Height = 480
		v.IsInterlaced = true
	case VideoFormat480p:
		v.Height = 480
		v.IsInterlaced = false
	case VideoFormat576i:
		v.Height = 576
		v.IsInterlaced = true
	case VideoFormat576p:
		v.Height = 576
		v.IsInterlaced = false
	case VideoFormat720p:
		v.Height = 720
		v.IsInterlaced = false
	case VideoFormat1080i:
		v.Height = 1080
		v.IsInterlaced = true
	case VideoFormat1080p:
		v.Height = 1080
		v.IsInterlaced = false
	case VideoFormat2160p:
		v.Height = 2160
		v.IsInterlaced = false
	}
}

func (v *VideoStream) FrameRate() FrameRate {
	return v.frameRate
}

func (v *VideoStream) SetFrameRate(rate FrameRate) {
	v.frameRate = rate
	switch rate {
	case FrameRate23976:
		v.FrameRateEnum = 24000
		v.FrameRateDen = 1001
	case FrameRate24:
		v.FrameRateEnum = 24000
		v.FrameRateDen = 1000
	case FrameRate25:
		v.FrameRateEnum = 25000
		v.FrameRateDen = 1000
	case FrameRate2997:
		v.FrameRateEnum = 30000
		v.FrameRateDen = 1001
	case FrameRate50:
		v.FrameRateEnum = 50000
		v.FrameRateDen = 1000
	case FrameRate5994:
		v.FrameRateEnum = 60000
		v.FrameRateDen = 1001
	}
}

func (v *VideoStream) Description() string {
	description := ""

	if v.BaseView != nil {
		if *v.BaseView {
			description += "Right Eye"
		} else {
			description += "Left Eye"
		}
		description += " / "
	}

	if v.Height > 0 {
		if v.IsInterlaced {
			description += fmt.Sprintf("%di / ", v.Height)
		} else {
			description += fmt.Sprintf("%dp / ", v.Height)
		}
	}
	if v.FrameRateEnum > 0 && v.FrameRateDen > 0 {
		if v.FrameRateEnum%v.FrameRateDen == 0 {
			description += fmt.Sprintf("%d fps / ", v.FrameRateEnum/v.FrameRateDen)
		} else {
			description += fmt.Sprintf("%.3f fps / ", float64(v.FrameRateEnum)/float64(v.FrameRateDen))
		}
	}
	switch v.AspectRatio {
	case Aspect43:
		description += "4:3 / "
	case Aspect169:
		description += "16:9 / "
	}
	if v.EncodingProfile != "" {
		description += v.EncodingProfile + " / "
	}
	if v.StreamType == StreamTypeHEVCVideo && v.ExtendedData != nil {
		if ext, ok := v.ExtendedData.(*HEVCExtendedData); ok {
			if len(ext.ExtendedFormatInfo) > 0 {
				description += strings.Join(ext.ExtendedFormatInfo, " / ")
			}
		}
	}
	if strings.HasSuffix(description, " / ") {
		description = strings.TrimSuffix(description, " / ")
	}
	return description
}

func (v *VideoStream) Base() *Stream {
	return &v.Stream
}

func (v *VideoStream) Clone() Info {
	clone := *v
	streamClone := v.Stream.Clone().(*Stream)
	clone.Stream = *streamClone
	return &clone
}

// AudioMode mirrors TSAudioMode.
type AudioMode int

const (
	AudioModeUnknown AudioMode = iota
	AudioModeDualMono
	AudioModeStereo
	AudioModeSurround
	AudioModeExtended
	AudioModeJointStereo
	AudioModeMono
)

// AudioStream adds audio metadata.
type AudioStream struct {
	Stream

	SampleRate int
	ChannelCount int
	BitDepth int
	LFE int
	DialNorm int
	HasExtensions bool
	ExtendedData any
	AudioMode AudioMode
	CoreStream *AudioStream
	ChannelLayout ChannelLayout
}

func ConvertSampleRate(rate SampleRate) int {
	switch rate {
	case SampleRate48:
		return 48000
	case SampleRate96, SampleRate4896:
		return 96000
	case SampleRate192, SampleRate48192:
		return 192000
	default:
		return 0
	}
}

func (a *AudioStream) ChannelDescription() string {
	description := ""
	if a.ChannelCount > 0 {
		description += fmt.Sprintf("%d.%d", a.ChannelCount, a.LFE)
	} else {
		switch a.ChannelLayout {
		case ChannelLayoutMono:
			description += "1.0"
		case ChannelLayoutStereo:
			description += "2.0"
		case ChannelLayoutMulti:
			description += "5.1"
		}
	}

	if a.AudioMode != AudioModeExtended {
		return description
	}

	switch a.StreamType {
	case StreamTypeAC3Audio:
		description += "-EX"
	case StreamTypeDTSAudio, StreamTypeDTSHDAudio, StreamTypeDTSHDMasterAudio:
		description += "-ES"
	}

	return description
}

func (a *AudioStream) Description() string {
	description := a.ChannelDescription()

	if a.SampleRate > 0 {
		description += fmt.Sprintf(" / %d kHz", a.SampleRate/1000)
	}
	if a.BitRate > 0 {
		coreBitRate := int64(0)
		if a.StreamType == StreamTypeAC3TrueHDAudio && a.CoreStream != nil {
			coreBitRate = a.CoreStream.BitRate
		}
		description += fmt.Sprintf(" / %5d kbps", int64(float64(a.BitRate-coreBitRate)/1000+0.5))
	}
	if a.BitDepth > 0 {
		description += fmt.Sprintf(" / %d-bit", a.BitDepth)
	}
	if a.DialNorm != 0 {
		description += fmt.Sprintf(" / DN %ddB", a.DialNorm)
	}
	if a.ChannelCount == 2 {
		switch a.AudioMode {
		case AudioModeDualMono:
			description += " / Dual Mono"
		case AudioModeSurround:
			description += " / Dolby Surround"
		case AudioModeJointStereo:
			description += " / Joint Stereo"
		}
	}
	if strings.HasSuffix(description, " / ") {
		description = strings.TrimSuffix(description, " / ")
	}

	if a.CoreStream == nil {
		return description
	}

	codec := ""
	switch a.CoreStream.StreamType {
	case StreamTypeAC3Audio:
		codec = "AC3 Embedded"
	case StreamTypeDTSAudio:
		codec = "DTS Core"
	case StreamTypeAC3PlusAudio:
		codec = "DD+ Embedded"
	}
	if codec != "" {
		description += fmt.Sprintf(" (%s: %s)", codec, a.CoreStream.Description())
	}
	return description
}

func (a *AudioStream) Base() *Stream {
	return &a.Stream
}

func (a *AudioStream) Clone() Info {
	clone := *a
	streamClone := a.Stream.Clone().(*Stream)
	clone.Stream = *streamClone
	if a.CoreStream != nil {
		clone.CoreStream = a.CoreStream.Clone().(*AudioStream)
	}
	return &clone
}

// GraphicsStream for PGS/IGS.
type GraphicsStream struct {
	Stream
	Width int
	Height int
	Captions int
	ForcedCaptions int
	CaptionIDs map[int]any
	LastFrame any
}

func NewGraphicsStream() *GraphicsStream {
	return &GraphicsStream{
		Stream: Stream{IsVBR: true, IsInitialized: false},
		CaptionIDs: make(map[int]any),
	}
}

func (g *GraphicsStream) Description() string {
	description := ""
	if g.Width > 0 || g.Height > 0 {
		description = fmt.Sprintf("%dx%d", g.Width, g.Height)
	}
	if g.Captions > 0 {
		description += fmt.Sprintf(" / %d Caption", g.Captions)
		if g.Captions != 1 {
			description += "s"
		}
	}
	if g.ForcedCaptions > 0 {
		forced := fmt.Sprintf("%d Forced Caption", g.ForcedCaptions)
		if g.ForcedCaptions != 1 {
			forced += "s"
		}
		if g.Captions > 0 {
			description += fmt.Sprintf(" (%s)", forced)
		} else {
			description += " / " + forced
		}
	}
	return description
}

func (g *GraphicsStream) Base() *Stream {
	return &g.Stream
}

func (g *GraphicsStream) Clone() Info {
	clone := *g
	streamClone := g.Stream.Clone().(*Stream)
	clone.Stream = *streamClone
	return &clone
}

// TextStream for subtitles.
type TextStream struct {
	Stream
}

// HEVCExtendedData holds HEVC extended format info for descriptions.
type HEVCExtendedData struct {
	ExtendedFormatInfo []string
}

func NewTextStream() *TextStream {
	return &TextStream{Stream: Stream{IsVBR: true, IsInitialized: true}}
}

func (t *TextStream) Base() *Stream {
	return &t.Stream
}

func (t *TextStream) Clone() Info {
	clone := *t
	streamClone := t.Stream.Clone().(*Stream)
	clone.Stream = *streamClone
	return &clone
}
