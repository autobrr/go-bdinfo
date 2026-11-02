// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bdinfo_test

import (
	"context"
	"fmt"

	"github.com/autobrr/go-bdinfo/pkg/bdinfo"
	"github.com/autobrr/go-bdinfo/pkg/bdinfo/video"
)

// byteCounter is an example consumer with constant retained memory. A real
// metadata collector would feed these borrowed batches to its shared HEVC parser.
type byteCounter struct{ bytes uint64 }

func (c *byteCounter) Consume(ctx context.Context, chunk video.Chunk) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.bytes += uint64(len(chunk.Data))
	return nil
}
func (c *byteCounter) Discontinuity(ctx context.Context, _ video.Discontinuity) error {
	return ctx.Err()
}
func (c *byteCounter) Finish(video.End) error { return nil }

func ExampleOptions_VideoConsumer() {
	cfg := bdinfo.DefaultSettings(".")
	cfg.PlaylistOnly = "00800.MPLS"
	result, err := bdinfo.Run(context.Background(), bdinfo.Options{
		Path: `D:\Discs\Film`, Settings: cfg,
		VideoConsumer: func(ctx context.Context, info video.StreamInfo) (video.Consumer, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if info.Codec != video.HEVC {
				return nil, nil
			}
			for _, occurrence := range info.Occurrences {
				if occurrence.Angle == 0 && occurrence.Mapping.Role == video.Primary {
					return &byteCounter{}, nil
				}
			}
			return nil, nil
		},
	})
	if err != nil {
		fmt.Println(err)
		return
	}
	// A valid report can coexist with failed optional collection. Validate required
	// source outcomes and timeline associations before assembling any frame metadata.
	for _, source := range result.Collection {
		if source.Status != video.Declined && source.Status != video.Complete {
			fmt.Printf("%s PID %#x: %v\n", source.Info.Source.Path, source.Info.PID, source.Err)
			return
		}
	}
	for _, timeline := range result.Timelines {
		if !timeline.Complete {
			fmt.Println(timeline.Err)
			return
		}
		for _, item := range timeline.Items {
			fmt.Printf("%s item %d: [%d,%d), playlist offset %d (45 kHz)\n", timeline.Name, item.Index, item.In45, item.Out45, item.Offset45)
		}
	}
	// The caller's decoder must separately validate frame association, inheritance,
	// metadata support and exact trims. Transport completion alone proves none of them.
}
