package bdrom

import (
	"bytes"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/autobrr/go-bdinfo/internal/fs"
	"github.com/autobrr/go-bdinfo/internal/stream"
)

type memFileInfo struct {
	name string
	data []byte
}

func (m *memFileInfo) Name() string       { return filepath.Base(m.name) }
func (m *memFileInfo) FullName() string   { return m.name }
func (m *memFileInfo) Length() int64      { return int64(len(m.data)) }
func (m *memFileInfo) Extension() string  { return filepath.Ext(m.name) }
func (m *memFileInfo) IsDirectory() bool  { return false }
func (m *memFileInfo) ModTime() time.Time { return time.Time{} }
func (m *memFileInfo) OpenRead() (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(m.data)), nil
}

var _ fs.FileInfo = (*memFileInfo)(nil)

func encodePTS(prefix byte, pts uint64) [5]byte {
	// Standard PES timestamp packing with marker bits set.
	return [5]byte{
		prefix | byte((pts>>29)&0x0E) | 0x01,
		byte(pts >> 22),
		byte((pts>>14)&0xFE) | 0x01,
		byte(pts >> 7),
		byte((pts<<1)&0xFE) | 0x01,
	}
}

func tsPacket188(pid uint16, payloadStart bool, payload []byte) [188]byte {
	var pkt [188]byte
	pkt[0] = 0x47
	pkt[1] = byte((pid >> 8) & 0x1F)
	if payloadStart {
		pkt[1] |= 0x40
	}
	pkt[2] = byte(pid & 0xFF)
	pkt[3] = 0x10 // payload only

	// Payload is 184 bytes.
	copy(pkt[4:], payload)
	return pkt
}

func TestStreamFileScanLengthSingleTimestampIsZero(t *testing.T) {
	const pid = 0x1011

	// Build a minimal PES header with PTS+DTS (one timestamp total).
	pts := uint64(90000)
	dts := uint64(90000)
	pts5 := encodePTS(0x30, pts)
	dts5 := encodePTS(0x10, dts)

	pes := make([]byte, 19)
	pes[0], pes[1], pes[2] = 0x00, 0x00, 0x01
	pes[3] = 0xE0 // video stream_id
	pes[4], pes[5] = 0x00, 0x00
	pes[6] = 0x80
	pes[7] = 0xC0
	pes[8] = 0x0A
	copy(pes[9:14], pts5[:])
	copy(pes[14:19], dts5[:])

	payload1 := make([]byte, 184)
	copy(payload1, pes)
	// Include an SPS-like prefix that would initialize the AVC codec if the analyzer ran.
	copy(payload1[len(pes):], []byte{0x00, 0x00, 0x01, 0x67, 0x64, 0x00, 0x28})
	payload2 := make([]byte, 184) // continuation; no new PES start

	p1 := tsPacket188(pid, true, payload1)
	p2 := tsPacket188(pid, false, payload2)

	data := append(p1[:], p2[:]...)
	fi := &memFileInfo{name: "TEST.M2TS", data: data}

	s := NewStreamFile(fi)
	vs := &stream.VideoStream{Stream: stream.Stream{PID: pid, StreamType: stream.StreamTypeAVCVideo}}
	s.Streams[pid] = vs

	// Ensure Scan resets Length and does not seed it from playlist clips.
	s.Length = 123.0
	if err := s.Scan(nil, false); err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	if s.Length != 0 {
		t.Fatalf("expected length 0 for single timestamp, got %v", s.Length)
	}
	if vs.IsVBR || vs.IsInitialized || vs.EncodingProfile != "" {
		t.Fatalf("expected codec uninitialized for single unterminated transfer: IsVBR=%v IsInitialized=%v EncodingProfile=%q", vs.IsVBR, vs.IsInitialized, vs.EncodingProfile)
	}
}
