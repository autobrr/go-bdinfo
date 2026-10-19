// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bdinfo_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/autobrr/go-bdinfo/pkg/bdinfo"
	"github.com/autobrr/go-bdinfo/pkg/bdinfo/video"
)

func TestOriginalPESHeaderDelivery(t *testing.T) {
	root := discFixture(t)
	headers := [][]byte{
		{0, 0, 1, 0xe0, 0, 0, 0x80, 0, 0},
		{0, 0, 1, 0xe0, 0, 0, 0x80, 0x88, 6, 0x21, 0, 1, 0, 1, 0x20},
	}
	var transport []byte
	for i, header := range headers {
		binary.BigEndian.PutUint16(header[4:6], uint16(len(header)-6+3))
		payload := append(append([]byte(nil), header...), 0xaa, 0xbb, 0xcc)
		packet := make([]byte, 192)
		packet[4], packet[5], packet[6], packet[7] = 0x47, 0x50, 0x11, 0x30|byte(i)
		packet[8] = byte(183 - len(payload))
		for j := 10; j < 192-len(payload); j++ {
			packet[j] = 0xff
		}
		copy(packet[192-len(payload):], payload)
		transport = append(transport, packet...)
	}
	if err := os.WriteFile(filepath.Join(root, "BDMV", "STREAM", "00001.m2ts"), transport, 0644); err != nil {
		t.Fatal(err)
	}
	cfg := bdinfo.DefaultSettings(".")
	cfg.FilterLoopingPlaylists, cfg.FilterShortPlaylists, cfg.BigPlaylistOnly = false, false, false
	r := &recorder{}
	out, err := bdinfo.Run(t.Context(), bdinfo.Options{Path: root, Settings: cfg, VideoConsumer: func(context.Context, video.StreamInfo) (video.Consumer, error) { return r, nil }})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.starts) != 2 || len(r.ends) != 1 || !r.ends[0].CleanEOF || len(out.Collection) != 1 {
		t.Fatalf("delivery: %+v %+v %+v", r.starts, r.ends, out.Collection)
	}
	for i, start := range r.starts {
		if !bytes.Equal(start.Header, headers[i]) {
			t.Fatalf("header%d changed/stripped: %x want %x", i, start.Header, headers[i])
		}
		if start.Offset != uint64(3*i) || start.PacketIndex != uint64(i) {
			t.Fatalf("coordinates: %+v", start)
		}
	}
}
