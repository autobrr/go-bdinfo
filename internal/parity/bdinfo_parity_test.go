package parity

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParity_OfficialBDInfo_ReportText(t *testing.T) {
	if os.Getenv("BDINFO_PARITY") != "1" {
		t.Skip("set BDINFO_PARITY=1 to enable slow parity test")
	}

	disc := os.Getenv("BDINFO_PARITY_DISC")
	if disc == "" {
		// Convenience default for this workspace; skipped if absent.
		const candidate = "/mnt/storage/torrents/Network.1976.1080p.USA.Blu-ray.AVC.LPCM.1.0-TMT"
		if _, err := os.Stat(candidate); err == nil {
			disc = candidate
		}
	}
	if disc == "" {
		t.Skip("set BDINFO_PARITY_DISC=/path/to/disc")
	}
	if _, err := os.Stat(disc); err != nil {
		t.Skipf("disc path missing: %s", disc)
	}

	official := os.Getenv("BDINFO_OFFICIAL_BIN")
	officialReport := os.Getenv("BDINFO_OFFICIAL_REPORT")
	if official == "" {
		const candidate = "/root/github/oss/bdinfo-official/bdinfo_linux_v2.0.5_extracted/BDInfo"
		if _, err := os.Stat(candidate); err == nil {
			official = candidate
		}
	}

	repoRoot, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// internal/parity -> repo root
	repoRoot = filepath.Clean(filepath.Join(repoRoot, "../.."))

	tmp := t.TempDir()
	officialOut := filepath.Join(tmp, "official.txt")
	oursOut := filepath.Join(tmp, "ours.txt")
	oursBin := filepath.Join(tmp, "bdinfo")

	var officialBytes []byte
	if officialReport != "" {
		var err error
		officialBytes, err = os.ReadFile(officialReport)
		if err != nil {
			t.Fatalf("read official report: %v", err)
		}
	} else {
		if official == "" {
			t.Skip("set BDINFO_OFFICIAL_BIN=/path/to/official/BDInfo or BDINFO_OFFICIAL_REPORT=/path/to/report.txt")
		}
		// Run official BDInfo.
		// NOTE: official requires explicit bool values (e.g. `-m true`).
		offArgs := []string{
			"-p", disc,
			"-o", officialOut,
			// Stabilize toggles: match go-bdinfo defaults.
			"-b", "true",
			"-y", "true",
			"-v", "20",
			"-l", "false",
			"-k", "false",
			"-g", "false",
			"-e", "false",
			"-j", "false",
			"-m", "true",
			"-q", "true",
		}
		if err := runCmd(t, "", official, offArgs...); err != nil {
			t.Fatalf("official failed: %v", err)
		}
		var err error
		officialBytes, err = os.ReadFile(officialOut)
		if err != nil {
			t.Fatalf("read official output: %v", err)
		}
	}

	// Build once (faster than `go run` and avoids hitting `go test`'s default timeout).
	if err := runCmd(t, repoRoot, "go", "build", "-o", oursBin, "./cmd/bdinfo"); err != nil {
		t.Fatalf("build ours failed: %v", err)
	}

	// Run go-bdinfo.
	oursArgs := []string{
		"-p", disc,
		"-o", oursOut,
		"--enablessif=true",
		"--filtershortplaylist=true",
		"--filtershortplaylistvalue=20",
		"--filterloopingplaylists=false",
		"--keepstreamorder=false",
		"--generatestreamdiagnostics=false",
		"--extendedstreamdiagnostics=false",
		"--groupbytime=false",
		"--generatetextsummary=true",
		"--includeversionandnotes=true",
	}
	if err := runCmd(t, "", oursBin, oursArgs...); err != nil {
		t.Fatalf("ours failed: %v", err)
	}
	oursBytes, err := os.ReadFile(oursOut)
	if err != nil {
		t.Fatalf("read ours output: %v", err)
	}

	offNorm := normalizeReport(string(officialBytes))
	oursNorm := normalizeReport(string(oursBytes))

	if offNorm != oursNorm {
		// Keep diff small; first mismatch context.
		offLines := strings.Split(offNorm, "\n")
		oursLines := strings.Split(oursNorm, "\n")
		n := len(offLines)
		if len(oursLines) < n {
			n = len(oursLines)
		}
		mismatch := -1
		for i := 0; i < n; i++ {
			if offLines[i] != oursLines[i] {
				mismatch = i
				break
			}
		}
		if mismatch == -1 && len(offLines) != len(oursLines) {
			mismatch = n
		}
		start := mismatch - 5
		if start < 0 {
			start = 0
		}
		end := mismatch + 5
		if end > len(offLines) {
			end = len(offLines)
		}
		if end2 := mismatch + 5; end2 > len(oursLines) {
			// cap to shorter
		}
		var b strings.Builder
		b.WriteString("report mismatch (normalized)\n")
		b.WriteString("context (official | ours):\n")
		for i := start; i < end && i < len(oursLines); i++ {
			if i == mismatch {
				b.WriteString(">> ")
			} else {
				b.WriteString("   ")
			}
			b.WriteString(offLines[i])
			b.WriteString("\n")
			b.WriteString("   ")
			b.WriteString(oursLines[i])
			b.WriteString("\n")
		}
		t.Fatalf("%s", b.String())
	}
}

func normalizeReport(s string) string {
	// Minimal fuzz: normalize line endings + trailing whitespace.
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t")
	}
	// Trim leading/trailing empty lines.
	for len(lines) > 0 && lines[0] == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

func runCmd(t *testing.T, dir, exe string, args ...string) error {
	t.Helper()
	cmd := exec.Command(exe, args...)
	cmd.Dir = dir
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	if err != nil {
		// Keep exact error output (often needed for diagnosis).
		t.Logf("cmd: %s %s", exe, strings.Join(args, " "))
		t.Logf("output:\n%s", buf.String())
	}
	return err
}
