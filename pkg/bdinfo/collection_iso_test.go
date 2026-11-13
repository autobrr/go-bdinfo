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

	"github.com/autobrr/go-bdinfo/internal/fs/udf"
	"github.com/autobrr/go-bdinfo/pkg/bdinfo"
	"github.com/autobrr/go-bdinfo/pkg/bdinfo/video"
)

// A tiny generated UDF image exercises the real ReadAt/extent reader, including
// its legal data-with-EOF return. No private media or external ISO tool is needed.
func fixtureISO(t *testing.T, root string) string {
	t.Helper()
	const sector = 2048
	image := make([]byte, 320*sector)
	put := func(block int, v any, extra []byte) {
		t.Helper()
		var b bytes.Buffer
		if err := binary.Write(&b, binary.LittleEndian, v); err != nil {
			t.Fatal(err)
		}
		b.Write(extra)
		if b.Len() > sector {
			t.Fatal("fixture block overflow")
		}
		copy(image[block*sector:], b.Bytes())
	}
	copy(image[16*sector+1:], "NSR03")
	put(256, udf.AnchorVolumeDescriptorPointer{DescriptorTag: udf.Tag{TagIdentifier: udf.TagAnchorVolume}, MainVolumeDescriptorSequenceExtent: udf.ExtentAD{Location: 257, Length: sector * 3}}, nil)
	put(257, udf.PartitionDescriptor{DescriptorTag: udf.Tag{TagIdentifier: udf.TagPartition}, PartitionStartingLocation: 300, PartitionLength: 20}, nil)
	lvd := udf.LogicalVolumeDescriptor{DescriptorTag: udf.Tag{TagIdentifier: udf.TagLogicalVolume}, LogicalBlockSize: sector, MapTableLength: 6, NumberOfPartitionMaps: 1}
	var contents bytes.Buffer
	_ = binary.Write(&contents, binary.LittleEndian, udf.LongAD{ExtentLength: sector, ExtentLocation: udf.LBAddr{LogicalBlockNumber: 0}})
	copy(lvd.LogicalVolumeContentsUse[:], contents.Bytes())
	put(258, lvd, []byte{1, 6, 1, 0, 0, 0})
	put(259, udf.Tag{TagIdentifier: udf.TagTerminating}, nil)
	put(300, udf.FileSetDescriptor{DescriptorTag: udf.Tag{TagIdentifier: udf.TagFileSet}, RootDirectoryICB: udf.LongAD{ExtentLength: sector, ExtentLocation: udf.LBAddr{LogicalBlockNumber: 1}}}, nil)
	type node struct {
		name     string
		block    uint32
		dir      bool
		data     []byte
		children []int
	}
	nodes := []node{{block: 1, dir: true, children: []int{1}}, {name: "BDMV", block: 2, dir: true, children: []int{2, 3, 4}}, {name: "PLAYLIST", block: 3, dir: true, children: []int{5, 6}}, {name: "CLIPINF", block: 4, dir: true, children: []int{7}}, {name: "STREAM", block: 5, dir: true, children: []int{8}}, {name: "00000.mpls", block: 6}, {name: "00001.mpls", block: 7}, {name: "00001.clpi", block: 8}, {name: "00001.m2ts", block: 9}}
	for i := range nodes {
		n := &nodes[i]
		if n.dir {
			for _, child := range n.children {
				c := nodes[child]
				name := append([]byte{8}, []byte(c.name)...)
				fid := make([]byte, (38+len(name)+3)&^3)
				binary.LittleEndian.PutUint16(fid, udf.TagFileIdentifier)
				binary.LittleEndian.PutUint16(fid[16:], 1)
				if c.dir {
					fid[18] = 2
				}
				fid[19] = byte(len(name))
				binary.LittleEndian.PutUint32(fid[20:], sector)
				binary.LittleEndian.PutUint32(fid[24:], c.block)
				copy(fid[38:], name)
				n.data = append(n.data, fid...)
			}
		} else {
			parent := "PLAYLIST"
			if i == 7 {
				parent = "CLIPINF"
			}
			if i == 8 {
				parent = "STREAM"
			}
			var err error
			n.data, err = os.ReadFile(filepath.Join(root, "BDMV", parent, n.name))
			if err != nil {
				t.Fatal(err)
			}
		}
		fileType := uint8(5)
		if n.dir {
			fileType = 4
		}
		if n.dir {
			put(300+int(n.block), udf.FileEntry{DescriptorTag: udf.Tag{TagIdentifier: udf.TagFile}, ICBTag: udf.ICBTag{Flags: 3, FileType: fileType}, InformationLength: uint64(len(n.data)), LengthOfAllocationDescriptors: uint32(len(n.data))}, n.data)
		} else {
			dataBlock := uint32(10 + i - 5)
			var ad bytes.Buffer
			_ = binary.Write(&ad, binary.LittleEndian, udf.ShortAD{ExtentLength: uint32(len(n.data)), ExtentPosition: dataBlock})
			put(300+int(n.block), udf.FileEntry{DescriptorTag: udf.Tag{TagIdentifier: udf.TagFile}, ICBTag: udf.ICBTag{FileType: fileType}, InformationLength: uint64(len(n.data)), LengthOfAllocationDescriptors: 8}, ad.Bytes())
			copy(image[(300+int(dataBlock))*sector:], n.data)
		}
	}
	path := filepath.Join(t.TempDir(), "fixture.iso")
	if err := os.WriteFile(path, image, 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCollectionFolderAndUDFISO(t *testing.T) {
	folder := filepath.Join(t.TempDir(), "bDmV", "\u023f", "Film")
	writeDiscFixture(t, folder)
	iso := fixtureISO(t, folder)
	cfg := bdinfo.DefaultSettings(".")
	cfg.FilterLoopingPlaylists = false
	cfg.FilterShortPlaylists = false
	var previous []byte
	var previousSource video.Source
	for _, path := range []string{folder, iso} {
		r := &recorder{}
		res, err := bdinfo.Run(t.Context(), bdinfo.Options{Path: path, Settings: cfg, VideoConsumer: func(context.Context, video.StreamInfo) (video.Consumer, error) { return r, nil }})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Collection) != 1 || res.Collection[0].Status != video.Complete || len(res.Timelines) != 2 || !res.Timelines[0].Complete {
			t.Fatalf("path %s collection %+v timeline %+v scan %+v", path, res.Collection, res.Timelines, res.Scan)
		}
		if previous != nil && !bytes.Equal(previous, r.data) {
			t.Fatal("folder/ISO ES mismatch")
		}
		if previous != nil && previousSource != res.Collection[0].Info.Source {
			t.Fatalf("folder/ISO source mismatch: %+v / %+v", previousSource, res.Collection[0].Info.Source)
		}
		previous = r.data
		previousSource = res.Collection[0].Info.Source
	}
	// Run closes the ISO before returning, allowing its removal on Windows.
	if err := os.Remove(iso); err != nil {
		t.Fatalf("ISO remained open: %v", err)
	}
}
