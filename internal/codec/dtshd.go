package codec

import (
	"encoding/binary"

	"github.com/autobrr/go-bdinfo/internal/buffer"
	"github.com/autobrr/go-bdinfo/internal/stream"
)

var dtsHdSampleRates = []int{
	0x1F40, 0x3E80, 0x7D00, 0x0FA00, 0x1F400, 0x5622, 0x0AC44, 0x15888,
	0x2B110, 0x56220, 0x2EE0, 0x5DC0, 0x0BB80, 0x17700, 0x2EE00, 0x5DC00,
}

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

	br := buffer.NewBitReader(data[syncOffset+4:])
	if !br.SkipBits(8) {
		return
	}
	subStreamIndex, ok := br.ReadBits(2)
	if !ok {
		return
	}
	blownUpHeaderBit, ok := br.ReadBits(1)
	if !ok {
		return
	}
	blownUpHeader := blownUpHeaderBit == 1
	if blownUpHeader {
		if !br.SkipBits(32) {
			return
		}
	} else {
		if !br.SkipBits(24) {
			return
		}
	}

	numAssets := uint64(1)
	staticFieldsBit, ok := br.ReadBits(1)
	if !ok {
		return
	}
	staticFields := staticFieldsBit == 1
	if staticFields {
		if !br.SkipBits(5) {
			return
		}
		flag, ok := br.ReadBits(1)
		if !ok {
			return
		}
		if flag == 1 {
			if !br.SkipBits(36) {
				return
			}
		}
		numAudioPresentBits, ok := br.ReadBits(3)
		if !ok {
			return
		}
		numAudioPresent := int(numAudioPresentBits) + 1
		numAssetsBits, ok := br.ReadBits(3)
		if !ok {
			return
		}
		numAssets = numAssetsBits + 1
		for range numAudioPresent {
			if _, ok := br.ReadBits(int(subStreamIndex) + 1); !ok {
				return
			}
		}
		for range numAudioPresent {
			for j := 0; j < int(subStreamIndex)+1; j++ {
				if (j+1)%2 == 1 {
					if !br.SkipBits(8) {
						return
					}
				}
			}
		}
		flag, ok = br.ReadBits(1)
		if !ok {
			return
		}
		if flag == 1 {
			if !br.SkipBits(2) {
				return
			}
			bits4MixOutMask, ok := br.ReadBits(2)
			if !ok {
				return
			}
			numBits := int(bits4MixOutMask)*4 + 4
			numMixOutConfigsBits, ok := br.ReadBits(2)
			if !ok {
				return
			}
			numMixOutConfigs := int(numMixOutConfigsBits) + 1
			for range numMixOutConfigs {
				if _, ok := br.ReadBits(numBits); !ok {
					return
				}
			}
		}
	}

	for i := 0; i < int(numAssets); i++ {
		if blownUpHeader {
			if _, ok := br.ReadBits(20); !ok {
				return
			}
		} else {
			if _, ok := br.ReadBits(16); !ok {
				return
			}
		}
	}

	for i := 0; i < int(numAssets); i++ {
		if !br.SkipBits(12) {
			return
		}
		if staticFields {
			flag, ok := br.ReadBits(1)
			if !ok {
				return
			}
			if flag == 1 {
				if !br.SkipBits(4) {
					return
				}
			}
			flag, ok = br.ReadBits(1)
			if !ok {
				return
			}
			if flag == 1 {
				if !br.SkipBits(24) {
					return
				}
			}
			flag, ok = br.ReadBits(1)
			if !ok {
				return
			}
			if flag == 1 {
				infoTextBytes, ok := br.ReadBits(10)
				if !ok {
					return
				}
				if !br.SkipBits(int(infoTextBytes+1) * 8) {
					return
				}
			}
			bitResolution, ok := br.ReadBits(5)
			if !ok {
				return
			}
			maxSampleRate, ok := br.ReadBits(4)
			if !ok {
				return
			}
			totalChannels, ok := br.ReadBits(8)
			if !ok {
				return
			}
			totalChannels++
			speakerMask := uint64(0)
			flag, ok = br.ReadBits(1)
			if !ok {
				return
			}
			if flag == 1 {
				if totalChannels > 2 {
					if !br.SkipBits(1) {
						return
					}
				}
				if totalChannels > 6 {
					if !br.SkipBits(1) {
						return
					}
				}
				flag, ok = br.ReadBits(1)
				if !ok {
					return
				}
				if flag == 1 {
					bits4Mask, ok := br.ReadBits(2)
					if !ok {
						return
					}
					maskBits := int(bits4Mask)*4 + 4
					speakerMask, ok = br.ReadBits(maskBits)
					if !ok {
						return
					}
				}
			}
			if int(maxSampleRate) < len(dtsHdSampleRates) {
				a.SampleRate = dtsHdSampleRates[maxSampleRate]
			}
			a.BitDepth = int(bitResolution + 1)
			a.LFE = 0
			if (speakerMask & 0x8) != 0 {
				a.LFE++
			}
			if (speakerMask & 0x1000) != 0 {
				a.LFE++
			}
			a.ChannelCount = int(totalChannels) - a.LFE
			if layout := dtsHDSpeakerActivityMaskChannelLayout(uint16(speakerMask)); layout != "" {
				a.ChannelLayoutText = layout
			}
		}
		if numAssets > 1 {
			break
		}
	}

	a.HasExtensions = detectDTSX(data[syncOffset:])

	if a.CoreStream != nil && a.CoreStream.AudioMode == stream.AudioModeExtended && a.ChannelCount == 5 {
		a.AudioMode = stream.AudioModeExtended
	}

	if a.StreamType == stream.StreamTypeDTSHDMasterAudio {
		a.IsVBR = true
		a.IsInitialized = true
	} else if fallbackBitrate > 0 {
		a.IsVBR = false
		a.BitRate = fallbackBitrate
		if a.CoreStream != nil {
			a.BitRate += a.CoreStream.BitRate
		}
		a.IsInitialized = a.BitRate > 0
	}

	if a.SampleRate == 0 && a.CoreStream != nil {
		a.SampleRate = a.CoreStream.SampleRate
	}
	if a.ChannelCount == 0 && a.CoreStream != nil {
		a.ChannelCount = a.CoreStream.ChannelCount
	}
	if a.LFE == 0 && a.CoreStream != nil {
		a.LFE = a.CoreStream.LFE
	}
	if a.BitDepth == 0 && a.CoreStream != nil {
		a.BitDepth = a.CoreStream.BitDepth
	}
}

// dtsHDSpeakerActivityMaskChannelLayout converts a DTS-HD ExSS speaker activity mask
// into MediaInfo-compatible speaker labels.
func dtsHDSpeakerActivityMaskChannelLayout(mask uint16) string {
	if mask == 1 {
		return "M"
	}
	out := ""
	if mask&0x0001 != 0 {
		out += " C"
	}
	if mask&0x0002 != 0 {
		out += " L R"
	}
	if mask&0x0004 != 0 {
		out += " Ls Rs"
	}
	if mask&0x0008 != 0 {
		out += " LFE"
	}
	if mask&0x0010 != 0 {
		out += " Cs"
	}
	if mask&0x0020 != 0 {
		out += " Lh Rh"
	}
	if mask&0x0040 != 0 {
		out += " Lsr Rsr"
	}
	if mask&0x0080 != 0 {
		out += " Ch"
	}
	if mask&0x0100 != 0 {
		out += " Oh"
	}
	if mask&0x0200 != 0 {
		out += " Lc Rc"
	}
	if mask&0x0400 != 0 {
		out += " Lw Rw"
	}
	if mask&0x0800 != 0 {
		out += " Lss Rss"
	}
	if mask&0x1000 != 0 {
		out += " LFE2"
	}
	if mask&0x2000 != 0 {
		out += " Lhs Rhs"
	}
	if mask&0x4000 != 0 {
		out += " Chr"
	}
	if mask&0x8000 != 0 {
		out += " Lhr Rhr"
	}
	if out == "" {
		return ""
	}
	return out[1:]
}

func detectDTSX(data []byte) bool {
	var temp uint32
	for i := range data {
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
