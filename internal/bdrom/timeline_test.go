// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bdrom

import (
	"encoding/binary"
	"fmt"
	"strings"
	"testing"

	"github.com/autobrr/go-bdinfo/internal/settings"
	"github.com/autobrr/go-bdinfo/internal/stream"
	"github.com/autobrr/go-bdinfo/pkg/bdinfo/video"
)

func timelineFixture(in, out uint32, repeat int) []byte {
	data := make([]byte, 64)
	copy(data, "MPLS0200")
	binary.BigEndian.PutUint32(data[8:], 64)
	list := make([]byte, 6)
	binary.BigEndian.PutUint16(list[2:], uint16(repeat))
	for range repeat {
		item := make([]byte, 32)
		copy(item, "00001M2TS")
		item[10] = 5
		item[11] = 7
		binary.BigEndian.PutUint32(item[12:], in)
		binary.BigEndian.PutUint32(item[16:], out)
		stn := make([]byte, 14)
		stn[2] = 1
		stn = append(stn, 3, 1, 0x10, 0x11, 4, 0x24, 0x61, 0, 0)
		item = append(item, byte(len(stn)>>8), byte(len(stn)))
		item = append(item, stn...)
		list = append(list, byte(len(item)>>8), byte(len(item)))
		list = append(list, item...)
	}
	data = append(data, byte(len(list)>>24), byte(len(list)>>16), byte(len(list)>>8), byte(len(list)))
	data = append(data, list...)
	return data
}
func TestExactPlaylistTimelineRepeatedAndHighBit(t *testing.T) {
	data := timelineFixture(0x80000001, 0x80000010, 3)
	sf := NewStreamFile(&memFileInfo{name: "00001.M2TS", data: make([]byte, 192)})
	cf := NewStreamClipFile(&memFileInfo{name: "00001.CLPI"})
	cf.STCSequences = []video.STCSequence{{ID: 7, StartPacket: 0, PresentationStart45: 0x80000000, PresentationEnd45: 0x80000020}}
	cf.Streams[0x1011] = &stream.VideoStream{Stream: stream.Stream{PID: 0x1011, StreamType: stream.StreamTypeHEVCVideo}}
	items, duration, err := parsePlaylistTimeline(data, map[string]*StreamFile{sf.Name: sf}, map[string]*StreamClipFile{cf.Name: cf}, false)
	if err != nil {
		t.Fatal(err)
	}
	if duration != 45 || len(items) != 3 {
		t.Fatalf("duration %d items %+v", duration, items)
	}
	for i, item := range items {
		if item.In45 != 0x80000001 || item.Offset45 != uint64(i*15) || item.Connection != 5 || item.Angles[0].STCID != 7 || item.Angles[0].Video[0].Role != video.Primary || item.Angles[0].Err != nil {
			t.Fatalf("item %+v", item)
		}
	}
}
func TestTimelineTruncatedAndInvalidRange(t *testing.T) {
	for _, data := range [][]byte{timelineFixture(100, 99, 1), timelineFixture(1, 2, 1)[:90]} {
		if _, _, err := parsePlaylistTimeline(data, nil, nil, false); err == nil {
			t.Fatal("accepted malformed timeline")
		}
	}
}

func TestLegacyPlaylistTruncationRemainsSafe(t *testing.T) {
	data := timelineFixture(1, 45001, 1)
	sf := NewStreamFile(&memFileInfo{name: "00001.M2TS"})
	cf := NewStreamClipFile(&memFileInfo{name: "00001.CLPI"})
	for n := 8; n < len(data); n++ {
		for _, capture := range []bool{false, true} {
			p := NewPlaylistFile(&memFileInfo{name: "00000.MPLS", data: data[:n]}, settings.Settings{})
			p.CaptureTimeline = capture
			_ = p.Scan(map[string]*StreamFile{sf.Name: sf}, map[string]*StreamClipFile{cf.Name: cf})
		}
	}
}

func TestLegacyPlaylistTruncatedStreamEntries(t *testing.T) {
	// Exercise every caller, including the ignored PIP stream table, in the last item.
	for table := range 7 {
		for _, attributes := range [][]byte{
			{4, 0x24, 0x61, 0x30, 0},       // HEVC
			{5, 0x81, 0x31, 'e', 'n', 'g'}, // audio
			{4, 0x90, 'e', 'n', 'g'},       // graphics
			{5, 0x92, 1, 'e', 'n', 'g'},    // text
			{1, 0xff},                      // unknown coding type is still skippable
		} {
			entry := append([]byte{3, 1, 0x10, 0x11}, attributes...)
			for n := 0; n <= len(entry); n++ {
				t.Run(fmt.Sprintf("table%d/type%x/bytes%d", table, attributes[1], n), func(t *testing.T) {
					data := timelineFixture(1, 45001, 1)[:124]
					clear(data[112:119])
					data[112+table] = 1
					data = append(data, entry[:n]...)
					p := NewPlaylistFile(&memFileInfo{name: "00000.MPLS", data: data}, settings.Settings{})
					sf := NewStreamFile(&memFileInfo{name: "00001.M2TS"})
					cf := NewStreamClipFile(&memFileInfo{name: "00001.CLPI"})
					err := p.Scan(map[string]*StreamFile{sf.Name: sf}, map[string]*StreamClipFile{cf.Name: cf})
					if n < len(entry) {
						if err == nil || !strings.Contains(err.Error(), "truncated") {
							t.Fatalf("cut entry: got %v, want truncation error", err)
						}
					} else if err != nil {
						t.Fatalf("complete entry: %v", err)
					}
				})
			}
		}
	}
}
func TestSTCSequenceBoundsAndClockResets(t *testing.T) {
	data := make([]byte, 60)
	copy(data, "HDMV0200")
	binary.BigEndian.PutUint32(data[8:], 60)
	binary.BigEndian.PutUint32(data[40:], 16)
	binary.BigEndian.PutUint32(data[56:], 100)
	seq := []byte{0, 1, 0, 0, 0, 0, 2, 7}
	for i := range 2 {
		entry := make([]byte, 14)
		binary.BigEndian.PutUint16(entry, 0x1001)
		binary.BigEndian.PutUint32(entry[2:], uint32(i*50))
		binary.BigEndian.PutUint32(entry[6:], 0)
		binary.BigEndian.PutUint32(entry[10:], 90000)
		seq = append(seq, entry...)
	}
	data = append(data, 0, 0, 0, byte(len(seq)))
	data = append(data, seq...)
	got, err := parseSTCSequences(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != 7 || got[1].ID != 8 || got[0].EndPacket != 50 || got[1].EndPacket != 100 || !got[1].HasEndPacket {
		t.Fatalf("sequences %+v", got)
	}
	if _, err := parseSTCSequences(data[:len(data)-1]); err == nil {
		t.Fatal("accepted truncated sequence")
	}
}

func TestTimelineAnglesDoNotDuplicateDuration(t *testing.T) {
	data := timelineFixture(1, 45001, 1)
	body := append([]byte(nil), data[76:]...)
	body[10] |= 0x10
	multi := append(append(append([]byte(nil), body[:32]...), 2, 3), []byte("00002M2TS")...)
	multi = append(multi, 8)
	multi = append(multi, body[32:]...)
	data = data[:74]
	data = append(data, byte(len(multi)>>8), byte(len(multi)))
	data = append(data, multi...)
	binary.BigEndian.PutUint32(data[64:], uint32(len(data)-68))
	streams := map[string]*StreamFile{}
	clips := map[string]*StreamClipFile{}
	for i, name := range []string{"00001", "00002"} {
		sf := NewStreamFile(&memFileInfo{name: name + ".M2TS", data: make([]byte, 192)})
		cf := NewStreamClipFile(&memFileInfo{name: name + ".CLPI"})
		cf.STCSequences = []video.STCSequence{{ID: uint8(7 + i), PresentationEnd45: 90000}}
		cf.Streams[0x1011] = &stream.VideoStream{Stream: stream.Stream{PID: 0x1011, StreamType: stream.StreamTypeHEVCVideo}}
		streams[sf.Name] = sf
		clips[cf.Name] = cf
	}
	items, duration, err := parsePlaylistTimeline(data, streams, clips, false)
	if err != nil {
		t.Fatal(err)
	}
	if duration != 45000 || len(items) != 1 || len(items[0].Angles) != 2 || items[0].Angles[1].STCID != 8 || items[0].Angles[1].Index != 1 || !items[0].Angles[1].DifferentAudio || !items[0].Angles[1].Seamless || items[0].Angles[1].Err != nil {
		t.Fatalf("items %+v duration %d", items, duration)
	}
	streams["00001.M2TS"].InterleavedFile = &InterleavedFile{Name: "00001.SSIF", FileInfo: &memFileInfo{name: "BDMV/STREAM/SSIF/00001.ssif"}}
	items, _, err = parsePlaylistTimeline(data, streams, clips, true)
	if err != nil {
		t.Fatal(err)
	}
	if items[0].Angles[0].Source.Kind != "ssif" || items[0].Angles[0].Err == nil {
		t.Fatalf("SSIF association %+v", items)
	}
}

func TestTimelinePerItemMappingsAndReferenceArrays(t *testing.T) {
	data := make([]byte, 14)
	data[2] = 1
	data[4] = 1
	data[6] = 1
	data[7] = 1
	data[9] = 1
	data = append(data, 3, 1, 0x10, 0x11, 4, 0x24, 0x61, 0, 0)                // primary video
	data = append(data, 3, 1, 0x12, 0x00, 4, 0x90, 'e', 'n', 'g')             // primary PG
	data = append(data, 4, 3, 9, 0x11, 0x00, 6, 0xa1, 0x31, 'e', 'n', 'g', 0) // secondary audio
	data = append(data, 3, 0, 0, 1, 2, 0)                                     // three references, padding
	data = append(data, 5, 2, 4, 6, 0x10, 0x12, 4, 0x24, 0x61, 0, 0)          // secondary video with subclip
	data = append(data, 1, 0, 0, 0, 0, 0)                                     // one secondary-audio ref, zero PiP refs
	data = append(data, 4, 4, 6, 0x10, 0x13, 4, 0x24, 0x61, 0, 0)             // DV type 4 has no subclip
	r := timelineReader{data: data}
	got := parseTimelineSTN(&r)
	if r.err != nil {
		t.Fatal(r.err)
	}
	if len(got) != 3 || got[0].Role != video.Primary || got[1].Role != video.Secondary || got[1].Subpath != 4 || got[1].Subclip != 6 || !got[1].HasSubclip || got[2].PID != 0x1013 || got[2].Subpath != 6 || got[2].HasSubclip || got[2].Role != video.Unknown {
		t.Fatalf("mappings %+v", got)
	}
}

func TestTimelineSecondaryRoleRequiresSupportedEntry(t *testing.T) {
	for _, kind := range []byte{0, 1, 2, 3, 4, 255} {
		data := make([]byte, 14)
		data[7] = 1 // one secondary video entry
		entry := []byte{kind}
		switch kind {
		case 1:
			entry = append(entry, 0x10, 0x11)
		case 2:
			entry = append(entry, 4, 6, 0x10, 0x11)
		case 3, 4:
			entry = append(entry, 4, 0x10, 0x11)
		}
		data = append(data, byte(len(entry)))
		data = append(data, entry...)
		data = append(data, 4, 0x24, 0x61, 0x30, 0, 0, 0, 0, 0)
		r := timelineReader{data: data}
		mappings := parseTimelineSTN(&r)
		want := video.Unknown
		if kind >= 1 && kind <= 4 {
			want = video.Secondary
		}
		if r.err != nil || len(mappings) != 1 || mappings[0].Role != want || mappings[0].EntryType != kind {
			t.Fatalf("entry type %d: mappings %+v error %v", kind, mappings, r.err)
		}
	}
}
