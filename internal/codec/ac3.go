package codec

import (
	"github.com/autobrr/go-bdinfo/internal/buffer"
	"github.com/autobrr/go-bdinfo/internal/stream"
)

var ac3BitrateKbps = []int{32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 384, 448, 512, 576, 640}
var ac3Channels = []int{2, 1, 2, 3, 3, 4, 4, 5}

func ScanAC3(a *stream.AudioStream, data []byte) {
	if a.IsInitialized {
		return
	}
	if len(data) < 7 {
		return
	}
	if data[0] != 0x0b || data[1] != 0x77 {
		return
	}

	br := buffer.NewBitReader(data)
	read := func(bits int) uint64 {
		v, _ := br.ReadBits(bits)
		return v
	}

	_ = read(16) // sync
	_ = read(16) // crc1
	srCode := read(2)
	frameSizeCode := read(6)
	bsid := read(5)
	_ = read(3) // bsmod
	channelMode := read(3)
	if (channelMode&0x1) > 0 && channelMode != 0x1 {
		_ = read(2)
	}
	if (channelMode & 0x4) > 0 {
		_ = read(2)
	}
	if channelMode == 0x2 {
		dsurmod := read(2)
		if dsurmod == 0x2 {
			a.AudioMode = stream.AudioModeSurround
		}
	}
	lfeOn := read(1)
	dialNorm := read(5)
	if read(1) == 1 {
		_ = read(8)
	}
	if read(1) == 1 {
		_ = read(8)
	}
	if read(1) == 1 {
		_ = read(7)
	}
	if channelMode == 0 {
		_ = read(5)
		if read(1) == 1 {
			_ = read(8)
		}
		if read(1) == 1 {
			_ = read(8)
		}
		if read(1) == 1 {
			_ = read(7)
		}
	}
	_ = read(2)
	if bsid == 6 {
		if read(1) == 1 {
			_ = read(14)
		}
		if read(1) == 1 {
			dsurexmod := read(2)
			_ = read(2) // dheadphonmod
			_ = read(10)
			if dsurexmod == 2 {
				a.AudioMode = stream.AudioModeExtended
			}
		}
	}

	if bsid <= 10 {
		sampleRates := []int{48000, 44100, 32000}
		if srCode < 3 {
			a.SampleRate = sampleRates[srCode]
		}
		if int(frameSizeCode/2) < len(ac3BitrateKbps) {
			a.BitRate = int64(ac3BitrateKbps[frameSizeCode/2] * 1000)
		}
		if int(channelMode) < len(ac3Channels) {
			a.ChannelCount = ac3Channels[channelMode]
		}
		if lfeOn > 0 {
			a.LFE = 1
		}
		a.DialNorm = -int(dialNorm)
		if a.AudioMode == stream.AudioModeUnknown {
			switch channelMode {
			case 0:
				a.AudioMode = stream.AudioModeDualMono
			case 2:
				a.AudioMode = stream.AudioModeStereo
			}
		}
		a.IsInitialized = true
		return
	}

	// E-AC3 minimal parse
	br = buffer.NewBitReader(data)
	read = func(bits int) uint64 {
		v, _ := br.ReadBits(bits)
		return v
	}
	_ = read(16) // sync
	_ = read(16) // crc1
	_ = read(2)  // strmtyp
	_ = read(3)  // substreamid
	_ = read(11) // frmsiz
	fscod := read(2)
	if fscod == 3 {
		fscod2 := read(2)
		sampleRates2 := []int{24000, 22050, 16000}
		if fscod2 < 3 {
			a.SampleRate = sampleRates2[fscod2]
		}
	} else {
		sampleRates := []int{48000, 44100, 32000}
		if fscod < 3 {
			a.SampleRate = sampleRates[fscod]
		}
		_ = read(2) // numblkscod
	}
	acmod := read(3)
	lfeon := read(1)
	if int(acmod) < len(ac3Channels) {
		a.ChannelCount = ac3Channels[acmod]
	}
	if lfeon > 0 {
		a.LFE = 1
	}
	if a.AudioMode == stream.AudioModeUnknown {
		switch acmod {
		case 0:
			a.AudioMode = stream.AudioModeDualMono
		case 2:
			a.AudioMode = stream.AudioModeStereo
		}
	}
	// Atmos JOC detection not fully implemented; mark extensions for E-AC3
	a.HasExtensions = true
	a.IsInitialized = true
}
