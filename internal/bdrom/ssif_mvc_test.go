// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bdrom

import (
	"fmt"
	"testing"

	"github.com/autobrr/go-bdinfo/internal/settings"
	"github.com/autobrr/go-bdinfo/internal/stream"
)

func TestSelectedSSIFMVCPlaylistAccounting(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		clips, transfers       int
		timeIn, timeOut        float64
		wantBytes, wantPackets uint64
		wantSeconds            float64
		declaredMVC            bool
	}{
		{"multiple clips", 2, 2, 0, 3, 32, 8, 2, false},
		{"partial clip", 1, 3, 1.5, 2.5, 8, 3, 1, false},
		{"CLPI MVC multiple clips", 2, 2, 0, 3, 32, 8, 2, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := settings.Default("")
			p := &PlaylistFile{Settings: cfg}
			for i := range tc.clips {
				pat := psiWithCRC([]byte{0, 0xb0, 13, 0, 1, 0xc1, 0, 0, 0, 1, 0xf0, 0})
				pmt := psiWithCRC([]byte{2, 0xb0, 23, 0, 1, 0xc1, 0, 0, 0xf0, 0x11, 0xf0, 0, 0x1b, 0xf0, 0x11, 0xf0, 0, 0x20, 0xf0, 0x12, 0xf0, 0})
				var data []byte
				appendPacket := func(packet []byte) {
					data = append(data, 0, 0, 0, 0)
					data = append(data, packet...)
				}
				appendPacket(collectionPacket(0, 0, true, append([]byte{0}, pat...)))
				appendPacket(collectionPacket(0x1000, 0, true, append([]byte{0}, pmt...)))
				for transfer := range tc.transfers {
					timestamp := uint64(transfer+1) * 90000
					appendPacket(collectionPacket(0x1012, byte(2*transfer), true, collectionPES(make([]byte, 8), timestamp, timestamp, true)))
					appendPacket(collectionPacket(0x1012, byte(2*transfer+1), false, nil))
				}
				fi := &memFileInfo{name: fmt.Sprintf("%05d.M2TS", i+1), data: data}
				sf := NewStreamFile(fi)
				sf.InterleavedFile = &InterleavedFile{FileInfo: fi}
				cf := &StreamClipFile{Streams: map[uint16]stream.Info{0x1011: &stream.VideoStream{Stream: stream.Stream{PID: 0x1011, StreamType: stream.StreamTypeAVCVideo}}}}
				if tc.declaredMVC {
					cf.Streams[0x1012] = &stream.VideoStream{Stream: stream.Stream{PID: 0x1012, StreamType: stream.StreamTypeMVCVideo, IsVBR: true}}
				}
				clip := NewStreamClip(sf, cf, cfg)
				clip.TimeIn, clip.TimeOut = tc.timeIn, tc.timeOut
				clip.Length, clip.RelativeLength = tc.timeOut-tc.timeIn, 1
				p.StreamClips = append(p.StreamClips, clip)
			}
			p.Initialize()
			t.Run("scan", func(t *testing.T) {
				for _, clip := range p.StreamClips {
					t.Run(clip.Name, func(t *testing.T) {
						t.Parallel()
						if err := clip.StreamFile.Scan([]*PlaylistFile{p}, false); err != nil {
							t.Fatal(err)
						}
						source := clip.StreamFile.Streams[0x1012].Base()
						if source.PayloadBytes != uint64(tc.transfers*8) || source.PacketCount != uint64(tc.transfers*2) {
							t.Fatalf("source accounting: %+v", source)
						}
					})
				}
			})
			// Reinitialization must retain selected counters without accumulating again.
			for range 2 {
				p.Initialize()
				mvc := p.Streams[0x1012].Base()
				if mvc.PayloadBytes != tc.wantBytes || mvc.PacketCount != tc.wantPackets || mvc.PacketSeconds != tc.wantSeconds {
					t.Fatalf("playlist MVC accounting: bytes=%d packets=%d seconds=%v; want %d/%d/%v", mvc.PayloadBytes, mvc.PacketCount, mvc.PacketSeconds, tc.wantBytes, tc.wantPackets, tc.wantSeconds)
				}
				if (!tc.declaredMVC && !mvc.IsInitialized) || !mvc.IsVBR || mvc.BitRate != 43 {
					t.Fatalf("playlist MVC state: initialized=%v VBR=%v bitrate=%d", mvc.IsInitialized, mvc.IsVBR, mvc.BitRate)
				}
				if avc := p.Streams[0x1011].Base(); avc.StreamType != stream.StreamTypeAVCVideo || avc.PayloadBytes != 0 {
					t.Fatalf("AVC preservation: %+v", avc)
				}
			}
		})
	}
}

func TestStreamFileScanMVCCompletion(t *testing.T) {
	for _, tc := range []struct {
		name            string
		pesLengths      []uint16
		wantInitialized bool
	}{
		{"two truncated bounded", []uint16{400, 400}, false},
		{"completed before truncated", []uint16{14, 400}, true},
		{"one unbounded", []uint16{0}, false},
		{"terminated unbounded", []uint16{0, 400}, true},
		{"header-only bounded", []uint16{13}, true},
	} {
		for _, full := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/full=%v", tc.name, full), func(t *testing.T) {
				var data []byte
				for i, length := range tc.pesLengths {
					pes := collectionPES([]byte{1}, uint64(i+1)*90000, uint64(i+1)*90000, true)
					pes[4], pes[5] = byte(length>>8), byte(length)
					data = append(data, collectionPacket(0x1012, byte(i), true, pes)...)
				}
				sf := NewStreamFile(&memFileInfo{name: "MVC.M2TS", data: data})
				mvc := &stream.VideoStream{Stream: stream.Stream{PID: 0x1012, StreamType: stream.StreamTypeMVCVideo}}
				sf.Streams[0x1012] = mvc
				if err := sf.Scan([]*PlaylistFile{}, full); err != nil {
					t.Fatal(err)
				}
				if mvc.IsInitialized != tc.wantInitialized || mvc.IsVBR != tc.wantInitialized {
					t.Fatalf("MVC initialized=%v VBR=%v; want %v", mvc.IsInitialized, mvc.IsVBR, tc.wantInitialized)
				}
			})
		}
	}
}
