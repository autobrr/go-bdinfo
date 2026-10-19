// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bdinfo

import (
	"errors"
	"fmt"

	"github.com/autobrr/go-bdinfo/internal/bdrom"
	"github.com/autobrr/go-bdinfo/pkg/bdinfo/video"
)

// PlaylistTimeline retains exact MPLS occurrences in playback order.
// Times use 45,000 ticks/second; PES clocks use 90,000.
// The default angle is Items[i].Angles[0]; other angles are retained for inspection.
// Complete means timing and selected STC clock associations are valid.
// It does not mean that video or HDR metadata was extracted.
// Scanned indicates that Run attempted a scan. See Result.Collection for outcomes.
// Discovery assumes 192-byte M2TS packets when checking file extents.
// Run invalidates that clock association if it finds bare 188-byte TS packets.
// Err explains invalid or unsupported timing. Returned slices are owned snapshots.
type PlaylistTimeline struct {
	Name       string
	Items      []PlayItem
	Duration45 uint64
	Complete   bool
	Scanned    bool
	Err        error
}

// PlayItem is one occurrence, including repeated uses of the same physical clip.
// Index is zero-based. In45/Out45 preserve unsigned MPLS values for [In45, Out45).
// Offset45 and Duration45 use integer ticks, counted once per item.
// ConnectionCondition preserves the raw join condition. It cannot alone prove
// that decoder or metadata state continues across the join.
// Angles[0] is the default angle. Configurable angle selection is not supported.
// Err covers unsupported join conditions.
type PlayItem struct {
	Index               int
	In45                uint32
	Out45               uint32
	Offset45            uint64
	Duration45          uint64
	ConnectionCondition uint8
	Angles              []Angle
	Err                 error
}

// Angle identifies an alternate physical/logical clip and its per-item video
// mappings. Index is zero-based. STCID is the original MPLS clock reference;
// STC is in the logical M2TS packet domain, even when Source is SSIF. An absent
// or contradictory association, including unverified SSIF coordinate mapping,
// sets Err. DifferentAudio and Seamless preserve raw multiangle flags and are
// false for ordinary single-angle items. These flags do not validate an HEVC join.
type Angle struct {
	Index          int
	Source         video.Source
	STCID          uint8
	DifferentAudio bool
	Seamless       bool
	Video          []video.Mapping
	STC            *video.STCSequence
	Err            error
}

const (
	connectionNonSeamless = 1
	connectionSeamless5   = 5
	connectionSeamless6   = 6
)

func buildTimelines(playlists []*bdrom.PlaylistFile, streams map[string]*bdrom.StreamFile, scanned bool) []PlaylistTimeline {
	out := make([]PlaylistTimeline, 0, len(playlists))
	for _, p := range playlists {
		t := PlaylistTimeline{Name: p.Name, Duration45: p.TimelineDuration45, Scanned: scanned, Err: p.TimelineErr, Complete: p.CaptureTimeline && p.TimelineErr == nil}
		for _, item := range p.TimelineItems {
			entry := PlayItem{Index: item.Index, In45: item.In45, Out45: item.Out45, Offset45: item.Offset45, Duration45: item.Duration45, ConnectionCondition: item.Connection}
			if item.Connection != connectionNonSeamless && item.Connection != connectionSeamless5 && item.Connection != connectionSeamless6 {
				entry.Err = fmt.Errorf("%w: connection condition %d", video.ErrUnsupportedMapping, item.Connection)
			}
			for _, a := range item.Angles {
				angle := Angle{Index: a.Index, Source: a.Source, STCID: a.STCID, DifferentAudio: a.DifferentAudio, Seamless: a.Seamless, Video: append([]video.Mapping(nil), a.Video...), Err: a.Err}
				if sf := streams[a.Source.LogicalClip]; scanned && sf != nil && sf.PacketSize == 188 {
					angle.Err = errors.Join(angle.Err, fmt.Errorf("%w: 188-byte TS and CLPI packet domains require mapping", video.ErrUnsupportedMapping))
				}
				if a.STC != nil {
					sequence := *a.STC
					angle.STC = &sequence
				}
				entry.Angles = append(entry.Angles, angle)
			}
			if len(entry.Angles) > 0 {
				t.Err = errors.Join(t.Err, entry.Err, entry.Angles[0].Err)
			}
			t.Items = append(t.Items, entry)
		}
		t.Complete = t.Complete && t.Err == nil
		out = append(out, t)
	}
	return out
}
