package bdrom

import (
	"slices"
	"testing"
)

func TestClipTargetCursor_ActiveIndicesMatchBaseline(t *testing.T) {
	targets := []scanClipTarget{
		{clip: &StreamClip{TimeIn: 10, TimeOut: 20}},
		{clip: &StreamClip{TimeIn: 0, TimeOut: 5}},
		{clip: &StreamClip{TimeIn: 3, TimeOut: 8}},
		{clip: &StreamClip{TimeIn: 21, TimeOut: 30}},
		{clip: &StreamClip{TimeIn: 15, TimeOut: 25}},
	}

	cursor := newClipTargetCursor(targets)
	if cursor == nil {
		t.Fatal("expected cursor")
	}

	sequence := []float64{0, 1, 4, 6, 16, 22, 18, 31, 5}
	for _, streamTime := range sequence {
		got := append([]int(nil), cursor.activeIndices(streamTime)...)
		want := baselineClipTargetIndices(targets, streamTime)
		if !slices.Equal(got, want) {
			t.Fatalf("streamTime=%.3f activeIndices=%v want %v", streamTime, got, want)
		}
	}
}

func baselineClipTargetIndices(targets []scanClipTarget, streamTime float64) []int {
	out := make([]int, 0, len(targets))
	for i, target := range targets {
		clip := target.clip
		if streamTime != 0 && (streamTime < clip.TimeIn || streamTime > clip.TimeOut) {
			continue
		}
		out = append(out, i)
	}
	return out
}
