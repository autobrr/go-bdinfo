package main

import (
	"testing"
	"time"
)

func TestNormalizeArgs_BoolValueTokens(t *testing.T) {
	in := []string{"-m", "true", "-q", "false", "--enablessif", "TRUE", "--summaryonly", "false", "--progress", "TRUE"}
	got := normalizeArgs(in)
	want := []string{
		"--generatetextsummary=true",
		"--includeversionandnotes=false",
		"--enablessif=true",
		"--summaryonly=false",
		"--progress=true",
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

func TestFormatPercent(t *testing.T) {
	if got := formatPercent(5, 10); got != "50.0%" {
		t.Fatalf("formatPercent got=%q", got)
	}
	if got := formatPercent(1, 0); got != "100.0%" {
		t.Fatalf("formatPercent total=0 got=%q", got)
	}
}

func TestFormatBytePercent(t *testing.T) {
	if got := formatBytePercent(25, 100); got != "25.0%" {
		t.Fatalf("formatBytePercent got=%q", got)
	}
	if got := formatBytePercent(100, 0); got != "100.0%" {
		t.Fatalf("formatBytePercent total=0 got=%q", got)
	}
}

func TestFormatETA(t *testing.T) {
	if got := formatETA(65 * time.Second); got != "01:05" {
		t.Fatalf("formatETA short got=%q", got)
	}
	if got := formatETA((1 * time.Hour) + (2 * time.Minute) + (3 * time.Second)); got != "1:02:03" {
		t.Fatalf("formatETA long got=%q", got)
	}
}

func TestFormatReadSpeed(t *testing.T) {
	if got := formatReadSpeed(0); got != "--" {
		t.Fatalf("formatReadSpeed zero got=%q", got)
	}
	if got := formatReadSpeed(1024 * 1024); got != "1.00 MB/s" {
		t.Fatalf("formatReadSpeed oneMB got=%q", got)
	}
}
