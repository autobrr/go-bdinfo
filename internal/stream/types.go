package stream

// StreamType mirrors BDInfo TSStreamType.
type StreamType uint8

const (
	StreamTypeUnknown               StreamType = 0x00
	StreamTypeMPEG1Video            StreamType = 0x01
	StreamTypeMPEG2Video            StreamType = 0x02
	StreamTypeAVCVideo              StreamType = 0x1b
	StreamTypeMVCVideo              StreamType = 0x20
	StreamTypeHEVCVideo             StreamType = 0x24
	StreamTypeVC1Video              StreamType = 0xea
	StreamTypeMPEG1Audio            StreamType = 0x03
	StreamTypeMPEG2Audio            StreamType = 0x04
	StreamTypeMPEG2AACAudio         StreamType = 0x0f
	StreamTypeMPEG4AACAudio         StreamType = 0x11
	StreamTypeLPCMAudio             StreamType = 0x80
	StreamTypeAC3Audio              StreamType = 0x81
	StreamTypeAC3PlusAudio          StreamType = 0x84
	StreamTypeAC3PlusSecondaryAudio StreamType = 0xa1
	StreamTypeAC3TrueHDAudio        StreamType = 0x83
	StreamTypeDTSAudio              StreamType = 0x82
	StreamTypeDTSHDAudio            StreamType = 0x85
	StreamTypeDTSHDSecondaryAudio   StreamType = 0xa2
	StreamTypeDTSHDMasterAudio      StreamType = 0x86
	StreamTypePresentationGraphics  StreamType = 0x90
	StreamTypeInteractiveGraphics   StreamType = 0x91
	StreamTypeSubtitle              StreamType = 0x92
)

type VideoFormat uint8

const (
	VideoFormatUnknown VideoFormat = 0
	VideoFormat480i    VideoFormat = 1
	VideoFormat576i    VideoFormat = 2
	VideoFormat480p    VideoFormat = 3
	VideoFormat1080i   VideoFormat = 4
	VideoFormat720p    VideoFormat = 5
	VideoFormat1080p   VideoFormat = 6
	VideoFormat576p    VideoFormat = 7
	VideoFormat2160p   VideoFormat = 8
)

type FrameRate uint8

const (
	FrameRateUnknown FrameRate = 0
	FrameRate23976   FrameRate = 1
	FrameRate24      FrameRate = 2
	FrameRate25      FrameRate = 3
	FrameRate2997    FrameRate = 4
	FrameRate50      FrameRate = 6
	FrameRate5994    FrameRate = 7
)

type ChannelLayout uint8

const (
	ChannelLayoutUnknown ChannelLayout = 0
	ChannelLayoutMono    ChannelLayout = 1
	ChannelLayoutStereo  ChannelLayout = 3
	ChannelLayoutMulti   ChannelLayout = 6
	ChannelLayoutCombo   ChannelLayout = 12
)

type SampleRate uint8

const (
	SampleRateUnknown SampleRate = 0
	SampleRate48      SampleRate = 1
	SampleRate96      SampleRate = 4
	SampleRate192     SampleRate = 5
	SampleRate48192   SampleRate = 12
	SampleRate4896    SampleRate = 14
)

type AspectRatio uint8

const (
	AspectUnknown AspectRatio = 0
	Aspect43      AspectRatio = 2
	Aspect169     AspectRatio = 3
	Aspect221     AspectRatio = 4
)

// Descriptor mirrors TSDescriptor.
type Descriptor struct {
	Name  byte
	Value []byte
}

func (d Descriptor) Clone() Descriptor {
	value := make([]byte, len(d.Value))
	copy(value, d.Value)
	return Descriptor{Name: d.Name, Value: value}
}
