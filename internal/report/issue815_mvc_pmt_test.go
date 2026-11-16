// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package report

import (
	"encoding/binary"
	"hash/crc32"
	"math/bits"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/go-bdinfo/internal/bdrom"
	"github.com/autobrr/go-bdinfo/internal/fs"
	"github.com/autobrr/go-bdinfo/internal/settings"
	"github.com/autobrr/go-bdinfo/internal/stream"
)

// The reference TSStreamFile.CreateStream creates MVC from the selected SSIF's
// PMT even when the base clip's CLPI lists only AVC. No private media is used.
func TestSelectedSSIFReportIncludesPMTMVC(t *testing.T) {
	sectionCRC := func(section []byte) []byte {
		reversed := make([]byte, len(section))
		for i, b := range section {
			reversed[i] = bits.Reverse8(b)
		}
		return binary.BigEndian.AppendUint32(section, bits.Reverse32(^crc32.ChecksumIEEE(reversed)))
	}
	packet := func(pid uint16, payload []byte) []byte {
		p := make([]byte, 192)
		for i := range p {
			p[i] = 0xff
		}
		p[0], p[1], p[2], p[3] = 0, 0, 0, 0
		p[4], p[5], p[6], p[7] = 0x47, 0x40|byte(pid>>8), byte(pid), 0x10
		copy(p[8:], payload)
		return p
	}
	for _, name := range []string{"selected SSIF", "PMT without payload", "alternate signalled PID", "disabled SSIF", "base M2TS", "invalid CRC", "changing PMT", "ambiguous PAT", "PID without MVC type", "existing non-MVC 4114"} {
		t.Run(name, func(t *testing.T) {
			mvcPID, mvcType := uint16(4114), byte(0x20)
			if name == "alternate signalled PID" {
				mvcPID = 4115
			}
			if name == "PID without MVC type" {
				mvcType = 0x1b
			}
			patBody := []byte{0, 0xb0, 13, 0, 1, 0xc1, 0, 0, 0, 1, 0xf0, 0}
			if name == "ambiguous PAT" {
				patBody[2] += 4
				patBody = append(patBody, 0, 2, 0xf0, 1)
			}
			pat := sectionCRC(patBody)
			pmtBody := []byte{2, 0xb0, 23, 0, 1, 0xc1, 0, 0, 0xf0, 0x11, 0xf0, 0, 0x1b, 0xf0, 0x11, 0xf0, 0, mvcType, 0xe0 | byte(mvcPID>>8), byte(mvcPID), 0xf0, 0}
			pmt := sectionCRC(pmtBody)
			if name == "invalid CRC" {
				pmt[len(pmt)-1] ^= 1
			}
			data := packet(0, append([]byte{0}, pat...))
			data = append(data, packet(0x1000, append([]byte{0}, pmt...))...)
			if name != "PMT without payload" {
				data = append(data, packet(mvcPID, []byte{0, 0, 1, 0xe0, 0, 4, 0x80, 0, 0, 0})...)
			}
			if name == "changing PMT" {
				pmtBody[5] = 0xc3
				pmtBody[17] = 0x1b
				changed := packet(0x1000, append([]byte{0}, sectionCRC(pmtBody)...))
				changed[7] |= 1
				data = append(data, changed...)
			}
			dir := t.TempDir()
			path := filepath.Join(dir, "00001.ssif")
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			info, err := fs.NewDiskFileSystem().GetFileInfo(path)
			if err != nil {
				t.Fatal(err)
			}
			cfg := settings.Default("")
			cfg.FilterShortPlaylists = false
			if name == "disabled SSIF" {
				cfg.EnableSSIF = false
			}
			sf := bdrom.NewStreamFile(info)
			sf.Name = "00001.M2TS"
			sf.InterleavedFile = &bdrom.InterleavedFile{Name: "00001.SSIF", FileInfo: info, Size: int64(len(data))}
			if name == "existing non-MVC 4114" {
				sf.Streams[4114] = &stream.AudioStream{Stream: stream.Stream{PID: 4114, StreamType: stream.StreamTypeAC3Audio}}
			}
			if name == "base M2TS" {
				sf.InterleavedFile = nil
			}
			cf := &bdrom.StreamClipFile{Streams: map[uint16]stream.Info{}}
			avc := &stream.VideoStream{Stream: stream.Stream{PID: 4113, StreamType: stream.StreamTypeAVCVideo}}
			cf.Streams[4113] = avc
			clip := bdrom.NewStreamClip(sf, cf, cfg)
			clip.Length, clip.TimeOut, clip.RelativeLength = 60, 60, 1
			selected := &bdrom.PlaylistFile{Name: "00000.MPLS", Settings: cfg, StreamClips: []*bdrom.StreamClip{clip}, PlaylistStreams: map[uint16]stream.Info{4113: avc}}
			selected.Initialize()
			if err := sf.Scan([]*bdrom.PlaylistFile{selected}, true); err != nil {
				t.Fatal(err)
			}
			selected.Initialize()
			_, out, err := RenderReport("-", &bdrom.BDROM{Is3D: true}, []*bdrom.PlaylistFile{selected}, bdrom.ScanResult{}, cfg)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.Report, "MPEG-4 AVC Video") {
				t.Fatal("fixture lost AVC preservation baseline")
			}
			wantMVC := name == "selected SSIF" || name == "alternate signalled PID" || name == "PMT without payload"
			if strings.Contains(out.Report, "MPEG-4 MVC Video") != wantMVC {
				t.Fatalf("MVC report presence mismatch: want %v", wantMVC)
			}
			if name == "existing non-MVC 4114" && len(selected.AudioStreams) != 1 {
				t.Fatal("lost existing non-MVC stream inclusion")
			}
			if wantMVC {
				found := sf.Streams[mvcPID].Base()
				wantInitialized := name != "PMT without payload"
				if found.IsInitialized != wantInitialized || found.IsVBR != wantInitialized {
					t.Fatalf("MVC transfer flags initialized=%v VBR=%v want=%v", found.IsInitialized, found.IsVBR, wantInitialized)
				}
			}
		})
	}
}
