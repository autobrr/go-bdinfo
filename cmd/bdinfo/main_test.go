package main

import "testing"

func TestNormalizeArgs_BoolValueTokens(t *testing.T) {
	in := []string{"-m", "true", "-q", "false", "--enablessif", "TRUE", "--summaryonly", "false"}
	got := normalizeArgs(in)
	want := []string{
		"--generatetextsummary=true",
		"--includeversionandnotes=false",
		"--enablessif=true",
		"--summaryonly=false",
	}
	if len(got) != len(want) {
		t.Fatalf("len=%d want=%d; got=%q", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("idx=%d got=%q want=%q (all=%q)", i, got[i], want[i], got)
		}
	}
}
