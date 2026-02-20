package codec

import "github.com/autobrr/go-bdinfo/internal/stream"

func ScanLPCM(a *stream.AudioStream, data []byte) {
	if a.IsInitialized {
		return
	}

	// BDInfo TSCodecLPCM: parse 4-byte LPCM header, where bits/sample rate/channel config are in bytes 2-3.
	if len(data) >= 4 {
		flags := uint16(data[2])<<8 | uint16(data[3])

		switch (flags & 0xF000) >> 12 {
		case 1: // 1/0/0
			a.ChannelCount = 1
			a.LFE = 0
		case 3: // 2/0/0
			a.ChannelCount = 2
			a.LFE = 0
		case 4: // 3/0/0
			a.ChannelCount = 3
			a.LFE = 0
		case 5: // 2/1/0
			a.ChannelCount = 3
			a.LFE = 0
		case 6: // 3/1/0
			a.ChannelCount = 4
			a.LFE = 0
		case 7: // 2/2/0
			a.ChannelCount = 4
			a.LFE = 0
		case 8: // 3/2/0
			a.ChannelCount = 5
			a.LFE = 0
		case 9: // 3/2/1
			a.ChannelCount = 5
			a.LFE = 1
		case 10: // 3/4/0
			a.ChannelCount = 7
			a.LFE = 0
		case 11: // 3/4/1
			a.ChannelCount = 7
			a.LFE = 1
		default:
			a.ChannelCount = 0
			a.LFE = 0
		}

		switch (flags & 0x00C0) >> 6 {
		case 1:
			a.BitDepth = 16
		case 2:
			a.BitDepth = 20
		case 3:
			a.BitDepth = 24
		default:
			a.BitDepth = 0
		}

		switch (flags & 0x0F00) >> 8 {
		case 1:
			a.SampleRate = 48000
		case 4:
			a.SampleRate = 96000
		case 5:
			a.SampleRate = 192000
		default:
			a.SampleRate = 0
		}
	}

	if a.SampleRate > 0 && a.BitDepth > 0 && a.ChannelCount+a.LFE > 0 {
		a.BitRate = int64(a.SampleRate * a.BitDepth * (a.ChannelCount + a.LFE))
	}
	a.IsVBR = false
	a.IsInitialized = true
}
