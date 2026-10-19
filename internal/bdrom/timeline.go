// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bdrom

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"

	"github.com/autobrr/go-bdinfo/pkg/bdinfo/video"
)

type TimelineItem struct {
	Index                int
	In45, Out45          uint32
	Offset45, Duration45 uint64
	Connection           uint8
	Angles               []TimelineAngle
}

type TimelineAngle struct {
	Index                    int
	Source                   video.Source
	STCID                    uint8
	DifferentAudio, Seamless bool
	Video                    []video.Mapping
	STC                      *video.STCSequence
	Err                      error
}

// These optional readers retain the original fields independently of the report's
// signed masking/float conversion. Structural layout was checked against libbluray
// mpls_parse.c and clpi_parse.c; unknown wrap/SSIF semantics stay unsupported.
type timelineReader struct {
	data []byte
	pos  int
	err  error
}

func (r *timelineReader) take(n int) []byte {
	if r.err != nil {
		return nil
	}
	if n < 0 || n > len(r.data)-r.pos {
		r.err = fmt.Errorf("truncated playlist/clip timing table at byte %d", r.pos)
		return nil
	}
	b := r.data[r.pos : r.pos+n]
	r.pos += n
	return b
}
func (r *timelineReader) u8() byte {
	b := r.take(1)
	if len(b) == 0 {
		return 0
	}
	return b[0]
}
func (r *timelineReader) u16() uint16 {
	b := r.take(2)
	if len(b) == 0 {
		return 0
	}
	return binary.BigEndian.Uint16(b)
}
func (r *timelineReader) u32() uint32 {
	b := r.take(4)
	if len(b) == 0 {
		return 0
	}
	return binary.BigEndian.Uint32(b)
}
func (r *timelineReader) section16() timelineReader {
	n := int(r.u16())
	return timelineReader{data: r.take(n), err: r.err}
}
func (r *timelineReader) section32() timelineReader {
	n := uint64(r.u32())
	if n > uint64(len(r.data)-r.pos) {
		r.err = fmt.Errorf("invalid timing section length %d", n)
		return timelineReader{err: r.err}
	}
	return timelineReader{data: r.take(int(n)), err: r.err}
}

// The report parser reads the first program only. Exact association can reuse
// that map only for one complete program covering the logical source from zero;
// later program sequences require packet-aware mapping that is not implemented.
func validateTimelineProgram(data []byte) error {
	r := timelineReader{data: data}
	r.take(1)
	programs := r.u8()
	if programs == 0 {
		return fmt.Errorf("%w: missing CLPI program context", video.ErrIncomplete)
	}
	if programs != 1 {
		return fmt.Errorf("%w: %d CLPI program sequences", video.ErrUnsupportedMapping, programs)
	}
	if r.u32() != 0 {
		return fmt.Errorf("%w: CLPI program starts after source packet zero", video.ErrUnsupportedMapping)
	}
	r.take(2) // program_map_PID
	streams := r.u8()
	r.take(1) // number_of_groups
	pids := make(map[uint16]bool, streams)
	for range streams {
		pid := r.u16()
		if pids[pid] {
			return fmt.Errorf("%w: duplicate CLPI PID %#x", video.ErrUnsupportedMapping, pid)
		}
		pids[pid] = true
		n := int(r.u8())
		if n == 0 && r.err == nil {
			return fmt.Errorf("%w: empty CLPI stream coding information", video.ErrIncomplete)
		}
		coding := r.take(n)
		if r.err != nil {
			break
		}
		// Legacy CLPI parsing reads video format/rate and aspect outside a short
		// declared region. Exact associations may reuse only fields inside it.
		switch video.Codec(coding[0]) {
		case video.MPEG1, video.MPEG2, video.AVC, video.MVC, video.HEVC, video.VC1:
			if len(coding) < 3 {
				return fmt.Errorf("%w: short CLPI video coding information for PID %#x", video.ErrIncomplete, pid)
			}
		}
	}
	if r.err != nil {
		return fmt.Errorf("%w: CLPI program context: %v", video.ErrIncomplete, r.err)
	}
	return nil
}

func parsePlaylistTimeline(data []byte, streams map[string]*StreamFile, clips map[string]*StreamClipFile, ssif bool) ([]TimelineItem, uint64, error) {
	if len(data) < 20 {
		return nil, 0, fmt.Errorf("short MPLS header")
	}
	offset := uint64(binary.BigEndian.Uint32(data[8:12]))
	if offset > uint64(len(data)-4) {
		return nil, 0, fmt.Errorf("invalid MPLS playlist offset")
	}
	root := timelineReader{data: data, pos: int(offset)}
	list := root.section32()
	list.take(2)
	count := int(list.u16())
	list.take(2)
	if list.err != nil || count > (len(list.data)-list.pos)/34 {
		return nil, 0, fmt.Errorf("invalid MPLS play item count")
	}
	items := make([]TimelineItem, 0, count)
	var total uint64
	for i := range count {
		r := list.section16()
		clipID := string(r.take(5))
		codecID := string(r.take(4))
		flags := r.u16()
		stc := r.u8()
		in, out := r.u32(), r.u32()
		r.take(12)
		if r.err != nil {
			return nil, 0, r.err
		}
		if codecID != "M2TS" {
			return nil, 0, fmt.Errorf("%w: clip format %q", video.ErrUnsupportedMapping, codecID)
		}
		if out < in {
			return nil, 0, fmt.Errorf("%w: wrapped/invalid MPLS range %d..%d", video.ErrUnsupportedMapping, in, out)
		}
		duration := uint64(out) - uint64(in)
		item := TimelineItem{Index: i, In45: in, Out45: out, Offset45: total, Duration45: duration, Connection: uint8(flags & 15)}
		main := timelineAngle(clipID, stc, 0, streams, clips, ssif, in, out)
		item.Angles = append(item.Angles, main)
		if flags&0x10 != 0 {
			angles := int(r.u8())
			angleFlags := r.u8()
			if angles < 1 {
				return nil, 0, fmt.Errorf("invalid MPLS angle count")
			}
			item.Angles[0].DifferentAudio = angleFlags&2 != 0
			item.Angles[0].Seamless = angleFlags&1 != 0
			for j := 1; j < angles; j++ {
				name := string(r.take(5))
				kind := string(r.take(4))
				id := r.u8()
				if r.err != nil {
					return nil, 0, r.err
				}
				if kind != "M2TS" {
					return nil, 0, fmt.Errorf("%w: angle format %q", video.ErrUnsupportedMapping, kind)
				}
				angle := timelineAngle(name, id, j, streams, clips, ssif, in, out)
				angle.DifferentAudio = angleFlags&2 != 0
				angle.Seamless = angleFlags&1 != 0
				item.Angles = append(item.Angles, angle)
			}
		}
		stn := r.section16()
		mappings := parseTimelineSTN(&stn)
		if r.err != nil {
			return nil, 0, r.err
		}
		if stn.err != nil {
			return nil, 0, stn.err
		}
		for j := range item.Angles {
			a := &item.Angles[j]
			a.Video = append([]video.Mapping(nil), mappings...)
			if cf := clips[strings.TrimSuffix(a.Source.LogicalClip, ".M2TS")+".CLPI"]; cf != nil {
				for _, m := range a.Video {
					if m.EntryType != 1 {
						continue
					}
					if s := cf.Streams[m.PID]; s == nil {
						a.Err = errors.Join(a.Err, fmt.Errorf("%w: MPLS PID %#x absent from CLPI program context", video.ErrIncomplete, m.PID))
					} else if !s.Base().IsVideoStream() || video.Codec(s.Base().StreamType) != m.Codec {
						a.Err = errors.Join(a.Err, fmt.Errorf("%w: PID %#x MPLS/CLPI codec conflict", video.ErrUnsupportedMapping, m.PID))
					}
				}
			}
		}
		items = append(items, item)
		total += duration
	}
	if list.err != nil {
		return nil, 0, list.err
	}
	return items, total, nil
}

func timelineAngle(name string, id uint8, index int, streams map[string]*StreamFile, clips map[string]*StreamClipFile, ssif bool, in, out uint32) TimelineAngle {
	name = strings.ToUpper(name + ".M2TS")
	a := TimelineAngle{Index: index, STCID: id, Source: video.Source{Path: "BDMV/STREAM/" + name, Kind: "m2ts", LogicalClip: name}}
	sf := streams[name]
	if sf == nil {
		a.Err = fmt.Errorf("%w: missing source %s", video.ErrIncomplete, name)
	} else {
		a.Source = streamSource(sf, ssif)
	}
	cf := clips[strings.TrimSuffix(name, ".M2TS")+".CLPI"]
	if cf == nil {
		a.Err = errors.Join(a.Err, fmt.Errorf("%w: missing CLPI for %s", video.ErrIncomplete, name))
		return a
	}
	if cf.SequenceErr != nil {
		a.Err = errors.Join(a.Err, cf.SequenceErr)
	}
	for _, seq := range cf.STCSequences {
		if seq.ID != id {
			continue
		}
		if a.STC != nil {
			a.Err = errors.Join(a.Err, fmt.Errorf("%w: ambiguous STC ID %d", video.ErrUnsupportedMapping, id))
			return a
		}
		sequence := seq
		a.STC = &sequence
	}
	if a.STC == nil {
		a.Err = errors.Join(a.Err, fmt.Errorf("%w: missing STC ID %d", video.ErrUnsupportedMapping, id))
	} else if in < a.STC.PresentationStart45 || out > a.STC.PresentationEnd45 {
		a.Err = errors.Join(a.Err, fmt.Errorf("%w: trim outside STC presentation range", video.ErrUnsupportedMapping))
	}
	if a.Source.Kind == "ssif" {
		a.Err = errors.Join(a.Err, fmt.Errorf("%w: SSIF and CLPI packet domains require mapping", video.ErrUnsupportedMapping))
	} else if a.STC != nil && sf != nil && sf.FileInfo != nil {
		// CLPI source packet numbers address 192-byte logical M2TS packets. Use
		// FileInfo's extent without opening another stream in metadata discovery.
		length := sf.FileInfo.Length()
		if length <= 0 || length%192 != 0 {
			a.Err = errors.Join(a.Err, fmt.Errorf("%w: logical M2TS extent is not whole packets", video.ErrIncomplete))
		} else {
			packets := uint64(length / 192)
			if uint64(a.STC.StartPacket) >= packets || a.STC.HasEndPacket && a.STC.EndPacket > packets {
				a.Err = errors.Join(a.Err, fmt.Errorf("%w: STC packet range exceeds logical M2TS extent", video.ErrIncomplete))
			} else if !a.STC.HasEndPacket {
				a.STC.EndPacket = packets
				a.STC.HasEndPacket = true
			}
		}
	}
	return a
}

func parseTimelineSTN(r *timelineReader) []video.Mapping {
	r.take(2)
	var counts [8]int
	for i := range counts {
		counts[i] = int(r.u8())
	}
	r.take(4)
	var out []video.Mapping
	// PiP PG entries share the presentation-graphics table, rather than following
	// secondary video. Secondary reference arrays have count-dependent padding.
	counts[2] += counts[6]
	counts[6] = 0
	for group, count := range counts {
		for range count {
			entry := timelineReader{data: r.take(int(r.u8())), err: r.err}
			kind := entry.u8()
			m := video.Mapping{EntryType: kind, Role: video.Unknown}
			switch kind {
			case 1:
				m.PID = entry.u16()
			case 2:
				m.Subpath = entry.u8()
				m.Subclip = entry.u8()
				m.HasSubclip = true
				m.PID = entry.u16()
			case 3, 4:
				m.Subpath = entry.u8()
				m.PID = entry.u16()
			}
			attr := timelineReader{data: r.take(int(r.u8())), err: r.err}
			m.Codec = video.Codec(attr.u8())
			// MPLS classic video carries format/rate; HEVC also carries dynamic
			// range/color and flags (libbluray mpls_parse.c). No aspect field here.
			switch m.Codec {
			case video.MPEG1, video.MPEG2, video.AVC, video.VC1:
				attr.take(1)
			case video.HEVC:
				attr.take(3)
			}
			if entry.err != nil {
				r.err = entry.err
			}
			if attr.err != nil {
				r.err = fmt.Errorf("%w: MPLS stream attributes: %v", video.ErrIncomplete, attr.err)
			}
			if r.err != nil {
				return nil
			}
			if group == 0 || group == 5 || group == 7 {
				if group == 0 && kind == 1 {
					m.Role = video.Primary
				}
				if group == 5 && kind >= 1 && kind <= 4 {
					m.Role = video.Secondary
				}
				out = append(out, m)
			}
			refs := 0
			if group == 4 {
				refs = 1
			}
			if group == 5 {
				refs = 2
			}
			for range refs {
				n := int(r.u8())
				r.take(1)
				r.take(n + (n & 1))
			}
			if r.err != nil {
				return nil
			}
		}
	}
	return out
}

func parseSTCSequences(data []byte) ([]video.STCSequence, error) {
	if len(data) < 60 {
		return nil, fmt.Errorf("%w: short CLPI sequence context", video.ErrIncomplete)
	}
	offset := uint64(binary.BigEndian.Uint32(data[8:12]))
	if offset < 40 || offset > uint64(len(data)-4) {
		return nil, fmt.Errorf("%w: invalid CLPI sequence offset", video.ErrUnsupportedMapping)
	}
	// ClipInfo precedes SequenceInfo. Its declared body must contain the source
	// packet count before that count can bound an exact clock association.
	clipRoot := timelineReader{data: data[:int(offset)], pos: 40}
	clip := clipRoot.section32()
	clip.take(12)
	packetCount := uint64(clip.u32())
	if clip.err != nil {
		return nil, fmt.Errorf("%w: invalid CLPI ClipInfo: %v", video.ErrIncomplete, clip.err)
	}
	root := timelineReader{data: data, pos: int(offset)}
	r := root.section32()
	r.take(1)
	count := int(r.u8())
	var out []video.STCSequence
	for atcIndex := range count {
		atcStart := r.u32()
		n := int(r.u8())
		id := int(r.u8())
		if id+n > 256 {
			return nil, fmt.Errorf("invalid CLPI STC IDs")
		}
		for j := range n {
			seq := video.STCSequence{ATCIndex: atcIndex, ATCStartPacket: atcStart, ID: uint8(id + j), PCRPID: r.u16(), StartPacket: r.u32(), PresentationStart45: r.u32(), PresentationEnd45: r.u32()}
			if r.err != nil {
				return nil, r.err
			}
			if seq.StartPacket < atcStart || seq.PresentationEnd45 < seq.PresentationStart45 || packetCount > 0 && uint64(seq.StartPacket) >= packetCount {
				return nil, fmt.Errorf("%w: contradictory CLPI STC range", video.ErrUnsupportedMapping)
			}
			if len(out) > 0 {
				previous := &out[len(out)-1]
				if seq.StartPacket <= previous.StartPacket {
					return nil, fmt.Errorf("%w: unordered CLPI STC packet ranges", video.ErrUnsupportedMapping)
				}
				previous.EndPacket = uint64(seq.StartPacket)
				previous.HasEndPacket = true
			}
			out = append(out, seq)
		}
	}
	if r.err != nil {
		return nil, r.err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: no CLPI STC sequences", video.ErrUnsupportedMapping)
	}
	if packetCount > 0 {
		last := &out[len(out)-1]
		last.EndPacket = packetCount
		last.HasEndPacket = true
	}
	return out, nil
}
