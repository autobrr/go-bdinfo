package buffer

// BitReader reads MSB-first bits from a byte slice.
type BitReader struct {
	data    []byte
	bytePos int
	bitPos  uint8
}

func NewBitReader(data []byte) *BitReader {
	return &BitReader{data: data}
}

func (r *BitReader) BytesLeft() int {
	if r.bytePos >= len(r.data) {
		return 0
	}
	return len(r.data) - r.bytePos
}

// Position returns the current byte position.
func (r *BitReader) Position() int {
	return r.bytePos
}

// BitPosition returns the current position in bits.
func (r *BitReader) BitPosition() int {
	return r.bytePos*8 + int(r.bitPos)
}

// Length returns total buffer length.
func (r *BitReader) Length() int {
	return len(r.data)
}

// BitsRemaining returns remaining bits in the buffer.
func (r *BitReader) BitsRemaining() int {
	remain := len(r.data)*8 - r.BitPosition()
	if remain < 0 {
		return 0
	}
	return remain
}

// SetBitPosition sets the current position in bits.
func (r *BitReader) SetBitPosition(bitPos int) bool {
	if bitPos < 0 || bitPos > len(r.data)*8 {
		return false
	}
	r.bytePos = bitPos / 8
	r.bitPos = uint8(bitPos % 8)
	return true
}

func (r *BitReader) ReadBit() (uint64, bool) {
	if r.bytePos >= len(r.data) {
		return 0, false
	}
	b := r.data[r.bytePos]
	bit := (b >> (7 - r.bitPos)) & 0x01
	r.bitPos++
	if r.bitPos == 8 {
		r.bitPos = 0
		r.bytePos++
	}
	return uint64(bit), true
}

func (r *BitReader) ReadBits(n int) (uint64, bool) {
	if n <= 0 {
		return 0, true
	}
	var v uint64
	for i := 0; i < n; i++ {
		bit, ok := r.ReadBit()
		if !ok {
			return 0, false
		}
		v = (v << 1) | bit
	}
	return v, true
}

func (r *BitReader) ReadByte() (byte, bool) {
	if r.bitPos == 0 {
		if r.bytePos >= len(r.data) {
			return 0, false
		}
		b := r.data[r.bytePos]
		r.bytePos++
		return b, true
	}
	val, ok := r.ReadBits(8)
	if !ok {
		return 0, false
	}
	return byte(val), true
}

// ReadBytes reads n bytes.
func (r *BitReader) ReadBytes(n int) ([]byte, bool) {
	if n <= 0 {
		return []byte{}, true
	}
	out := make([]byte, n)
	for i := 0; i < n; i++ {
		b, ok := r.ReadByte()
		if !ok {
			return nil, false
		}
		out[i] = b
	}
	return out, true
}

// ReadUInt16 reads a big-endian uint16.
func (r *BitReader) ReadUInt16() (uint16, bool) {
	b1, ok := r.ReadByte()
	if !ok {
		return 0, false
	}
	b2, ok := r.ReadByte()
	if !ok {
		return 0, false
	}
	return uint16(b1)<<8 | uint16(b2), true
}

// ReadUInt32 reads a big-endian uint32.
func (r *BitReader) ReadUInt32() (uint32, bool) {
	b1, ok := r.ReadByte()
	if !ok {
		return 0, false
	}
	b2, ok := r.ReadByte()
	if !ok {
		return 0, false
	}
	b3, ok := r.ReadByte()
	if !ok {
		return 0, false
	}
	b4, ok := r.ReadByte()
	if !ok {
		return 0, false
	}
	return uint32(b1)<<24 | uint32(b2)<<16 | uint32(b3)<<8 | uint32(b4), true
}

func (r *BitReader) SkipBits(n int) bool {
	_, ok := r.ReadBits(n)
	return ok
}

// AlignByte advances to the next byte boundary.
func (r *BitReader) AlignByte() {
	if r.bitPos == 0 {
		return
	}
	r.bitPos = 0
	r.bytePos++
}

// Skip skips n bytes.
func (r *BitReader) Skip(n int) bool {
	if n <= 0 {
		return true
	}
	for i := 0; i < n; i++ {
		if _, ok := r.ReadByte(); !ok {
			return false
		}
	}
	return true
}

// ReadUE reads an unsigned Exp-Golomb code.
func (r *BitReader) ReadUE() (uint64, bool) {
	zeros := 0
	for {
		bit, ok := r.ReadBit()
		if !ok {
			return 0, false
		}
		if bit == 0 {
			zeros++
			continue
		}
		break
	}
	if zeros == 0 {
		return 0, true
	}
	value, ok := r.ReadBits(zeros)
	if !ok {
		return 0, false
	}
	return (1<<zeros - 1) + value, true
}

// ReadSE reads a signed Exp-Golomb code.
func (r *BitReader) ReadSE() (int64, bool) {
	ue, ok := r.ReadUE()
	if !ok {
		return 0, false
	}
	k := int64(ue)
	if k%2 == 0 {
		return -(k / 2), true
	}
	return (k + 1) / 2, true
}

// ReadExpGolomb reads an unsigned Exp-Golomb code as int.
func (r *BitReader) ReadExpGolomb() (int, bool) {
	val, ok := r.ReadUE()
	if !ok {
		return 0, false
	}
	return int(val), true
}

// ReadSignedExpGolomb reads a signed Exp-Golomb code as int.
func (r *BitReader) ReadSignedExpGolomb() (int, bool) {
	val, ok := r.ReadSE()
	if !ok {
		return 0, false
	}
	return int(val), true
}
