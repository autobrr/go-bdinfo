// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package codec

import (
	"encoding/binary"

	"github.com/autobrr/go-bdinfo/internal/buffer"
	"github.com/autobrr/go-bdinfo/internal/stream"
)

var dtsSampleRates = []int{0, 8000, 16000, 32000, 0, 0, 11025, 22050, 44100, 0, 0, 12000, 24000, 48000, 96000, 192000}
var dtsBitRates = []int{32000, 56000, 64000, 96000, 112000, 128000, 192000, 224000, 256000, 320000, 384000, 448000, 512000, 576000, 640000, 768000, 896000, 1024000, 1152000, 1280000, 1344000, 1408000, 1411200, 1472000, 1509000, 1920000, 2048000, 3072000, 3840000, 1, 2, 3}
var dtsBitsPerSample = []int{16, 16, 20, 20, 0, 24, 24}

func ScanDTS(a *stream.AudioStream, data []byte, fallbackBitrate int64) {
	if a.IsInitialized {
		return
	}
	syncOffset := -1
	for i := 0; i+4 <= len(data); i++ {
		if binary.BigEndian.Uint32(data[i:i+4]) == 0x7FFE8001 {
			syncOffset = i
			break
		}
	}
	if syncOffset == -1 {
		return
	}
	br := buffer.NewBitReader(data[syncOffset+4:])
	_, _ = br.ReadBits(6)
	crcPresent, _ := br.ReadBits(1)
	_, _ = br.ReadBits(7)
	frameSize, _ := br.ReadBits(14)
	if frameSize < 95 {
		return
	}
	_, _ = br.ReadBits(6)
	sampleRateIdx, _ := br.ReadBits(4)
	if int(sampleRateIdx) >= len(dtsSampleRates) {
		return
	}
	bitRateIdx, _ := br.ReadBits(5)
	if int(bitRateIdx) >= len(dtsBitRates) {
		return
	}
	_, _ = br.ReadBits(8)
	extCoding, _ := br.ReadBits(1)
	_, _ = br.ReadBits(1)
	lfe, _ := br.ReadBits(2)
	_, _ = br.ReadBits(1)
	if crcPresent == 1 {
		_, _ = br.ReadBits(16)
	}
	_, _ = br.ReadBits(7)
	sourcePcmRes, _ := br.ReadBits(3)
	_, _ = br.ReadBits(2)
	dialogNorm, _ := br.ReadBits(4)
	if int(sourcePcmRes) >= len(dtsBitsPerSample) {
		return
	}
	_, _ = br.ReadBits(4)
	totalChannels, _ := br.ReadBits(3)
	totalChannels = totalChannels + 1 + extCoding

	a.SampleRate = dtsSampleRates[sampleRateIdx]
	a.ChannelCount = int(totalChannels)
	if lfe > 0 {
		a.LFE = 1
	}
	a.BitDepth = dtsBitsPerSample[sourcePcmRes]
	a.DialNorm = int(-dialogNorm)
	if (sourcePcmRes & 0x1) == 0x1 {
		a.AudioMode = stream.AudioModeExtended
	}

	bitRate := dtsBitRates[bitRateIdx]
	switch bitRate {
	case 1:
		if fallbackBitrate > 0 {
			a.BitRate = fallbackBitrate
			a.IsVBR = false
			a.IsInitialized = true
		} else {
			a.BitRate = 0
		}
	case 2, 3:
		a.IsVBR = true
		a.IsInitialized = true
	default:
		a.BitRate = int64(bitRate)
		a.IsVBR = false
		a.IsInitialized = true
	}
}
