package bdrom

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync/atomic"
	"testing"

	"github.com/autobrr/go-bdinfo/internal/stream"
)

// cancelOnReadFileInfo cancels the context on the first chunk read of the scan
// loop, so the loop must notice the cancel before the next chunk read. The scan
// opens the file twice: the PMT order probe first, then the scan loop. Only the
// second open is counted, and only its multi-megabyte chunk reads.
type cancelOnReadFileInfo struct {
	memFileInfo
	cancel     context.CancelFunc
	opens      int
	chunkReads int
}

func (c *cancelOnReadFileInfo) OpenRead() (io.ReadCloser, error) {
	c.opens++
	return io.NopCloser(&cancelReader{r: bytes.NewReader(c.data), fi: c, scanLoop: c.opens == 2}), nil
}

type cancelReader struct {
	r        io.Reader
	fi       *cancelOnReadFileInfo
	scanLoop bool
}

func (c *cancelReader) Read(p []byte) (int, error) {
	if c.scanLoop && len(p) >= 1<<20 {
		c.fi.chunkReads++
		c.fi.cancel()
	}
	return c.r.Read(p)
}

func TestStreamFileScan_StopsReadingAfterCancel(t *testing.T) {
	const pid = uint16(0x1011)
	pkt := tsPacket188(pid, false, make([]byte, 184))
	var data []byte
	for range 8 {
		data = append(data, pkt[:]...)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fi := &cancelOnReadFileInfo{memFileInfo: memFileInfo{name: "TEST.M2TS", data: data}, cancel: cancel}

	s := NewStreamFile(fi)
	s.Streams[pid] = &stream.VideoStream{Stream: stream.Stream{PID: pid, StreamType: stream.StreamTypeAVCVideo}}

	err := s.ScanWithProgress(ctx, nil, false, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ScanWithProgress() error = %v, want context.Canceled", err)
	}
	if fi.chunkReads != 1 {
		t.Fatalf("chunk reads = %d, want 1 (scan kept reading after cancel)", fi.chunkReads)
	}
}

func TestRunParallel_SkipsItemsAfterCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var calls atomic.Int32
	runParallel(ctx, []int{1, 2, 3}, 2, func(int) error {
		calls.Add(1)
		return nil
	}, nil, nil)
	if calls.Load() != 0 {
		t.Fatalf("fn called %d times after cancel, want 0", calls.Load())
	}
}

func TestScan_CanceledContextReturnsScanError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	rom := testEpilogueBDROM()
	result := rom.scanWithProgress(ctx, nil, true)
	if !errors.Is(result.ScanError, context.Canceled) {
		t.Fatalf("ScanError = %v, want context.Canceled", result.ScanError)
	}
}
