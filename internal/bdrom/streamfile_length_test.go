package bdrom

import "testing"

func TestStreamFileLengthUsesDTSOnly(t *testing.T) {
	s := &StreamFile{}
	state := &streamState{}
	states := map[uint16]*streamState{0x1011: state}

	first := uint64(0)
	last := uint64(0)

	// First DTS-bearing timestamp: should not set length (BDInfo skips the first timestamp).
	s.handleTimestamp(nil, nil, nil, states, 0x1011, state, 1000, 1000, true, &first, &last)
	if s.Length != 0 {
		t.Fatalf("expected length 0 after first timestamp, got %v", s.Length)
	}

	// Second DTS-bearing timestamp: duration baseline begins here.
	s.handleTimestamp(nil, nil, nil, states, 0x1011, state, 2000, 2000, true, &first, &last)
	if s.Length != 0 {
		t.Fatalf("expected length 0 after second timestamp (first==last), got %v", s.Length)
	}

	// PTS-only timestamp advances presentation time but must not extend TSStreamFile.Length.
	// dtsForLength remains the last seen DTS.
	s.handleTimestamp(nil, nil, nil, states, 0x1011, state, 3000, 2000, true, &first, &last)
	if s.Length != 0 {
		t.Fatalf("expected length 0 after PTS-only timestamp, got %v", s.Length)
	}

	// Next DTS-bearing timestamp extends the file duration.
	s.handleTimestamp(nil, nil, nil, states, 0x1011, state, 5000, 5000, true, &first, &last)
	want := float64(5000-2000) / 90000.0
	if s.Length != want {
		t.Fatalf("expected length %v, got %v", want, s.Length)
	}
}
