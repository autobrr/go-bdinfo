// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bdrom

import (
	"bytes"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/autobrr/go-bdinfo/internal/fs"
	"github.com/autobrr/go-bdinfo/internal/stream"
)

type memFileInfo struct {
	name string
	data []byte
}

func (m *memFileInfo) Name() string       { return filepath.Base(m.name) }
func (m *memFileInfo) FullName() string   { return m.name }
func (m *memFileInfo) Length() int64      { return int64(len(m.data)) }
func (m *memFileInfo) Extension() string  { return filepath.Ext(m.name) }
func (m *memFileInfo) IsDirectory() bool  { return false }
func (m *memFileInfo) ModTime() time.Time { return time.Time{} }
func (m *memFileInfo) OpenRead() (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(m.data)), nil
}

var _ fs.FileInfo = (*memFileInfo)(nil)

func encodePTS(prefix byte, pts uint64) [5]byte {
	// Standard PES timestamp packing with marker bits set.
	return [5]byte{
		prefix | byte((pts>>29)&0x0E) | 0x01,
		byte(pts >> 22),
		byte((pts>>14)&0xFE) | 0x01,
		byte(pts >> 7),
		byte((pts<<1)&0xFE) | 0x01,
	}
}

func tsPacket188(pid uint16, payloadStart bool, payload []byte) [188]byte {
	var pkt [188]byte
	pkt[0] = 0x47
	pkt[1] = byte((pid >> 8) & 0x1F)
	if payloadStart {
		pkt[1] |= 0x40
	}
	pkt[2] = byte(pid & 0xFF)
	pkt[3] = 0x10 // payload only

	// Payload is 184 bytes.
	copy(pkt[4:], payload)
	return pkt
}

func TestStreamFileScanLengthSingleTimestampIsZero(t *testing.T) {
	const pid = 0x1011

	// Build a minimal PES header with PTS+DTS (one timestamp total).
	pts := uint64(90000)
	dts := uint64(90000)
	pts5 := encodePTS(0x30, pts)
	dts5 := encodePTS(0x10, dts)

	pes := make([]byte, 19)
	pes[0], pes[1], pes[2] = 0x00, 0x00, 0x01
	pes[3] = 0xE0 // video stream_id
	pes[4], pes[5] = 0x00, 0x00
	pes[6] = 0x80
	pes[7] = 0xC0
	pes[8] = 0x0A
	copy(pes[9:14], pts5[:])
	copy(pes[14:19], dts5[:])

	payload1 := make([]byte, 184)
	copy(payload1, pes)
	// Include an SPS-like prefix that would initialize the AVC codec if the analyzer ran.
	copy(payload1[len(pes):], []byte{0x00, 0x00, 0x01, 0x67, 0x64, 0x00, 0x28})
	payload2 := make([]byte, 184) // continuation; no new PES start

	p1 := tsPacket188(pid, true, payload1)
	p2 := tsPacket188(pid, false, payload2)

	data := append(p1[:], p2[:]...)
	fi := &memFileInfo{name: "TEST.M2TS", data: data}

	s := NewStreamFile(fi)
	vs := &stream.VideoStream{Stream: stream.Stream{PID: pid, StreamType: stream.StreamTypeAVCVideo}}
	s.Streams[pid] = vs

	// Ensure Scan resets Length and does not seed it from playlist clips.
	s.Length = 123.0
	if err := s.Scan(nil, false); err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	if s.Length != 0 {
		t.Fatalf("expected length 0 for single timestamp, got %v", s.Length)
	}
	if vs.IsVBR || vs.IsInitialized || vs.EncodingProfile != "" {
		t.Fatalf("expected codec uninitialized for single unterminated transfer: IsVBR=%v IsInitialized=%v EncodingProfile=%q", vs.IsVBR, vs.IsInitialized, vs.EncodingProfile)
	}
}

func TestStreamFileScanMVCAccounting(t *testing.T) {
	const pid = 0x1012
	for _, tc := range []struct {
		name          string
		packetSize    int
		payloadSize   int
		pesLength     uint16
		continuations int
		splitHeader   bool
		wantPackets   uint64
		wantBytes     uint64
	}{
		{"TS", 188, 184, 0, 1, false, 6, 1047},
		{"M2TS", 192, 184, 0, 1, false, 6, 1047},
		{"adaptation", 192, 182, 0, 1, false, 6, 1035},
		{"bounded PES", 192, 184, 20, 1, false, 6, 21},
		{"bounded PES continuation", 192, 184, 400, 2, false, 9, 1161},
		{"split PES header", 192, 184, 0, 1, true, 9, 1074},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var data []byte
			appendPacket := func(start bool, payload []byte, size int) {
				p := tsPacket188(pid, start, nil)
				if size < 184 {
					p[3], p[4] = 0x30, byte(183-size)
					for i := 6; i < 188-size; i++ {
						p[i] = 0xff // adaptation stuffing after the zero flags byte
					}
				}
				copy(p[188-size:], payload)
				if tc.packetSize == 192 {
					data = append(data, 0, 0, 0, 0)
				}
				data = append(data, p[:]...)
			}
			for _, timestamp := range []uint64{90000, 180000, 270000} {
				pes := []byte{0, 0, 1, 0xfd, 0, 0, 0x80, 0xc0, 10}
				pes[4], pes[5] = byte(tc.pesLength>>8), byte(tc.pesLength)
				pts := encodePTS(0x30, timestamp)
				dts := encodePTS(0x10, timestamp)
				pes = append(pes, pts[:]...)
				pes = append(pes, dts[:]...)
				if tc.splitHeader {
					appendPacket(true, pes[:9], 9)
					appendPacket(false, pes[9:], 184)
				} else {
					appendPacket(true, pes, tc.payloadSize)
				}
				for range tc.continuations {
					appendPacket(false, nil, tc.payloadSize)
				}
			}
			s := NewStreamFile(&memFileInfo{name: "MVC.M2TS", data: data})
			video := &stream.VideoStream{Stream: stream.Stream{PID: pid, StreamType: stream.StreamTypeMVCVideo}}
			s.Streams[pid] = video
			if err := s.Scan([]*PlaylistFile{}, false); err != nil {
				t.Fatal(err)
			}
			if !video.IsInitialized || !video.IsVBR || s.Length != 1 {
				t.Fatalf("MVC transfer state: initialized=%v VBR=%v duration=%v", video.IsInitialized, video.IsVBR, s.Length)
			}
			// BDInfo's file duration starts at the second DTS, but packet time includes both intervals.
			if video.PacketCount != tc.wantPackets || video.PayloadBytes != tc.wantBytes || video.PacketSeconds != 2 {
				t.Fatalf("MVC accounting: packets=%d bytes=%d seconds=%v", video.PacketCount, video.PayloadBytes, video.PacketSeconds)
			}
			var packets, payload uint64
			for _, diagnostic := range s.StreamDiagnostics[pid] {
				packets += diagnostic.Packets
				payload += diagnostic.Bytes
				if diagnostic.Tag != "" {
					t.Fatalf("MVC has no decoded frame tag: %q", diagnostic.Tag)
				}
			}
			if packets != video.PacketCount || payload != video.PayloadBytes {
				t.Fatalf("MVC diagnostics: packets=%d bytes=%d", packets, payload)
			}
		})
	}
}
