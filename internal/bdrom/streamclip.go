// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bdrom

import (
	"math"

	"github.com/autobrr/go-bdinfo/internal/settings"
)

type StreamClip struct {
	Settings        settings.Settings
	AngleIndex      int
	Name            string
	TimeIn          float64
	TimeOut         float64
	RelativeTimeIn  float64
	RelativeTimeOut float64
	Length          float64
	RelativeLength  float64

	FileSize            uint64
	InterleavedFileSize uint64
	PayloadBytes        uint64
	PacketCount         uint64
	PacketSeconds       float64

	Chapters []float64

	StreamFile     *StreamFile
	StreamClipFile *StreamClipFile
}

func NewStreamClip(streamFile *StreamFile, streamClipFile *StreamClipFile, settings settings.Settings) *StreamClip {
	clip := &StreamClip{Settings: settings}
	if streamFile != nil {
		clip.Name = streamFile.Name
		clip.StreamFile = streamFile
		if streamFile.Size > 0 {
			clip.FileSize = uint64(streamFile.Size)
		}
		if streamFile.InterleavedFile != nil && streamFile.InterleavedFile.Size > 0 {
			clip.InterleavedFileSize = uint64(streamFile.InterleavedFile.Size)
		}
	}
	clip.StreamClipFile = streamClipFile
	return clip
}

func (s *StreamClip) DisplayName() string {
	if s.StreamFile != nil && s.StreamFile.InterleavedFile != nil && s.Settings.EnableSSIF {
		return s.StreamFile.InterleavedFile.Name
	}
	return s.Name
}

func (s *StreamClip) PacketSize() uint64 {
	return s.PacketCount * 192
}

func (s *StreamClip) PacketBitRate() uint64 {
	if s.PacketSeconds > 0 {
		return uint64(math.RoundToEven(float64(s.PacketSize()) * 8.0 / s.PacketSeconds))
	}
	return 0
}

func (s *StreamClip) IsCompatible(other *StreamClip) bool {
	if s.StreamFile == nil || other.StreamFile == nil {
		return false
	}
	for pid, stream1 := range s.StreamFile.Streams {
		stream2, ok := other.StreamFile.Streams[pid]
		if !ok {
			continue
		}
		if stream1.Base().StreamType != stream2.Base().StreamType {
			return false
		}
	}
	return true
}
