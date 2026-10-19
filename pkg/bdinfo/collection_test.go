// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bdinfo_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/go-bdinfo/pkg/bdinfo"
	"github.com/autobrr/go-bdinfo/pkg/bdinfo/video"
)

type recorder struct {
	data   []byte
	ends   []video.End
	starts []video.PESStart
}

func (r *recorder) Consume(_ context.Context, c video.Chunk) error {
	r.data = append(r.data, c.Data...)
	r.starts = append(r.starts, c.Starts...)
	return nil
}
func (r *recorder) Discontinuity(context.Context, video.Discontinuity) error { return nil }
func (r *recorder) Finish(e video.End) error                                 { r.ends = append(r.ends, e); return nil }

func discFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeDiscFixture(t, root)
	return root
}

func TestTruncatedPlaylistStreamAddsReportWarning(t *testing.T) {
	root := discFixture(t)
	path := filepath.Join(root, "BDMV", "PLAYLIST", "00000.mpls")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Cut the attributes of the final stream in the final play item.
	if err := os.WriteFile(path, data[:len(data)-9], 0644); err != nil {
		t.Fatal(err)
	}
	cfg := bdinfo.DefaultSettings(".")
	cfg.FilterShortPlaylists = false
	for _, capture := range []bool{false, true} {
		result, err := bdinfo.Run(t.Context(), bdinfo.Options{Path: root, Settings: cfg, IncludeTimeline: capture})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(result.Report, "WARNING: File errors") || !strings.Contains(result.Report, "00000.MPLS") {
			t.Fatalf("capture %v: missing playlist file warning in report:\n%s", capture, result.Report)
		}
	}
}

func writeDiscFixture(t *testing.T, root string) {
	t.Helper()
	for _, dir := range []string{"PLAYLIST", "CLIPINF", "STREAM"} {
		if err := os.MkdirAll(filepath.Join(root, "BDMV", dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(name string, b []byte) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, "BDMV", filepath.FromSlash(name)), b, 0644); err != nil {
			t.Fatal(err)
		}
	}
	clpi := make([]byte, 60)
	copy(clpi, "HDMV0200")
	binary.BigEndian.PutUint32(clpi[8:], 60)
	binary.BigEndian.PutUint32(clpi[40:], 16)
	binary.BigEndian.PutUint32(clpi[56:], 2)
	seq := make([]byte, 22)
	seq[1] = 1
	seq[6] = 1
	seq[7] = 7
	binary.BigEndian.PutUint16(seq[8:], 0x1001)
	binary.BigEndian.PutUint32(seq[18:], 90000)
	clpi = append(clpi, 0, 0, 0, 22)
	clpi = append(clpi, seq...)
	binary.BigEndian.PutUint32(clpi[12:], uint32(len(clpi)))
	program := make([]byte, 20)
	program[1] = 1
	program[8] = 1
	program[10] = 0x10
	program[11] = 0x11
	program[12] = 4
	program[13] = 0x24
	program[14] = 0x61
	program[15] = 0x30
	clpi = append(clpi, 0, 0, 0, 20)
	clpi = append(clpi, program...)
	write("CLIPINF/00001.clpi", clpi)
	playlist := make([]byte, 64)
	copy(playlist, "MPLS0200")
	binary.BigEndian.PutUint32(playlist[8:], 64)
	list := make([]byte, 6)
	list[3] = 2
	for range 2 {
		item := make([]byte, 32)
		copy(item, "00001M2TS")
		item[10] = 5
		item[11] = 7
		binary.BigEndian.PutUint32(item[12:], 1)
		binary.BigEndian.PutUint32(item[16:], 45001)
		stn := make([]byte, 14)
		stn[2] = 1
		stn = append(stn, 3, 1, 0x10, 0x11, 4, 0x24, 0x61, 0x30, 0)
		item = append(item, 0, byte(len(stn)))
		item = append(item, stn...)
		list = append(list, 0, byte(len(item)))
		list = append(list, item...)
	}
	playlist = append(playlist, 0, 0, byte(len(list)>>8), byte(len(list)))
	playlist = append(playlist, list...)
	binary.BigEndian.PutUint32(playlist[12:], uint32(len(playlist)))
	playlist = append(playlist, 0, 0, 0, 2, 0, 0)
	write("PLAYLIST/00000.mpls", playlist)
	write("PLAYLIST/00001.mpls", playlist)
	pes := []byte{0, 0, 1, 0xe0, 0, 6, 0x80, 0, 0, 0xaa, 0xbb, 0xcc}
	var transport []byte
	for cc := byte(0); cc < 2; cc++ {
		p := make([]byte, 192)
		p[4] = 0x47
		p[5] = 0x50
		p[6] = 0x11
		p[7] = 0x30 | cc
		p[8] = byte(183 - len(pes))
		for j := 10; j < 192-len(pes); j++ {
			p[j] = 0xff
		}
		copy(p[len(p)-len(pes):], pes)
		transport = append(transport, p...)
	}
	write("STREAM/00001.m2ts", transport)
}

func TestCollectionDiscRelativeSourceIdentity(t *testing.T) {
	for _, ancestor := range []string{"bDmV", "\u0131", "\u023f", "\u96ea"} {
		for _, ssif := range []bool{false, true} {
			t.Run(ancestor+"/"+map[bool]string{false: "M2TS", true: "SSIF"}[ssif], func(t *testing.T) {
				root := filepath.Join(t.TempDir(), ancestor, "Film")
				writeDiscFixture(t, root)
				want := video.Source{Path: "BDMV/STREAM/00001.m2ts", Kind: "m2ts", LogicalClip: "00001.M2TS"}
				if ssif {
					dir := filepath.Join(root, "BDMV", "STREAM", "SSIF")
					if err := os.MkdirAll(dir, 0755); err != nil {
						t.Fatal(err)
					}
					data, err := os.ReadFile(filepath.Join(root, "BDMV", "STREAM", "00001.m2ts"))
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(dir, "00001.ssif"), data, 0644); err != nil {
						t.Fatal(err)
					}
					want.Path, want.Kind = "BDMV/STREAM/SSIF/00001.ssif", "ssif"
				}
				cfg := bdinfo.DefaultSettings(".")
				cfg.PlaylistOnly = "00000.MPLS"
				cfg.FilterLoopingPlaylists, cfg.FilterShortPlaylists = false, false
				var factorySource video.Source
				res, err := bdinfo.Run(t.Context(), bdinfo.Options{Path: root, Settings: cfg, VideoConsumer: func(_ context.Context, info video.StreamInfo) (video.Consumer, error) {
					factorySource = info.Source
					return &recorder{}, nil
				}})
				if err != nil {
					t.Fatal(err)
				}
				if factorySource != want || len(res.Collection) != 1 || res.Collection[0].Info.Source != want || len(res.Timelines) != 1 || res.Timelines[0].Items[0].Angles[0].Source != want {
					t.Fatalf("source identity: factory %+v collection %+v timeline %+v want %+v", factorySource, res.Collection, res.Timelines, want)
				}
				discovery, err := bdinfo.DiscoverPlaylists(t.Context(), bdinfo.Options{Path: root, Settings: cfg, IncludeTimeline: true})
				if err != nil || len(discovery.Timelines) != 1 || discovery.Timelines[0].Items[0].Angles[0].Source != want {
					t.Fatalf("discovery identity: %+v error %v", discovery.Timelines, err)
				}
			})
		}
	}
}

func TestRunCollectionAndDiscoveryContract(t *testing.T) {
	root := discFixture(t)
	cfg := bdinfo.DefaultSettings(".")
	cfg.FilterLoopingPlaylists = false
	cfg.FilterShortPlaylists = false
	cfg.BigPlaylistOnly = false
	base, err := bdinfo.Run(t.Context(), bdinfo.Options{Path: root, Settings: cfg})
	if err != nil {
		t.Fatal(err)
	}
	if base.Timelines != nil || base.Collection != nil {
		t.Fatal("default allocated collection/timelines")
	}
	calls := 0
	r := &recorder{}
	var infos []video.StreamInfo
	opts := bdinfo.Options{Path: root, Settings: cfg, VideoConsumer: func(_ context.Context, info video.StreamInfo) (video.Consumer, error) {
		calls++
		infos = append(infos, info)
		return r, nil
	}}
	discovered, err := bdinfo.DiscoverPlaylists(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 0 || discovered.Collection != nil || len(discovered.Timelines) != 2 || discovered.Timelines[0].Scanned {
		t.Fatalf("discovery %+v calls %d", discovered, calls)
	}
	result, err := bdinfo.Run(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if result.Report != base.Report || result.QuickSummary != base.QuickSummary || result.ForumsBlock != base.ForumsBlock {
		t.Fatal("collection changed report text")
	}
	if calls != 1 || len(r.ends) != 1 || !r.ends[0].CleanEOF || !bytes.Equal(r.data, []byte{0xaa, 0xbb, 0xcc, 0xaa, 0xbb, 0xcc}) || len(result.Collection) != 1 || result.Collection[0].Status != video.Complete {
		t.Fatalf("calls %d ends %+v collection %+v bytes %x", calls, r.ends, result.Collection, r.data)
	}
	if len(infos[0].Occurrences) != 4 || infos[0].Occurrences[0].Mapping.Role != video.Primary {
		t.Fatalf("info %+v", infos)
	}
	for _, timeline := range result.Timelines {
		if !timeline.Complete || !timeline.Scanned || len(timeline.Items) != 2 || timeline.Duration45 != 90000 || timeline.Items[1].Offset45 != 45000 {
			t.Fatalf("timeline %+v", timeline)
		}
	}
	opts.Settings.PlaylistOnly = "00000.MPLS"
	scoped, err := bdinfo.Run(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(scoped.Timelines) != 1 || scoped.Timelines[0].Name != "00000.MPLS" {
		t.Fatalf("scoped %+v", scoped.Timelines)
	}
}

func TestDiscoveryTimelinesIgnoreReportSelection(t *testing.T) {
	root := discFixture(t)
	for _, tc := range []struct {
		name            string
		main, big       bool
		summary, noText bool
		filters         bool
		playlist        string
		want            int
	}{
		{name: "main", main: true, want: 2},
		{name: "biggest", big: true, want: 2},
		{name: "main summary", main: true, summary: true, want: 2},
		{name: "biggest summary", big: true, summary: true, want: 2},
		{name: "summary without text", summary: true, noText: true, want: 2},
		{name: "report filters", filters: true, want: 2},
		{name: "explicit playlist", main: true, playlist: "00001.MPLS", want: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := bdinfo.DefaultSettings(".")
			cfg.FilterLoopingPlaylists, cfg.FilterShortPlaylists = tc.filters, tc.filters
			cfg.MainPlaylistOnly, cfg.BigPlaylistOnly = tc.main, tc.big
			cfg.SummaryOnly, cfg.GenerateTextSummary = tc.summary, !tc.noText
			cfg.PlaylistOnly = tc.playlist
			calls := 0
			result, err := bdinfo.DiscoverPlaylists(t.Context(), bdinfo.Options{Path: root, Settings: cfg, VideoConsumer: func(context.Context, video.StreamInfo) (video.Consumer, error) {
				calls++
				return &recorder{}, nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Timelines) != tc.want || len(result.Playlists) != tc.want || calls != 0 || result.Collection != nil || result.Report != "" {
				t.Fatalf("discovery result %+v, calls %d; want %d metadata-only timelines", result, calls, tc.want)
			}
			for i, timeline := range result.Timelines {
				if timeline.Name != result.Playlists[i].Name || timeline.Scanned || len(timeline.Items) != 2 || timeline.Duration45 != 90000 {
					t.Fatalf("discovery timeline %+v does not match playlist %+v", timeline, result.Playlists[i])
				}
			}
		})
	}
}

func TestRunCollectionPrecedesReportSelection(t *testing.T) {
	root := discFixture(t)
	for _, suffix := range []string{"CLIPINF/00001.clpi", "STREAM/00001.m2ts", "PLAYLIST/00001.mpls"} {
		data, err := os.ReadFile(filepath.Join(root, "BDMV", filepath.FromSlash(suffix)))
		if err != nil {
			t.Fatal(err)
		}
		data = bytes.ReplaceAll(data, []byte("00001M2TS"), []byte("00002M2TS"))
		if err := os.WriteFile(filepath.Join(root, "BDMV", filepath.FromSlash(strings.ReplaceAll(suffix, "00001", "00002"))), data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	for _, mode := range []string{"main", "biggest"} {
		t.Run(mode, func(t *testing.T) {
			cfg := bdinfo.DefaultSettings(".")
			cfg.FilterLoopingPlaylists, cfg.FilterShortPlaylists = false, false
			cfg.MainPlaylistOnly, cfg.BigPlaylistOnly = mode == "main", mode == "biggest"
			base, err := bdinfo.Run(t.Context(), bdinfo.Options{Path: root, Settings: cfg})
			if err != nil {
				t.Fatal(err)
			}
			infos := make(chan video.StreamInfo, 3)
			result, err := bdinfo.Run(t.Context(), bdinfo.Options{Path: root, Settings: cfg, VideoConsumer: func(_ context.Context, info video.StreamInfo) (video.Consumer, error) {
				infos <- info
				return &recorder{}, nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			if result.Report != base.Report || result.QuickSummary != base.QuickSummary || result.ForumsBlock != base.ForumsBlock {
				t.Fatal("collection changed report text")
			}
			if len(result.Timelines) != 1 || len(result.Collection) != 2 || len(infos) != 2 {
				t.Fatalf("selected timelines %+v, collection %+v, factory calls %d", result.Timelines, result.Collection, len(infos))
			}
			if first, second := <-infos, <-infos; first.Source == second.Source {
				t.Fatal("shared source collected more than once")
			}
			for _, collected := range result.Collection {
				if collected.Status != video.Complete || !collected.CleanEOF || collected.DeliveredBytes != 6 {
					t.Fatalf("source collection %+v", collected)
				}
			}
		})
	}
}

func TestTimelineMissingClockAndMetadataFailure(t *testing.T) {
	root := discFixture(t)
	cfg := bdinfo.DefaultSettings(".")
	cfg.FilterLoopingPlaylists = false
	cfg.FilterShortPlaylists = false
	path := filepath.Join(root, "BDMV", "CLIPINF", "00001.clpi")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	binary.BigEndian.PutUint32(data[8:], 0)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	res, err := bdinfo.DiscoverPlaylists(t.Context(), bdinfo.Options{Path: root, Settings: cfg, IncludeTimeline: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Timelines) != 2 || res.Timelines[0].Complete || !errors.Is(res.Timelines[0].Err, video.ErrUnsupportedMapping) {
		t.Fatalf("missing clock %+v", res.Timelines)
	}
	if err := os.Remove(filepath.Join(root, "BDMV", "PLAYLIST", "00000.mpls")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "BDMV", "PLAYLIST", "00001.mpls"), []byte("MPLS0200"), 0644); err != nil {
		t.Fatal(err)
	}
	res, err = bdinfo.DiscoverPlaylists(t.Context(), bdinfo.Options{Path: root, Settings: cfg, IncludeTimeline: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Timelines) != 1 || res.Timelines[0].Complete || res.Timelines[0].Err == nil {
		t.Fatalf("metadata failure %+v", res.Timelines)
	}
}

func TestOptionalFactoryFailurePreservesReport(t *testing.T) {
	root := discFixture(t)
	cfg := bdinfo.DefaultSettings(".")
	cfg.FilterLoopingPlaylists = false
	cfg.FilterShortPlaylists = false
	base, err := bdinfo.Run(t.Context(), bdinfo.Options{Path: root, Settings: cfg})
	if err != nil {
		t.Fatal(err)
	}
	cause := errors.New("unsupported decoder")
	res, err := bdinfo.Run(t.Context(), bdinfo.Options{Path: root, Settings: cfg, VideoConsumer: func(context.Context, video.StreamInfo) (video.Consumer, error) { return nil, cause }})
	if err != nil {
		t.Fatal(err)
	}
	if res.Report != base.Report || len(res.Scan.FileErrors) != 0 || len(res.Collection) != 1 || res.Collection[0].Status != video.Failed || !errors.Is(res.Collection[0].Err, cause) {
		t.Fatalf("result %+v", res)
	}
}

func TestCollectionRejectsEmptyAdaptationExtension(t *testing.T) {
	root := discFixture(t)
	path := filepath.Join(root, "BDMV", "STREAM", "00001.m2ts")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for at := 0; at < len(data); at += 192 {
		p := data[at : at+192]
		p[9], p[10] = 1, 0 // extension flag and zero declared body length
		for i := 11; i < 9+int(p[8]); i++ {
			p[i] = 0xff
		}
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	cfg := bdinfo.DefaultSettings(".")
	cfg.PlaylistOnly = "00000.MPLS"
	cfg.FilterLoopingPlaylists, cfg.FilterShortPlaylists = false, false
	base, err := bdinfo.Run(t.Context(), bdinfo.Options{Path: root, Settings: cfg})
	if err != nil {
		t.Fatal(err)
	}
	r := &recorder{}
	res, err := bdinfo.Run(t.Context(), bdinfo.Options{Path: root, Settings: cfg, VideoConsumer: func(context.Context, video.StreamInfo) (video.Consumer, error) { return r, nil }})
	if err != nil {
		t.Fatal(err)
	}
	if res.Report != base.Report || res.QuickSummary != base.QuickSummary || res.ForumsBlock != base.ForumsBlock || len(res.Collection) != 1 || res.Collection[0].Status != video.Incomplete || !errors.Is(res.Collection[0].Err, video.ErrInvalidTransport) || len(r.data) != 0 || len(r.ends) != 1 || r.ends[0].CleanEOF {
		t.Fatalf("missing required extension flags accepted: collection %+v data %x ends %+v", res.Collection, r.data, r.ends)
	}
}

func TestOptionalTimelineFailurePreservesReportAndCollection(t *testing.T) {
	for _, mode := range []string{"wrapped interval", "STN length", "short HEVC attributes"} {
		t.Run(mode, func(t *testing.T) {
			root := discFixture(t)
			path := filepath.Join(root, "BDMV", "PLAYLIST", "00000.mpls")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "wrapped interval" {
				binary.BigEndian.PutUint32(data[88:], 0x80000001)
			} else if mode == "STN length" {
				// Legacy parsing ignores this enclosing length; all accessed bytes remain.
				binary.BigEndian.PutUint16(data[108:], 0)
			} else {
				data = bytes.ReplaceAll(data, []byte{4, 0x24, 0x61, 0x30, 0}, []byte{3, 0x24, 0x61, 0x30, 0})
			}
			if err := os.WriteFile(path, data, 0644); err != nil {
				t.Fatal(err)
			}
			cfg := bdinfo.DefaultSettings(".")
			cfg.PlaylistOnly = "00000.MPLS"
			cfg.FilterLoopingPlaylists, cfg.FilterShortPlaylists = false, false
			base, err := bdinfo.Run(t.Context(), bdinfo.Options{Path: root, Settings: cfg})
			if err != nil {
				t.Fatal(err)
			}
			r := &recorder{}
			calls := 0
			res, err := bdinfo.Run(t.Context(), bdinfo.Options{Path: root, Settings: cfg, VideoConsumer: func(context.Context, video.StreamInfo) (video.Consumer, error) { calls++; return r, nil }})
			if err != nil {
				t.Fatal(err)
			}
			if res.Report != base.Report || res.QuickSummary != base.QuickSummary || res.ForumsBlock != base.ForumsBlock || calls != 1 || len(res.Collection) != 1 || res.Collection[0].Status != video.Complete {
				t.Fatalf("optional failure changed report or scan: calls %d collection %+v", calls, res.Collection)
			}
			if len(res.Timelines) != 1 || res.Timelines[0].Complete || res.Timelines[0].Err == nil {
				t.Fatal(res.Timelines)
			}
		})
	}
}

func TestTimelineRequiresMainVideoCLPIMapping(t *testing.T) {
	root := discFixture(t)
	path := filepath.Join(root, "BDMV", "CLIPINF", "00001.clpi")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	program := int(binary.BigEndian.Uint32(data[12:])) + 4
	data[program+8] = 0
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	cfg := bdinfo.DefaultSettings(".")
	cfg.PlaylistOnly = "00000.MPLS"
	cfg.FilterLoopingPlaylists, cfg.FilterShortPlaylists = false, false
	res, err := bdinfo.DiscoverPlaylists(t.Context(), bdinfo.Options{Path: root, Settings: cfg, IncludeTimeline: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Timelines) != 1 || res.Timelines[0].Complete || !errors.Is(res.Timelines[0].Err, video.ErrIncomplete) {
		t.Fatalf("missing CLPI video mapping %+v", res.Timelines)
	}
}

func TestTimelineSTCRangeRequiresPhysicalM2TSPackets(t *testing.T) {
	for _, mode := range []string{
		"valid", "unknown count valid", "start beyond source", "end beyond source",
		"unknown count start beyond source", "partial packet", "188 byte packet domain",
		"empty clip info", "short clip info", "clip info overlaps sequence", "clip info beyond file",
	} {
		t.Run(mode, func(t *testing.T) {
			root := discFixture(t)
			path := filepath.Join(root, "BDMV", "CLIPINF", "00001.clpi")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			seq := int(binary.BigEndian.Uint32(data[8:])) + 4
			transportPath := filepath.Join(root, "BDMV", "STREAM", "00001.m2ts")
			transport, err := os.ReadFile(transportPath)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "empty clip info":
				binary.BigEndian.PutUint32(data[40:], 0)
			case "short clip info":
				binary.BigEndian.PutUint32(data[40:], 15)
			case "clip info overlaps sequence":
				binary.BigEndian.PutUint32(data[40:], 17)
			case "clip info beyond file":
				binary.BigEndian.PutUint32(data[40:], ^uint32(0))
			case "unknown count valid":
				binary.BigEndian.PutUint32(data[56:], 0)
			case "start beyond source":
				binary.BigEndian.PutUint32(data[56:], 40)
				binary.BigEndian.PutUint32(data[seq+10:], 10)
			case "end beyond source":
				binary.BigEndian.PutUint32(data[56:], 40)
			case "unknown count start beyond source":
				binary.BigEndian.PutUint32(data[56:], 0)
				binary.BigEndian.PutUint32(data[seq+10:], 10)
			case "partial packet":
				transport = append(transport, 0)
			case "188 byte packet domain":
				// 48 TS packets have a byte length divisible by 192. The scan must
				// still reject the logical M2TS association after detecting TS framing.
				transport = nil
				original, err := os.ReadFile(transportPath)
				if err != nil {
					t.Fatal(err)
				}
				for cc := byte(0); cc < 48; cc++ {
					packet := append([]byte(nil), original[4:192]...)
					packet[3] = packet[3]&0xf0 | cc&15
					transport = append(transport, packet...)
				}
				binary.BigEndian.PutUint32(data[56:], uint32(len(transport)/192))
			}
			if err := os.WriteFile(path, data, 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(transportPath, transport, 0644); err != nil {
				t.Fatal(err)
			}
			cfg := bdinfo.DefaultSettings(".")
			cfg.PlaylistOnly = "00000.MPLS"
			cfg.FilterLoopingPlaylists, cfg.FilterShortPlaylists = false, false
			base, err := bdinfo.Run(t.Context(), bdinfo.Options{Path: root, Settings: cfg})
			if err != nil {
				t.Fatal(err)
			}
			r := &recorder{}
			res, err := bdinfo.Run(t.Context(), bdinfo.Options{Path: root, Settings: cfg, VideoConsumer: func(context.Context, video.StreamInfo) (video.Consumer, error) { return r, nil }})
			if err != nil {
				t.Fatal(err)
			}
			valid := mode == "valid" || mode == "unknown count valid"
			if len(res.Timelines) != 1 || res.Timelines[0].Complete != valid || (res.Timelines[0].Err == nil) != valid || res.Report != base.Report {
				t.Fatalf("physical association: timelines %+v", res.Timelines)
			}
			if len(res.Collection) != 1 || len(res.Collection[0].Info.Occurrences) == 0 || (res.Collection[0].Info.Occurrences[0].AssociationError == nil) != valid {
				t.Fatalf("collection association: %+v", res.Collection)
			}
			if valid && (!res.Timelines[0].Items[0].Angles[0].STC.HasEndPacket || res.Timelines[0].Items[0].Angles[0].STC.EndPacket != 2) {
				t.Fatal("missing physical STC end")
			}
			if mode == "188 byte packet domain" && (res.Collection[0].Status != video.Complete || len(r.data) != 48*3 || r.starts[1].SourceOffset != 188) {
				t.Fatalf("188 byte collection: %+v recorder %+v", res.Collection, r)
			}
			if mode == "188 byte packet domain" {
				timelinesOnly, err := bdinfo.Run(t.Context(), bdinfo.Options{Path: root, Settings: cfg, IncludeTimeline: true})
				if err != nil || timelinesOnly.Timelines[0].Complete || !errors.Is(timelinesOnly.Timelines[0].Err, video.ErrUnsupportedMapping) || timelinesOnly.Report != base.Report {
					t.Fatalf("188 byte timeline-only scan: %+v err %v", timelinesOnly, err)
				}
			}
			discovery, err := bdinfo.DiscoverPlaylists(t.Context(), bdinfo.Options{Path: root, Settings: cfg, IncludeTimeline: true})
			if err != nil {
				t.Fatal(err)
			}
			// Metadata-only discovery assumes nominal M2TS framing; it does not read
			// the stream to detect a coincidentally aligned bare-TS container.
			if mode != "188 byte packet domain" && discovery.Timelines[0].Complete != valid {
				t.Fatalf("discovery association: %+v", discovery.Timelines)
			}
		})
	}
}

func TestTimelineRequiresSupportedCLPIProgramContext(t *testing.T) {
	for _, mode := range []string{"multiple programs", "zero programs", "truncated entries", "truncated attributes", "short video coding info", "missing video aspect", "nonzero first start", "duplicate matching last", "duplicate mismatching last", "duplicate identical"} {
		t.Run(mode, func(t *testing.T) {
			root := discFixture(t)
			path := filepath.Join(root, "BDMV", "CLIPINF", "00001.clpi")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			offset := int(binary.BigEndian.Uint32(data[12:]))
			program := data[offset+4:]
			wantErr := video.ErrIncomplete
			switch mode {
			case "multiple programs":
				// A selected STC in the later program cannot use the first program's codec.
				seq := int(binary.BigEndian.Uint32(data[8:])) + 4
				binary.BigEndian.PutUint32(data[seq+10:], 1)
				program = append(program[:17:17], 0, 0, 0, 1, 0x10, 0, 1, 0, 0x10, 0x11, 4, 0x1b, 0x61, 0x30, 0)
				program[1] = 2
				data = append(data[:offset+4:offset+4], program...)
				binary.BigEndian.PutUint32(data[offset:], uint32(len(program)))
				wantErr = video.ErrUnsupportedMapping
			case "zero programs":
				program[1] = 0
			case "truncated entries":
				program[8] = 2
			case "truncated attributes":
				program[12] = 255
			case "short video coding info":
				program[12] = 1
			case "missing video aspect":
				program[12] = 2
			case "nonzero first start":
				binary.BigEndian.PutUint32(program[2:], 1)
				wantErr = video.ErrUnsupportedMapping
			case "duplicate matching last", "duplicate mismatching last", "duplicate identical":
				program = append(program[:17:17], program[10:17]...)
				program[8] = 2
				if mode == "duplicate matching last" {
					program[13] = 0x1b
				} else if mode == "duplicate mismatching last" {
					program[20] = 0x1b
				}
				data = append(data[:offset+4:offset+4], program...)
				binary.BigEndian.PutUint32(data[offset:], uint32(len(program)))
				wantErr = video.ErrUnsupportedMapping
			}
			if err := os.WriteFile(path, data, 0644); err != nil {
				t.Fatal(err)
			}
			cfg := bdinfo.DefaultSettings(".")
			cfg.PlaylistOnly = "00000.MPLS"
			cfg.FilterLoopingPlaylists, cfg.FilterShortPlaylists = false, false
			base, err := bdinfo.Run(t.Context(), bdinfo.Options{Path: root, Settings: cfg})
			if err != nil {
				t.Fatal(err)
			}
			r := &recorder{}
			res, err := bdinfo.Run(t.Context(), bdinfo.Options{Path: root, Settings: cfg, VideoConsumer: func(context.Context, video.StreamInfo) (video.Consumer, error) { return r, nil }})
			if err != nil {
				t.Fatal(err)
			}
			if res.Report != base.Report || res.QuickSummary != base.QuickSummary || res.ForumsBlock != base.ForumsBlock || len(res.Collection) != 1 || len(r.ends) != 1 {
				t.Fatalf("program context changed report/collection: %+v", res.Collection)
			}
			if len(res.Timelines) != 1 || res.Timelines[0].Complete || !errors.Is(res.Timelines[0].Err, wantErr) || !errors.Is(res.Collection[0].Info.Occurrences[0].AssociationError, wantErr) {
				t.Fatalf("unverified program context accepted: %+v", res.Timelines)
			}
		})
	}
}

func TestTimelineVideoRegionsUseDeclaredLength(t *testing.T) {
	for _, codec := range []video.Codec{video.MPEG1, video.MPEG2, video.AVC, video.MVC, video.HEVC, video.VC1} {
		for _, table := range []string{"CLPI", "MPLS"} {
			if table == "MPLS" && codec == video.MVC {
				continue // MVC playlist attributes are not covered by this format check.
			}
			minimum := 3 // CLPI codec, format/rate, and aspect fields reused by the report parser.
			if table == "MPLS" {
				minimum = 2 // Classic video carries codec and format/rate.
				if codec == video.HEVC {
					minimum = 4 // HEVC adds dynamic-range/color and flags.
				}
			}
			for _, length := range []int{1, 2, 3, 4} {
				t.Run(fmt.Sprintf("%s/%#x/length%d", table, codec, length), func(t *testing.T) {
					root := discFixture(t)
					clpiPath := filepath.Join(root, "BDMV", "CLIPINF", "00001.clpi")
					clpi, err := os.ReadFile(clpiPath)
					if err != nil {
						t.Fatal(err)
					}
					program := int(binary.BigEndian.Uint32(clpi[12:])) + 4
					clpi[program+13] = byte(codec)
					if table == "CLPI" {
						clpi[program+12] = byte(length)
					}
					if err := os.WriteFile(clpiPath, clpi, 0644); err != nil {
						t.Fatal(err)
					}
					playlistPath := filepath.Join(root, "BDMV", "PLAYLIST", "00000.mpls")
					playlist, err := os.ReadFile(playlistPath)
					if err != nil {
						t.Fatal(err)
					}
					attributes := []byte{4, byte(codec), 0x61, 0x30, 0}
					if table == "MPLS" {
						attributes[0] = byte(length)
					}
					playlist = bytes.ReplaceAll(playlist, []byte{4, 0x24, 0x61, 0x30, 0}, attributes)
					if err := os.WriteFile(playlistPath, playlist, 0644); err != nil {
						t.Fatal(err)
					}
					cfg := bdinfo.DefaultSettings(".")
					cfg.PlaylistOnly = "00000.MPLS"
					result, err := bdinfo.DiscoverPlaylists(t.Context(), bdinfo.Options{Path: root, Settings: cfg, IncludeTimeline: true})
					if err != nil {
						t.Fatal(err)
					}
					if len(result.Timelines) != 1 || result.Timelines[0].Complete != (length >= minimum) {
						t.Fatalf("length %d minimum %d: %+v", length, minimum, result.Timelines)
					}
					if length < minimum && !errors.Is(result.Timelines[0].Err, video.ErrIncomplete) {
						t.Fatalf("missing incomplete error: %v", result.Timelines[0].Err)
					}
				})
			}
		}
	}
}

func TestConcurrentClipsHaveIndependentConsumers(t *testing.T) {
	t.Run("complete", func(t *testing.T) { testConcurrentClips(t, false) })
	t.Run("canceled", func(t *testing.T) { testConcurrentClips(t, true) })
}

func testConcurrentClips(t *testing.T, canceled bool) {
	t.Helper()
	t.Setenv("BDINFO_WORKERS", "2")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	root := discFixture(t)
	for _, name := range []string{"CLIPINF/00001.clpi", "STREAM/00001.m2ts", "PLAYLIST/00001.mpls"} {
		data, err := os.ReadFile(filepath.Join(root, "BDMV", filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		newName := strings.ReplaceAll(name, "00001", "00002")
		if strings.HasPrefix(name, "PLAYLIST/") {
			data = bytes.ReplaceAll(data, []byte("00001"), []byte("00002"))
		}
		if err := os.WriteFile(filepath.Join(root, "BDMV", filepath.FromSlash(newName)), data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	cfg := bdinfo.DefaultSettings(".")
	cfg.FilterLoopingPlaylists = false
	cfg.FilterShortPlaylists = false
	started := make(chan video.StreamInfo, 2)
	consumers := make(chan *recorder, 2)
	release := make(chan struct{})
	finished := make(chan struct {
		result bdinfo.Result
		err    error
	}, 1)
	go func() {
		res, err := bdinfo.Run(ctx, bdinfo.Options{Path: root, Settings: cfg, VideoConsumer: func(ctx context.Context, info video.StreamInfo) (video.Consumer, error) {
			r := &recorder{}
			consumers <- r
			started <- info
			select {
			case <-release:
				return r, nil
			case <-ctx.Done():
				// Ownership transfers even when the factory observes cancellation.
				return r, nil
			}
		}})
		finished <- struct {
			result bdinfo.Result
			err    error
		}{res, err}
	}()
	first, second := <-started, <-started
	if first.Source == second.Source {
		t.Fatal("factory duplicated one physical key")
	}
	if canceled {
		cancel()
	} else {
		close(release)
	}
	done := <-finished
	if canceled {
		if !errors.Is(done.err, context.Canceled) || done.result.Report != "" || done.result.Timelines != nil {
			t.Fatalf("canceled result %+v err %v", done.result, done.err)
		}
		for range 2 {
			r := <-consumers
			if len(r.ends) != 1 || r.ends[0].CleanEOF || !errors.Is(r.ends[0].Err, context.Canceled) {
				t.Fatalf("canceled end %+v", r.ends)
			}
		}
		return
	}
	if done.err != nil {
		t.Fatal(done.err)
	}
	if len(done.result.Collection) != 2 {
		t.Fatalf("collection %+v", done.result.Collection)
	}
	for _, r := range done.result.Collection {
		if r.Status != video.Complete || r.DeliveredBytes != 6 {
			t.Fatal(r)
		}
	}
}
