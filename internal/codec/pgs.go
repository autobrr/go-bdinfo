package codec

import (
	"github.com/autobrr/go-bdinfo/internal/buffer"
	"github.com/autobrr/go-bdinfo/internal/stream"
)

// ScanPGS ports BDInfo's TSCodecPGS.Scan. BDInfo runs it once per completed
// PES transfer and reads only the first segment of that transfer, so caption
// counts depend on how the disc splits segments across PES packets. Unlike
// BDInfo, the PCS object cropping fields are read only when the cropped flag is
// set, as the PGS format requires.
func ScanPGS(g *stream.GraphicsStream, data []byte) {
	r := buffer.NewBitReader(data)
	segmentType, _ := r.ReadByteValue()
	switch segmentType {
	case 0x15: // ODS: Object Definition Segment
		if g.LastFrame.Finished {
			return
		}
		if g.LastFrame.Forced {
			g.ForcedCaptions++
		} else {
			g.Captions++
		}
	case 0x16: // PCS: Presentation Composition Segment
		r.Skip(2) // segment size
		width, _ := r.ReadUInt16()
		height, _ := r.ReadUInt16()
		if !g.IsInitialized {
			g.Width, g.Height = int(width), int(height)
			g.IsInitialized = true
		}
		r.Skip(6) // frame rate, composition number, composition state, palette flag + ID
		n, _ := r.ReadByteValue()
		for range n {
			r.Skip(3) // object ID, window ID
			forced, _ := r.ReadByteValue()
			r.Skip(4) // horizontal and vertical position
			if forced&0x80 == 0x80 {
				r.Skip(8) // cropping rectangle, present only when the object is cropped
			}
			g.LastFrame = stream.PGSFrame{Forced: forced&0x40 == 0x40}
		}
	case 0x80: // END
		g.LastFrame.Finished = true
	}
}
