package bdrom

import "testing"

func FuzzParsePTSAndValidateTimestamp(f *testing.F) {
	f.Add([]byte{0x21, 0x00, 0x01, 0x00, 0x01})
	f.Add([]byte{0x31, 0xff, 0xff, 0xff, 0xff})
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) >= 5 {
			_ = parsePTS(data[:5])
			_ = validTimestamp(data[:5], 0x20)
			_ = validTimestamp(data[:5], 0x30)
			_ = validTimestamp(data[:5], 0x10)
		} else {
			_ = parsePTS(data)
			_ = validTimestamp(data, 0x20)
		}
	})
}
