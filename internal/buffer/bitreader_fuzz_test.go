package buffer

import "testing"

func FuzzBitReader(f *testing.F) {
	f.Add([]byte{0x00})
	f.Add([]byte{0xFF, 0x00, 0x00, 0x01})
	f.Add([]byte{0x00, 0x00, 0x03, 0x00, 0x01})

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			return
		}
		br := NewBitReader(data)
		if len(data) == 0 {
			_, _ = br.ReadBit()
			return
		}

		ops := int(data[0] & 0x3F)
		idx := 1
		for i := 0; i < ops; i++ {
			var b byte
			if idx < len(data) {
				b = data[idx]
				idx++
			}
			switch b % 8 {
			case 0:
				_, _ = br.ReadBits(int(b % 33))
			case 1:
				_, _ = br.ReadUE()
			case 2:
				_ = br.SkipBits(int(b % 33))
			case 3:
				_, _ = br.ReadUInt16()
			case 4:
				_, _ = br.ReadUInt32()
			case 5:
				br.AlignByte()
			case 6:
				if br.Length() > 0 {
					pos := int(b) % (br.Length() * 8)
					_ = br.SetBitPosition(pos)
				}
			case 7:
				_, _ = br.ReadByteValue()
			}
		}
	})
}
