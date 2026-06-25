package codec

import "bytes"

// applyHEVCNAL applies a single Annex-B NAL (without its start code) to the tag
// state, mirroring the per-NAL body of HEVCFrameTagFromTransfer exactly:
//   - validates the 2-byte NAL header (forbidden_zero_bit==0, temporal_id_plus1!=0)
//   - SPS (type 33) / PPS (type 34) update state
//   - any other type is run through parseHEVCSliceTag (which returns "" for
//     non-slice types)
//
// It returns the slice tag, whether the NAL hit the default (non-SPS/PPS) branch,
// and whether the NAL header was valid at all. Callers replicate the batch
// function's initialized/uninitialized tag selection from these. Equivalence to the
// batch function's inline NAL handling is enforced by FuzzHEVCTagScannerEquivalence.
func applyHEVCNAL(state *HEVCTagState, nal []byte) (tag string, isDefault bool, valid bool) {
	if len(nal) < 3 {
		return "", false, false
	}
	if (nal[0]&0x80) != 0 || (nal[1]&0x07) == 0 {
		return "", false, false
	}
	switch nalUnitType := (nal[0] >> 1) & 0x3F; nalUnitType {
	case 33: // SPS
		parseHEVCSPS(state, nal)
		return "", false, true
	case 34: // PPS
		parseHEVCPPS(state, nal)
		return "", false, true
	default:
		return parseHEVCSliceTag(state, nal, nalUnitType), true, true
	}
}

// peekHEVCSliceTag parses a still-growing trailing NAL as a slice WITHOUT mutating
// state, so the scanner can resolve the common single-slice access unit (whose slice
// is the last NAL, with no trailing start code) before the whole 64KB buffer is
// copied. A non-null result is final: it depends only on the slice header at the
// start of the NAL, so trailing bytes cannot change it (if the header is incomplete,
// parseHEVCSliceTag returns "" and the caller waits for more bytes). SPS/PPS
// (nal types 33/34) mutate state and so are deliberately never peeked — they are
// only applied once fully delimited or at finalize.
func peekHEVCSliceTag(state *HEVCTagState, nal []byte) string {
	if len(nal) < 3 {
		return ""
	}
	if (nal[0]&0x80) != 0 || (nal[1]&0x07) == 0 {
		return ""
	}
	nalUnitType := (nal[0] >> 1) & 0x3F
	if nalUnitType == 33 || nalUnitType == 34 {
		return ""
	}
	return parseHEVCSliceTag(state, nal, nalUnitType)
}

// nextStartCodeResume is a stateful variant of nextStartCode that searches for the
// next Annex-B start code from searchStart while classifying the 3-byte vs 4-byte
// form relative to canonStart. It returns the same (index, length) as
// nextStartCode(data, canonStart) PROVIDED the caller guarantees no start code
// exists in [canonStart, searchStart) — the incremental scanner upholds this by only
// advancing searchStart across regions a prior search already proved empty. This
// lets the scanner avoid re-scanning bytes it has already passed (O(n) over a
// growing buffer instead of O(n^2)). nextStartCodeResume(data, s, s) is identical to
// nextStartCode(data, s); FuzzNextStartCodeResumeEquivalence pins that.
func nextStartCodeResume(data []byte, searchStart, canonStart int) (int, int) {
	if searchStart < canonStart {
		searchStart = canonStart
	}
	if searchStart < 0 {
		searchStart = 0
	}
	rel := bytes.Index(data[searchStart:], startCode3)
	if rel < 0 {
		return -1, 0
	}
	p := searchStart + rel
	if p-1 >= canonStart && data[p-1] == 0x00 {
		return p - 1, 4
	}
	if p+3 < len(data) {
		return p, 3
	}
	return -1, 0
}

// resumeOffset returns where to resume a start-code search after a search over a
// buffer of length n (relative to canonStart) found nothing. It backs up 3 bytes
// from the buffer end so a start code that straddles the previous buffer boundary
// (or one that was excluded only by the 3-byte end bound) is re-examined once more
// bytes arrive, while never going below canonStart.
func resumeOffset(n, canonStart int) int {
	if r := n - 3; r > canonStart {
		return r
	}
	return canonStart
}

// HEVCTagScanner incrementally derives the per-transfer HEVC frame tag from a
// growing Annex-B buffer, mirroring HEVCFrameTagFromTransfer(state, buf, true)
// (initialized mode) without re-scanning bytes it has already passed. Feeding the
// buffer as it grows and stopping at the first resolved slice lets the caller stop
// copying the rest of the transfer payload (the over-buffering this avoids was the
// dominant per-frame memmove in HEVC UHD scans). Equivalence to the batch function
// for arbitrary inputs and arbitrary chunk splits is enforced by
// FuzzHEVCTagScannerEquivalence.
type HEVCTagScanner struct {
	started  bool   // first start code located
	resolved bool   // a non-null slice tag was found (initialized-mode early stop)
	done     bool   // the whole buffer has been walked (finalize processed the tail)
	tag      string // resolved tag
	pos      int    // current start code position
	scLen    int    // current start code length (3 or 4)
	search   int    // resume offset for the next start-code search
}

// Scan walks any NALs newly delimited in buf (the accumulated transfer so far),
// applying SPS/PPS to state and stopping at the first non-null slice tag. When
// finalize is true, buf is the complete transfer, so the trailing NAL (delimited by
// the buffer end) is processed too. It returns the resolved tag and whether a
// non-null slice tag was found. Once resolved or finalized, further calls are
// no-ops. The end result (tag and state mutations) equals
// HEVCFrameTagFromTransfer(state, finalBuf, true).
func (sc *HEVCTagScanner) Scan(state *HEVCTagState, buf []byte, finalize bool) (string, bool) {
	if sc.resolved || sc.done {
		return sc.tag, sc.resolved
	}
	if state == nil {
		return "", false
	}
	// HEVCFrameTagFromTransfer requires at least 6 bytes; a transfer that ends
	// shorter yields "" with no NAL processing. A buffer below 6 bytes can never
	// contain a delimited NAL, so nothing was processed incrementally either.
	if finalize && len(buf) < 6 {
		sc.done = true
		return "", false
	}

	if !sc.started {
		p, l := nextStartCodeResume(buf, sc.search, 0)
		if p == -1 {
			if finalize {
				sc.done = true
				return "", false
			}
			sc.search = resumeOffset(len(buf), 0)
			return "", false
		}
		sc.started = true
		sc.pos, sc.scLen = p, l
		sc.search = sc.pos + sc.scLen
	}

	for {
		nalStart := sc.pos + sc.scLen
		nextPos, nextLen := nextStartCodeResume(buf, sc.search, nalStart)
		if nextPos == -1 {
			if !finalize {
				// Early resolve: the trailing (not-yet-delimited) NAL is most often the
				// single slice of the access unit. If it is a slice whose header is already
				// buffered, resolve read-only and stop copying the rest of the transfer.
				if t := peekHEVCSliceTag(state, buf[nalStart:]); t != "" {
					sc.resolved = true
					sc.tag = t
					return sc.tag, true
				}
				sc.search = resumeOffset(len(buf), nalStart)
				return "", false
			}
			// Trailing NAL runs to the buffer end (batch: nalEnd = len(data)).
			if t, isDefault, valid := applyHEVCNAL(state, buf[nalStart:]); valid && isDefault && t != "" {
				sc.resolved = true
				sc.tag = t
			}
			sc.done = true
			return sc.tag, sc.resolved
		}
		// Complete NAL delimited by [nalStart, nextPos).
		if t, isDefault, valid := applyHEVCNAL(state, buf[nalStart:nextPos]); valid && isDefault && t != "" {
			sc.resolved = true
			sc.tag = t
			return sc.tag, true
		}
		sc.pos, sc.scLen = nextPos, nextLen
		sc.search = sc.pos + sc.scLen
	}
}
