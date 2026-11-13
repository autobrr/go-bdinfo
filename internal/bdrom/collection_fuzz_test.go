// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bdrom

import (
	"context"
	"testing"

	"github.com/autobrr/go-bdinfo/internal/settings"
	"github.com/autobrr/go-bdinfo/internal/stream"
	"github.com/autobrr/go-bdinfo/pkg/bdinfo/video"
)

func FuzzVideoCollection(f *testing.F) {
	f.Add(append(collectionPacket(0x1011, 0, true, collectionPES([]byte{1}, 0, 0, true)), collectionPacket(0x1011, 1, true, []byte{0, 0, 1})...))
	f.Add(make([]byte, 188))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 256<<10 {
			return
		}
		s := NewStreamFile(&memFileInfo{name: "FUZZ.M2TS"})
		s.Streams[0x1011] = &stream.VideoStream{Stream: stream.Stream{PID: 0x1011, StreamType: stream.StreamTypeHEVCVideo}}
		r := &recordingVideo{}
		s.VideoConsumer = func(context.Context, video.StreamInfo) (video.Consumer, error) { return r, nil }
		c := s.newVideoCollection(t.Context(), nil, streamSource(s, false))
		for i := 0; i+188 <= len(data); i += 188 {
			c.packet(data[i:i+188], 188, 0)
		}
		c.finish(nil, len(data)%188 == 0)
		if len(r.ends) != 1 {
			t.Fatal("missing terminal callback")
		}
		for i, b := range r.boundaries {
			if b.PTS >= 1<<33 || b.DTS >= 1<<33 || i > 0 && b.Offset < r.boundaries[i-1].Offset {
				t.Fatal("invalid boundary")
			}
		}
	})
}

func FuzzExactTimeline(f *testing.F) {
	f.Add(timelineFixture(0, 45000, 2))
	f.Add([]byte("MPLS0200"))
	f.Add([]byte{0, 1, 0, 0, 0, 0, 0x10, 0, 1, 0, 0x10, 0x11, 4, 0x24, 0x61, 0x30, 0})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 256<<10 {
			return
		}
		_, _, _ = parsePlaylistTimeline(data, nil, nil, false)
		_, _ = parseSTCSequences(data)
		_ = validateTimelineProgram(data)
		// Exact parsing errors must not make the independent report parser unsafe.
		sf := NewStreamFile(&memFileInfo{name: "00001.M2TS"})
		cf := NewStreamClipFile(&memFileInfo{name: "00001.CLPI"})
		for _, capture := range []bool{false, true} {
			p := NewPlaylistFile(&memFileInfo{name: "FUZZ.MPLS", data: data}, settings.Settings{})
			p.CaptureTimeline = capture
			_ = p.Scan(map[string]*StreamFile{sf.Name: sf}, map[string]*StreamClipFile{cf.Name: cf})
		}
	})
}
