// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bdrom

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"math/bits"
	"sort"
	"strings"

	"github.com/autobrr/go-bdinfo/internal/fs"
	"github.com/autobrr/go-bdinfo/pkg/bdinfo/video"
)

const collectionBatchSize = 64 << 10
const collectionBoundaryLimit = 256

// This optional collector shares packets with the report scanner.
// Separate PES header state preserves original timestamps. The report keeps its
// DTS substitution, probe limits, and tolerance of damaged input.
type videoCollection struct {
	ctx         context.Context
	streams     map[uint16]*videoDelivery
	results     []video.StreamResult
	packetIndex uint64
	pmtPID      uint16
	pmtProgram  uint16
	pat, pmt    collectionPSI
	packetSize  int
}

type videoDelivery struct {
	consumer              video.Consumer
	result                *video.StreamResult
	data                  []byte
	starts                []video.PESStart
	batchHeaders          []byte
	offset                uint64
	segment               uint64
	err                   error
	callbackFailed        bool
	observed              bool
	active                bool
	resync                bool
	header                [264]byte
	headerLen, headerNeed int
	remaining             int
	boundary              video.PESStart
	continuity            collectionContinuity
}

func (s *StreamFile) newVideoCollection(ctx context.Context, playlists []*PlaylistFile, source video.Source) *videoCollection {
	if s.VideoConsumer == nil {
		return nil
	}
	c := &videoCollection{ctx: ctx, streams: make(map[uint16]*videoDelivery), pmtPID: 0xffff}
	pids := make([]uint16, 0, len(s.Streams))
	for pid, st := range s.Streams {
		if st != nil && st.Base().IsVideoStream() {
			pids = append(pids, pid)
		}
	}
	sort.Slice(pids, func(i, j int) bool { return pids[i] < pids[j] })
	// Stable backing storage: deliveries hold pointers into results.
	c.results = make([]video.StreamResult, len(pids))
	for i, pid := range pids {
		info := video.StreamInfo{Source: source, PID: pid, Codec: video.Codec(s.Streams[pid].Base().StreamType)}
		info.Occurrences = s.videoOccurrences(playlists, pid)
		info = cloneVideoInfo(info)
		r := &c.results[i]
		r.Info = info
		r.Status = video.Declined
		if err := ctx.Err(); err != nil {
			r.Status = video.Canceled
			r.Err = err
			continue
		}
		consumer, err := s.VideoConsumer(ctx, cloneVideoInfo(info))
		if err != nil {
			r.Status = video.Failed
			r.Err = err
			if consumer != nil {
				r.Err = errors.Join(err, consumer.Finish(video.End{Err: err}))
			}
			continue
		}
		if consumer == nil {
			continue
		}
		r.Status = video.Incomplete
		c.streams[pid] = &videoDelivery{consumer: consumer, result: r, data: make([]byte, 0, collectionBatchSize), starts: make([]video.PESStart, 0, collectionBoundaryLimit)}
	}
	return c
}

func cloneVideoInfo(info video.StreamInfo) video.StreamInfo {
	info.Occurrences = append([]video.Occurrence(nil), info.Occurrences...)
	for i := range info.Occurrences {
		if info.Occurrences[i].STC != nil {
			sequence := *info.Occurrences[i].STC
			info.Occurrences[i].STC = &sequence
		}
	}
	return info
}

func (c *videoCollection) readPrefix(r io.Reader, b []byte) (int, error) {
	n := 0
	for n < len(b) {
		if err := c.ctx.Err(); err != nil {
			return n, err
		}
		count, err := r.Read(b[n:])
		n += count
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

func (s *StreamFile) videoOccurrences(playlists []*PlaylistFile, pid uint16) []video.Occurrence {
	var out []video.Occurrence
	for _, p := range playlists {
		for _, item := range p.TimelineItems {
			for _, angle := range item.Angles {
				if angle.Source.LogicalClip != s.Name {
					continue
				}
				found := false
				for _, m := range angle.Video {
					if m.PID != pid || m.EntryType != 1 {
						continue
					}
					out = append(out, video.Occurrence{Playlist: p.Name, Index: item.Index, Angle: angle.Index, STCID: angle.STCID, Mapping: m, STC: angle.STC, AssociationError: angle.Err})
					found = true
				}
				if !found {
					out = append(out, video.Occurrence{Playlist: p.Name, Index: item.Index, Angle: angle.Index, STCID: angle.STCID, Mapping: video.Mapping{PID: pid, Codec: video.Codec(s.Streams[pid].Base().StreamType), Role: video.Unknown}, STC: angle.STC, AssociationError: angle.Err})
				}
			}
		}
	}
	return out
}

func (d *videoDelivery) failCallback(err error) {
	if err != nil {
		d.err = errors.Join(d.err, err)
		d.callbackFailed = true
	}
}

func (d *videoDelivery) flush(ctx context.Context, terminal bool) {
	if d.callbackFailed || ctx.Err() != nil {
		return
	}
	end := d.offset + uint64(len(d.data))
	n := len(d.starts)
	// A boundary at the end belongs to the next chunk, including an empty final PES.
	if len(d.data) > 0 {
		for n > 0 && d.starts[n-1].Offset == end {
			n--
		}
	}
	if len(d.data) == 0 && n == 0 {
		return
	}
	err := d.consumer.Consume(ctx, video.Chunk{Offset: d.offset, Data: d.data, Starts: d.starts[:n]})
	if err != nil {
		d.failCallback(err)
		return
	}
	d.result.DeliveredBytes += uint64(len(d.data))
	d.result.Boundaries += uint64(n)
	d.offset = end
	d.data = d.data[:0]
	copy(d.starts, d.starts[n:])
	remaining := len(d.starts) - n
	clear(d.starts[remaining:])
	d.starts = d.starts[:remaining]
	// Headers are borrowed for one callback. Keep any boundary at the chunk end
	// for the next callback, then reuse the rest of the batch's header storage.
	used := 0
	for i := range d.starts {
		header := d.starts[i].Header
		end := used + len(header)
		copy(d.batchHeaders[used:end], header)
		d.starts[i].Header = d.batchHeaders[used:end:end]
		used = end
	}
	d.batchHeaders = d.batchHeaders[:used]
	if terminal && len(d.starts) > 0 {
		d.flush(ctx, true)
	}
}

func (d *videoDelivery) appendPayload(ctx context.Context, payload []byte) {
	for len(payload) > 0 && !d.callbackFailed && ctx.Err() == nil {
		n := min(collectionBatchSize-len(d.data), len(payload))
		d.data = append(d.data, payload[:n]...)
		payload = payload[n:]
		if len(d.data) == collectionBatchSize {
			d.flush(ctx, false)
		}
	}
}

func (d *videoDelivery) discontinuity(ctx context.Context, index uint64, size int, reason string, loss bool) {
	if d.callbackFailed || ctx.Err() != nil {
		return
	}
	d.flush(ctx, true)
	if d.callbackFailed || ctx.Err() != nil {
		return
	}
	d.segment++
	event := video.Discontinuity{Offset: d.offset, PacketIndex: index, SourceOffset: index * uint64(size), Segment: d.segment, Reason: reason, DataLoss: loss}
	if loss && d.err == nil {
		d.err = fmt.Errorf("%w: packet %d: %s", video.ErrInvalidTransport, index, reason)
	}
	if err := d.consumer.Discontinuity(ctx, event); err != nil {
		d.failCallback(err)
	} else {
		d.result.Discontinuities++
	}
	d.result.Segment = d.segment
	d.continuity = collectionContinuity{}
	// A clock/counter reset does not interrupt a trustworthy unbounded PES or
	// completed bounded PES. Preserve continuation bytes (or bounded padding).
	if !loss {
		return
	}
	d.active = false
	d.resync = true
	d.headerLen = 0
	d.headerNeed = 0
	d.remaining = 0
}

type collectionContinuity struct {
	haveCC, previousPayload, duplicate bool
	cc                                 byte
	previous                           [188]byte
}

// Video and mapping-table packets obey the same continuity/retransmission rule.
func (p *collectionContinuity) observe(ts []byte, announced bool) (bool, string) {
	cc := ts[3] & 15
	hasPayload := ts[3]&0x10 != 0
	reason := ""
	if p.haveCC {
		expected := p.cc
		if hasPayload {
			expected = (expected + 1) & 15
		}
		if hasPayload && cc == p.cc && p.previousPayload {
			if sameRetransmission(p.previous[:], ts) {
				if !p.duplicate {
					p.duplicate = true
					return true, ""
				}
				reason = "ambiguous duplicate packet"
			} else if !announced {
				reason = "ambiguous duplicate packet"
			}
		} else if cc != expected && !announced {
			reason = "continuity counter gap"
		}
	}
	p.cc = cc
	p.haveCC = true
	p.previousPayload = hasPayload
	p.duplicate = false
	if hasPayload {
		copy(p.previous[:], ts)
	}
	return false, reason
}

// PSI is bounded to one legal section. Process every section in a packet, and
// let pointer bytes complete the preceding section before starting the next one.
type collectionPSI struct {
	continuity collectionContinuity
	data       [1024]byte
	n, needed  int
}

func (p *collectionPSI) append(payload []byte, newSections bool, consume func([]byte) bool) bool {
	for len(payload) > 0 {
		if p.n == 0 {
			if payload[0] == 0xff {
				return psiStuffing(payload)
			}
			if !newSections {
				return false
			}
			p.needed = 3
		}
		n := min(p.needed-p.n, len(payload))
		copy(p.data[p.n:], payload[:n])
		p.n += n
		payload = payload[n:]
		if p.n == 3 && p.needed == 3 {
			p.needed = 3 + (int(p.data[1]&15)<<8 | int(p.data[2]))
			if p.needed < 12 || p.needed > len(p.data) {
				return false
			}
		}
		if p.n == p.needed {
			if !consume(p.data[:p.n]) {
				return false
			}
			p.n = 0
			p.needed = 0
			if !newSections {
				return psiStuffing(payload)
			}
		}
	}
	return true
}

func (p *collectionPSI) feed(payload []byte, start bool, consume func([]byte) bool) bool {
	if !start {
		if p.n == 0 {
			return psiStuffing(payload)
		}
		return p.append(payload, false, consume)
	}
	if len(payload) == 0 {
		return false
	}
	pointer := int(payload[0])
	payload = payload[1:]
	if pointer > len(payload) {
		return false
	}
	if p.n > 0 {
		if !p.append(payload[:pointer], false, consume) || p.n != 0 {
			return false
		}
	} else if !psiStuffing(payload[:pointer]) {
		return false
	}
	return p.append(payload[pointer:], true, consume)
}

func psiStuffing(b []byte) bool {
	for _, v := range b {
		if v != 0xff {
			return false
		}
	}
	return true
}

// H.222.0 (08/2023) 2.4.3.2/2.4.3.4 permits two consecutive packets of
// the same PID/CC with identical bytes except for the PCR value.
func sameRetransmission(a, b []byte) bool {
	if a[3]&0x20 != 0 && a[4] >= 7 && a[5]&0x10 != 0 {
		return bytes.Equal(a[:6], b[:6]) && bytes.Equal(a[12:], b[12:])
	}
	return bytes.Equal(a, b)
}

func (c *videoCollection) packet(pkt []byte, size, syncOffset int) {
	if c.ctx.Err() != nil || len(c.streams) == 0 {
		return
	}
	index := c.packetIndex
	c.packetIndex++
	c.packetSize = size
	if pkt[syncOffset] != 0x47 {
		for _, d := range c.streams {
			d.discontinuity(c.ctx, index, size, "invalid TS framing", true)
		}
		c.pat = collectionPSI{}
		c.pmt = collectionPSI{}
		return
	}
	ts := pkt[syncOffset:]
	pid := uint16(ts[1]&31)<<8 | uint16(ts[2])
	d := c.streams[pid]
	var psi *collectionPSI
	if pid == 0 {
		psi = &c.pat
	} else if pid == c.pmtPID {
		psi = &c.pmt
	}
	if d == nil && psi == nil {
		return
	}
	if d != nil && d.callbackFailed {
		return
	}
	control := (ts[3] >> 4) & 3
	at := 4
	announced := false
	if control == 0 {
		c.packetDamage(d, psi, index, size, "reserved adaptation control")
		return
	}
	if control&2 != 0 {
		n := int(ts[4])
		at = 5 + n
		if at > 188 || (control == 2 && at != 188) || (control == 3 && at == 188) || !validCollectionAdaptation(ts[5:at]) {
			c.packetDamage(d, psi, index, size, "invalid adaptation fields")
			return
		}
		announced = n > 0 && ts[5]&0x80 != 0
	}
	if ts[1]&0x80 != 0 || ts[3]&0xc0 != 0 {
		c.packetDamage(d, psi, index, size, "transport error or scrambling")
		return
	}
	if d != nil {
		d.observed = true
		duplicate, reason := d.continuity.observe(ts, announced)
		if duplicate {
			return
		}
		if reason != "" || announced {
			current := d.continuity
			loss := true
			if reason == "" {
				reason = "announced discontinuity"
				loss = d.headerNeed > 0 || d.remaining > 0
			}
			d.discontinuity(c.ctx, index, size, reason, loss)
			d.continuity = current
		}
	}
	if psi != nil {
		duplicate, reason := psi.continuity.observe(ts, announced)
		if duplicate {
			return
		}
		if announced && reason == "" {
			reason = "mapping table discontinuity"
		}
		if reason != "" {
			current := psi.continuity
			c.packetDamage(nil, psi, index, size, reason)
			psi.continuity = current
		}
	}
	if control&1 == 0 {
		return
	}
	start := ts[1]&0x40 != 0
	payload := ts[at:]
	if psi != nil {
		if !psi.feed(payload, start, func(section []byte) bool { return c.consumePSI(pid, section, index, size) }) {
			c.packetDamage(nil, psi, index, size, "invalid mapping section")
		}
		return
	}
	if d == nil || d.callbackFailed {
		return
	}
	if start {
		if d.headerNeed > 0 || d.remaining > 0 {
			d.discontinuity(c.ctx, index, size, "unfinished PES at next start", true)
		}
		d.active = true
		d.resync = false
		d.headerLen = 0
		d.headerNeed = 9
		d.remaining = -1
		d.boundary = video.PESStart{Offset: d.offset + uint64(len(d.data)), PacketIndex: index, SourceOffset: index * uint64(size), Segment: d.segment}
	} else if !d.active {
		if !d.resync {
			d.discontinuity(c.ctx, index, size, "missing PES start", true)
		}
		return
	}
	for d.headerNeed > 0 && len(payload) > 0 {
		n := min(d.headerNeed-d.headerLen, len(payload))
		copy(d.header[d.headerLen:], payload[:n])
		d.headerLen += n
		payload = payload[n:]
		if d.headerLen < d.headerNeed {
			return
		}
		if d.headerNeed == 9 {
			h := d.header[:9]
			length := int(h[4])<<8 | int(h[5])
			extra := int(h[8])
			flags := h[7] >> 6
			if h[0] != 0 || h[1] != 0 || h[2] != 1 || !(h[3] == 0xfd || h[3] >= 0xe0 && h[3] <= 0xef) || h[6]&0xf0 != 0x80 || flags == 1 || length > 0 && length < 3+extra || flags == 2 && extra < 5 || flags == 3 && extra < 10 {
				d.discontinuity(c.ctx, index, size, "invalid PES header", true)
				return
			}
			d.headerNeed = 9 + extra
			if length > 0 {
				d.remaining = length - 3 - extra
			}
			if d.headerLen < d.headerNeed {
				continue
			}
		}
		if !validCollectionPESFields(d.header[:d.headerNeed]) {
			d.discontinuity(c.ctx, index, size, "invalid optional PES fields", true)
			return
		}
		flags := d.header[7] >> 6
		if flags >= 2 {
			prefix := byte(2)
			if flags == 3 {
				prefix = 3
			}
			pts, ok := originalPESTimestamp(d.header[9:14], prefix)
			if !ok {
				d.discontinuity(c.ctx, index, size, "invalid PTS markers", true)
				return
			}
			d.boundary.PTS = pts
			d.boundary.HasPTS = true
			if flags == 3 {
				dts, ok := originalPESTimestamp(d.header[14:19], 1)
				if !ok {
					d.discontinuity(c.ctx, index, size, "invalid DTS markers", true)
					return
				}
				d.boundary.DTS = dts
				d.boundary.HasDTS = true
			}
		}
		if len(d.starts) == collectionBoundaryLimit {
			d.flush(c.ctx, true)
		}
		if d.callbackFailed {
			return
		}
		if d.batchHeaders == nil {
			d.batchHeaders = make([]byte, 0, collectionBoundaryLimit*len(d.header))
		}
		start := len(d.batchHeaders)
		d.batchHeaders = append(d.batchHeaders, d.header[:d.headerNeed]...)
		end := len(d.batchHeaders)
		d.boundary.Header = d.batchHeaders[start:end:end]
		d.starts = append(d.starts, d.boundary)
		d.boundary.Header = nil
		d.headerNeed = 0
	}
	if d.remaining >= 0 && len(payload) > d.remaining {
		if !psiStuffing(payload[d.remaining:]) {
			d.discontinuity(c.ctx, index, size, "non-stuffing bounded PES tail", true)
			return
		}
		payload = payload[:d.remaining]
	}
	if d.remaining == 0 {
		return
	}
	if d.remaining > 0 {
		d.remaining -= len(payload)
	}
	d.appendPayload(c.ctx, payload)
}

func (c *videoCollection) packetDamage(d *videoDelivery, psi *collectionPSI, index uint64, size int, reason string) {
	if psi != nil {
		*psi = collectionPSI{}
		c.mappingDamage(index, size, reason)
	} else if d != nil {
		d.discontinuity(c.ctx, index, size, reason, true)
	}
}

func (c *videoCollection) consumePSI(pid uint16, section []byte, index uint64, size int) bool {
	if !validCollectionPSI(section) {
		return false
	}
	if pid == 0 {
		if section[0] != 0 || section[6] != 0 || section[7] != 0 || (len(section)-12)%4 != 0 {
			return false
		}
		if section[5]&1 == 0 {
			return true
		}
		// Collection tracks one program. Duplicates and multiple programs cannot
		// establish a unique PID map; the report's tolerant first match is unsafe.
		var program, p uint16
		network := false
		for i := 8; i < len(section)-4; i += 4 {
			number := uint16(section[i])<<8 | uint16(section[i+1])
			if number == 0 {
				if network {
					return false
				}
				network = true
				continue
			}
			if program != 0 {
				return false
			}
			program = number
			p = uint16(section[i+2]&0x1f)<<8 | uint16(section[i+3])
		}
		if program == 0 {
			return false
		}
		if c.pmtPID != p || c.pmtProgram != program {
			if c.pmtPID != 0xffff {
				c.mappingDamage(index, size, "program mapping changed")
			}
			c.pmt = collectionPSI{}
			c.pmtPID = p
			c.pmtProgram = program
		}
		return true
	}
	if !validCollectionPMT(section) {
		return false
	}
	if section[5]&1 == 0 {
		return true
	}
	if uint16(section[3])<<8|uint16(section[4]) != c.pmtProgram {
		return false
	}
	_, _, entries, ok := parsePMTSection(section)
	if !ok {
		return false
	}
	pids := make(map[uint16]bool, len(entries))
	for _, e := range entries {
		if pids[e.PID] {
			return false
		}
		pids[e.PID] = true
	}
	for selected, d := range c.streams {
		found := false
		for _, e := range entries {
			if e.PID == selected && video.Codec(e.StreamType) == d.result.Info.Codec {
				found = true
				break
			}
		}
		if !found {
			d.discontinuity(c.ctx, index, size, "selected video PID/codec mapping changed", true)
			d.failCallback(video.ErrUnsupportedMapping)
		}
	}
	return true
}

func (c *videoCollection) invalidatePacketDomain() {
	// The factory received a snapshot before scanning. Bare 188-byte TS packets
	// cannot use the assumed M2TS clock mapping. Record that in the final results.
	for i := range c.results {
		for j := range c.results[i].Info.Occurrences {
			o := &c.results[i].Info.Occurrences[j]
			o.AssociationError = errors.Join(o.AssociationError, fmt.Errorf("%w: 188-byte TS and CLPI packet domains require mapping", video.ErrUnsupportedMapping))
		}
	}
}

func (c *videoCollection) mappingDamage(index uint64, size int, reason string) {
	for _, d := range c.streams {
		d.discontinuity(c.ctx, index, size, reason, true)
	}
}

// H.222.0 (08/2018), Tables 2-6 and 2-21. Use the bounded metadata reader:
// optional fields must fit their declared region, never borrow payload bytes.
func validCollectionAdaptation(fields []byte) bool {
	if len(fields) == 0 {
		return true
	}
	r := timelineReader{data: fields}
	flags := r.u8()
	if flags&0x18 == 8 {
		return false // H.222.0 requires PCR whenever OPCR is present.
	}
	for _, flag := range []byte{0x10, 0x08} {
		if flags&flag != 0 {
			b := r.take(6)
			if len(b) != 6 || b[4]&0x7e != 0x7e || int(b[4]&1)<<8|int(b[5]) >= 300 {
				return false
			}
		}
	}
	if flags&4 != 0 {
		r.take(1)
	}
	if flags&2 != 0 {
		r.take(int(r.u8()))
	}
	if flags&1 != 0 {
		ext := timelineReader{data: r.take(int(r.u8())), err: r.err}
		// H.222.0 Table 2-6 requires this flag byte when an extension is declared.
		// Unlike the outer adaptation field, an extension cannot have length zero.
		f := ext.u8()
		if f&15 != 15 {
			return false
		}
		ltwValid := false
		if f&0x80 != 0 {
			ltw := ext.take(2)
			if len(ltw) != 2 {
				return false
			}
			ltwValid = ltw[0]&0x80 != 0
		}
		if f&0x40 != 0 {
			rate := ext.take(3)
			if len(rate) != 3 || rate[0]&0xc0 != 0xc0 || ltwValid && rate[0]&63 == 0 && rate[1] == 0 && rate[2] == 0 {
				return false
			}
		}
		if f&0x20 != 0 {
			if flags&4 == 0 {
				return false
			}
			b := ext.take(5)
			if len(b) != 5 || !validTimestamp(b, b[0]&0xf0) {
				return false
			}
		}
		if f&0x10 == 0 {
			for ext.err == nil && ext.pos < len(ext.data) {
				ext.take(1)
				ext.take(int(ext.u8()))
			}
		} else {
			if !psiStuffing(ext.data[ext.pos:]) {
				return false
			}
		}
		if ext.err != nil {
			return false
		}
	}
	if r.err != nil {
		return false
	}
	return psiStuffing(r.data[r.pos:])
}

func validCollectionPESFields(h []byte) bool {
	timestamps := 0
	if h[7]>>6 == 2 {
		timestamps = 5
	} else if h[7]>>6 == 3 {
		timestamps = 10
	}
	if h[7]&0x3f == 0 && len(h) == 9+timestamps {
		return true
	}
	r := timelineReader{data: h[9:]}
	flags := h[7]
	r.take(timestamps)
	if flags&0x20 != 0 {
		b := r.take(6)
		if len(b) != 6 || b[0]&4 == 0 || b[2]&4 == 0 || b[4]&4 == 0 || b[5]&1 == 0 || int(b[4]&3)<<7|int(b[5]>>1) >= 300 {
			return false
		}
	}
	if flags&0x10 != 0 {
		b := r.take(3)
		if len(b) != 3 || b[0]&0x80 == 0 || b[2]&1 == 0 {
			return false
		}
	}
	if flags&8 != 0 {
		r.take(1)
	}
	if flags&4 != 0 {
		b := r.take(1)
		if len(b) != 1 || b[0]&0x80 == 0 {
			return false
		}
	}
	if flags&2 != 0 {
		r.take(2)
	}
	if flags&1 != 0 {
		f := r.u8()
		if f&0x80 != 0 {
			r.take(16)
		}
		if f&0x40 != 0 {
			r.take(int(r.u8()))
		}
		if f&0x20 != 0 {
			b := r.take(2)
			if len(b) != 2 || b[0]&0x80 == 0 || b[1]&0x80 == 0 {
				return false
			}
		}
		if f&0x10 != 0 {
			b := r.take(2)
			if len(b) != 2 || b[0]&0xc0 != 0x40 {
				return false
			}
		}
		if f&1 != 0 {
			n := r.u8()
			if n&0x80 == 0 {
				return false
			}
			ext := timelineReader{data: r.take(int(n & 0x7f)), err: r.err}
			f := ext.u8()
			if f&0x80 != 0 && f&1 == 0 {
				b := ext.take(5)
				if len(b) != 5 || !validTimestamp(b, b[0]&0xf0) {
					return false
				}
			}
			if ext.err != nil {
				return false
			}
		}
	}
	if r.err != nil {
		return false
	}
	return psiStuffing(r.data[r.pos:])
}

// H.222.0 Annex B uses the non-reflected IEEE polynomial with an all-ones
// initial remainder and no final XOR. Reverse bytes to reuse hash/crc32's proven
// reflected implementation; a complete section including its CRC has remainder 0.
func validCollectionPSI(section []byte) bool {
	if len(section) < 12 || len(section) > 1024 || section[1]&0x80 == 0 || 3+(int(section[1]&15)<<8|int(section[2])) != len(section) {
		return false
	}
	var reversed [1024]byte
	for i, v := range section {
		reversed[i] = bits.Reverse8(v)
	}
	return ^crc32.ChecksumIEEE(reversed[:len(section)]) == 0
}

func validCollectionPMT(section []byte) bool {
	if len(section) < 16 || section[0] != 2 || section[6] != 0 || section[7] != 0 {
		return false
	}
	at := 12 + (int(section[10]&15)<<8 | int(section[11]))
	end := len(section) - 4
	if at > end {
		return false
	}
	for at < end {
		if end-at < 5 {
			return false
		}
		n := int(section[at+3]&15)<<8 | int(section[at+4])
		at += 5 + n
		if at > end {
			return false
		}
	}
	return at == end
}

func originalPESTimestamp(b []byte, prefix byte) (uint64, bool) {
	return parsePTS(b), len(b) == 5 && validTimestamp(b, prefix<<4)
}

func (c *videoCollection) finish(cause error, clean bool) []video.StreamResult {
	if c.pat.n != 0 || c.pmt.n != 0 {
		c.mappingDamage(c.packetIndex, c.packetSize, "unfinished mapping section")
	}
	for _, d := range c.streams {
		if cause != nil {
			d.err = errors.Join(d.err, cause)
		}
		if !d.observed || !d.active || d.headerNeed > 0 || d.remaining > 0 {
			if d.err == nil {
				d.err = fmt.Errorf("%w: missing or unfinished selected PES", video.ErrIncomplete)
			}
		}
		// Valid bytes preceding a read error still belong to the consumer. Cancellation
		// cannot safely issue another Consume; Finish owns resource release in that case.
		if c.ctx.Err() == nil {
			d.flush(c.ctx, true)
		}
		if err := c.ctx.Err(); err != nil {
			d.err = errors.Join(d.err, err)
		}
		complete := clean && d.err == nil && !d.callbackFailed
		end := video.End{CleanEOF: complete, Err: d.err}
		if complete {
			end.SourcePacketCount = c.packetIndex
		}
		if !complete && end.Err == nil {
			end.Err = io.ErrUnexpectedEOF
		}
		finishErr := d.consumer.Finish(end)
		d.result.Err = errors.Join(end.Err, finishErr)
		d.result.CleanEOF = complete
		d.result.SourcePacketCount = end.SourcePacketCount
		switch {
		case errors.Is(d.result.Err, context.Canceled) || errors.Is(d.result.Err, context.DeadlineExceeded):
			d.result.Status = video.Canceled
		case d.callbackFailed || finishErr != nil:
			d.result.Status = video.Failed
		case !complete:
			d.result.Status = video.Incomplete
		default:
			d.result.Status = video.Complete
		}
		d.data = nil
		d.starts = nil
	}
	return c.results
}

func streamSource(s *StreamFile, ssif bool) video.Source {
	source := video.Source{Path: containerPath(s.FileInfo, "BDMV/STREAM/"), Kind: "m2ts", LogicalClip: s.Name}
	if ssif && s.InterleavedFile != nil {
		source.Path = containerPath(s.InterleavedFile.FileInfo, "BDMV/STREAM/SSIF/")
		source.Kind = "ssif"
	}
	return source
}

func containerPath(file fs.FileInfo, prefix string) string {
	if file == nil {
		return prefix
	}
	full := strings.ReplaceAll(file.FullName(), "\\", "/")
	// Find the containing BDMV in original directory components. Unicode case
	// mappings can change byte lengths, and ancestors can also be named BDMV.
	for end := strings.LastIndexByte(full, '/'); end >= 0; {
		start := strings.LastIndexByte(full[:end], '/') + 1
		if strings.EqualFold(full[start:end], "BDMV") {
			return full[start:]
		}
		end = start - 1
	}
	return prefix + file.Name()
}
