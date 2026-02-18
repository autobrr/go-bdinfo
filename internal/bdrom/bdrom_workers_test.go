package bdrom

import "testing"

func TestScanWorkerLimit_ISOStreamScanDefaultsToOneWorker(t *testing.T) {
	t.Setenv("BDINFO_WORKERS", "")

	if got, want := scanWorkerLimit(8, 90<<30), 1; got != want {
		t.Fatalf("scanWorkerLimit(iso stream)=%d want %d", got, want)
	}
}

func TestScanWorkerLimit_StreamScanDefaultsToOneWorker(t *testing.T) {
	t.Setenv("BDINFO_WORKERS", "")

	if got, want := scanWorkerLimit(8, 90<<30), 1; got != want {
		t.Fatalf("scanWorkerLimit(non-iso stream)=%d want %d", got, want)
	}
}

func TestScanWorkerLimit_MetadataScanUsesTunedLimit(t *testing.T) {
	t.Setenv("BDINFO_WORKERS", "")

	got := scanWorkerLimit(8, 0)
	want := clampWorkers(tunedWorkerLimit(8, 0), 8)
	if got != want {
		t.Fatalf("scanWorkerLimit(metadata)=%d want %d", got, want)
	}
}

func TestScanWorkerLimit_EnvOverrideWins(t *testing.T) {
	t.Setenv("BDINFO_WORKERS", "3")

	want := clampWorkers(3, 8)
	if got := scanWorkerLimit(8, 90<<30); got != want {
		t.Fatalf("scanWorkerLimit(env override)=%d want %d", got, want)
	}
}
