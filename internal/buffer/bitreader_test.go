package buffer

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestBitReader_ReadBits(t *testing.T) {
	// Test data: 0b11010010 0b01101110
	data := []byte{0xD2, 0x6E}
	br := NewBitReader(data)

	tests := []struct {
		name     string
		bits     int
		expected uint64
	}{
		{"Read 3 bits", 3, 0b110},
		{"Read 5 bits", 5, 0b10010},
		{"Read 4 bits", 4, 0b0110},
		{"Read 4 bits", 4, 0b1110},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := br.ReadBits(tt.bits)
			if !ok {
				t.Fatalf("ReadBits(%d) failed", tt.bits)
			}
			if got != tt.expected {
				t.Errorf("ReadBits(%d) = %b, want %b", tt.bits, got, tt.expected)
			}
		})
	}
}

func TestBitReader_ReadBytes(t *testing.T) {
	data := []byte{0x01, 0x02, 0x03, 0x04, 0x05}
	br := NewBitReader(data)

	// Read 2 bytes
	got, ok := br.ReadBytes(2)
	if !ok {
		t.Fatalf("ReadBytes(2) failed")
	}
	if !bytes.Equal(got, []byte{0x01, 0x02}) {
		t.Errorf("ReadBytes(2) = %v, want %v", got, []byte{0x01, 0x02})
	}

	// Read 3 more bytes
	got, ok = br.ReadBytes(3)
	if !ok {
		t.Fatalf("ReadBytes(3) failed")
	}
	if !bytes.Equal(got, []byte{0x03, 0x04, 0x05}) {
		t.Errorf("ReadBytes(3) = %v, want %v", got, []byte{0x03, 0x04, 0x05})
	}
}

func TestBitReader_ReadUInt16(t *testing.T) {
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint16(0x1234))

	br := NewBitReader(buf.Bytes())
	got, ok := br.ReadUInt16()
	if !ok {
		t.Fatalf("ReadUInt16() failed")
	}
	if got != 0x1234 {
		t.Errorf("ReadUInt16() = %x, want %x", got, 0x1234)
	}
}

func TestBitReader_ReadUInt32(t *testing.T) {
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(0x12345678))

	br := NewBitReader(buf.Bytes())
	got, ok := br.ReadUInt32()
	if !ok {
		t.Fatalf("ReadUInt32() failed")
	}
	if got != 0x12345678 {
		t.Errorf("ReadUInt32() = %x, want %x", got, 0x12345678)
	}
}

func TestBitReader_ReadExpGolomb(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		expected int
	}{
		{"Value 0", []byte{0x80}, 0}, // 1
		{"Value 1", []byte{0x40}, 1}, // 010
		{"Value 2", []byte{0x60}, 2}, // 011
		{"Value 3", []byte{0x20}, 3}, // 00100
		{"Value 4", []byte{0x28}, 4}, // 00101
		{"Value 7", []byte{0x10}, 7}, // 0001000
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			br := NewBitReader(tt.data)
			got, ok := br.ReadExpGolomb()
			if !ok {
				t.Fatalf("ReadExpGolomb() failed")
			}
			if got != tt.expected {
				t.Errorf("ReadExpGolomb() = %d, want %d", got, tt.expected)
			}
		})
	}
}

func TestBitReader_ReadSignedExpGolomb(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		expected int
	}{
		{"Value 0", []byte{0x80}, 0},   // 1
		{"Value 1", []byte{0x40}, 1},   // 010
		{"Value -1", []byte{0x60}, -1}, // 011
		{"Value 2", []byte{0x20}, 2},   // 00100
		{"Value -2", []byte{0x28}, -2}, // 00101
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			br := NewBitReader(tt.data)
			got, ok := br.ReadSignedExpGolomb()
			if !ok {
				t.Fatalf("ReadSignedExpGolomb() failed")
			}
			if got != tt.expected {
				t.Errorf("ReadSignedExpGolomb() = %d, want %d", got, tt.expected)
			}
		})
	}
}

func TestBitReader_Skip(t *testing.T) {
	data := []byte{0x01, 0x02, 0x03, 0x04, 0x05}
	br := NewBitReader(data)

	// Skip 2 bytes
	if !br.Skip(2) {
		t.Fatalf("Skip(2) failed")
	}

	// Read next byte
	got, ok := br.ReadByte()
	if !ok {
		t.Fatalf("ReadByte() failed")
	}
	if got != 0x03 {
		t.Errorf("ReadByte() after Skip(2) = %x, want %x", got, 0x03)
	}
}

func TestBitReader_MixedBitByteReading(t *testing.T) {
	// Test reading bits and bytes in mixed order
	data := []byte{0xFF, 0x00, 0xAA, 0x55}
	br := NewBitReader(data)

	// Read 4 bits (should get 0xF)
	bits, ok := br.ReadBits(4)
	if !ok {
		t.Fatalf("ReadBits(4) failed")
	}
	if bits != 0xF {
		t.Errorf("ReadBits(4) = %x, want %x", bits, 0xF)
	}

	// Read a byte (should get 0xF0 from remaining 4 bits of first byte + 4 bits of second byte)
	b, ok := br.ReadByte()
	if !ok {
		t.Fatalf("ReadByte() failed")
	}
	if b != 0xF0 {
		t.Errorf("ReadByte() after ReadBits(4) = %x, want %x", b, 0xF0)
	}

	// Align to byte boundary
	br.AlignByte()

	// Read next byte (should get 0xAA)
	b, ok = br.ReadByte()
	if !ok {
		t.Fatalf("ReadByte() after AlignByte() failed")
	}
	if b != 0xAA {
		t.Errorf("ReadByte() after AlignByte() = %x, want %x", b, 0xAA)
	}
}
