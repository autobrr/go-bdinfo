package codec

import (
	"encoding/binary"

	"github.com/autobrr/go-bdinfo/internal/stream"
)

func ScanDTSHD(a *stream.AudioStream, data []byte, fallbackBitrate int64) {
	if a.IsInitialized && (a.StreamType == stream.StreamTypeDTSHDSecondaryAudio || (a.CoreStream != nil && a.CoreStream.IsInitialized)) {
		return
	}

	syncOffset := -1
	for i := 0; i+4 <= len(data); i++ {
		if binary.BigEndian.Uint32(data[i:i+4]) == 0x64582025 {
			syncOffset = i
			break
		}
	}
	if syncOffset == -1 {
		if a.CoreStream == nil {
			a.CoreStream = &stream.AudioStream{Stream: stream.Stream{StreamType: stream.StreamTypeDTSAudio}}
		}
		if !a.CoreStream.IsInitialized {
			ScanDTS(a.CoreStream, data, fallbackBitrate)
		}
		return
	}

	if a.CoreStream == nil {
		a.CoreStream = &stream.AudioStream{Stream: stream.Stream{StreamType: stream.StreamTypeDTSAudio}}
	}
	if !a.CoreStream.IsInitialized {
		ScanDTS(a.CoreStream, data, fallbackBitrate)
	}

	a.HasExtensions = detectDTSX(data[syncOffset:])
	a.IsVBR = true
	if a.SampleRate == 0 {
		a.SampleRate = a.CoreStream.SampleRate
	}
	if a.ChannelCount == 0 {
		a.ChannelCount = a.CoreStream.ChannelCount
	}
	if a.LFE == 0 {
		a.LFE = a.CoreStream.LFE
	}
	a.IsInitialized = true
}

func detectDTSX(data []byte) bool {
	var temp uint32
	for i := 0; i < len(data); i++ {
		temp = (temp << 8) | uint32(data[i])
		switch temp {
		case 0x41A29547, // XLL Extended data
			0x655E315E, // XBR Extended data
			0x0A801921, // XSA Extended data
			0x1D95F262, // X96k
			0x47004A03, // XXch
			0x5A5A5A5A: // Xch
			var temp2 uint32
			for j := i + 1; j < len(data); j++ {
				temp2 = (temp2 << 8) | uint32(data[j])
				if temp2 == 0x02000850 {
					return true
				}
			}
		}
	}
	return false
}
