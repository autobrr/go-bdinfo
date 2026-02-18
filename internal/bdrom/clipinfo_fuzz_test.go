package bdrom

import (
	"bytes"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/autobrr/go-bdinfo/internal/fs"
)

type memFileInfoFuzz struct {
	name string
	data []byte
}

func (m *memFileInfoFuzz) Name() string       { return filepath.Base(m.name) }
func (m *memFileInfoFuzz) FullName() string   { return m.name }
func (m *memFileInfoFuzz) Length() int64      { return int64(len(m.data)) }
func (m *memFileInfoFuzz) Extension() string  { return filepath.Ext(m.name) }
func (m *memFileInfoFuzz) IsDirectory() bool  { return false }
func (m *memFileInfoFuzz) ModTime() time.Time { return time.Time{} }
func (m *memFileInfoFuzz) OpenRead() (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(m.data)), nil
}

var _ fs.FileInfo = (*memFileInfoFuzz)(nil)

func FuzzStreamClipFileScan(f *testing.F) {
	// Minimal valid-ish CLPI seed (enough to drive parsing without immediate "too short").
	seed := make([]byte, 0, 64)
	seed = append(seed, []byte("HDMV0100")...) // 0..7
	seed = append(seed, make([]byte, 12)...)   // 8..19
	// clipIndex at bytes 12..15 -> 0x00000014 (20)
	seed[12], seed[13], seed[14], seed[15] = 0x00, 0x00, 0x00, 0x14
	seed = append(seed, 0x00, 0x00, 0x00, 0x14) // clipLength=20
	clip := make([]byte, 20)
	clip[8] = 1 // streamCount
	clip[10], clip[11] = 0x10, 0x11
	clip[12] = 0 // stream info length byte (keeps offset small)
	clip[13] = byte(0x1B)
	seed = append(seed, clip...)
	f.Add(seed)
	f.Add([]byte("HDMV0100")) // short but non-empty

	f.Fuzz(func(t *testing.T, data []byte) {
		// Keep fuzzing bounded (avoid huge allocations from mutated corpora).
		if len(data) > 2<<20 {
			return
		}
		fi := &memFileInfoFuzz{name: "FUZZ.CLPI", data: data}
		clpi := NewStreamClipFile(fi)
		_ = clpi.Scan()
	})
}
