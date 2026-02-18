package bdrom

import "testing"

func TestScanWorkerLimit_ISOStreamScanDefaultsToOneWorker(t *testing.T) {
	t.Setenv("BDINFO_WORKERS", "")

	if got, want := scanWorkerLimit(8, 90<<30, true), 1; got != want {
		t.Fatalf("scanWorkerLimit(iso stream)=%d want %d", got, want)
	}
}

func TestScanWorkerLimit_NonISOUsesTunedLimit(t *testing.T) {
	t.Setenv("BDINFO_WORKERS", "")

	got := scanWorkerLimit(8, 90<<30, false)
	want := clampWorkers(tunedWorkerLimit(8, 90<<30), 8)
	if got != want {
		t.Fatalf("scanWorkerLimit(non-iso)=%d want %d", got, want)
	}
}

func TestScanWorkerLimit_EnvOverrideWins(t *testing.T) {
	t.Setenv("BDINFO_WORKERS", "3")

	want := clampWorkers(3, 8)
	if got := scanWorkerLimit(8, 90<<30, true); got != want {
		t.Fatalf("scanWorkerLimit(env override)=%d want %d", got, want)
	}
}
