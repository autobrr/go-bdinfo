// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bdrom

import (
	"context"
	"slices"

	"github.com/autobrr/go-bdinfo/internal/stream"
)

// ssifMVCProbe accepts report-only MVC discovery from one current, unchanged
// program within the initial bounded PMT probe. It does not read another source or
// establish any CLPI-to-SSIF packet association.
type ssifMVCProbe struct {
	mapping  videoCollection
	pat, pmt collectionPSI
	entries  []pmtStreamEntry
	invalid  bool
}

func newSSIFMVCProbe(ctx context.Context) *ssifMVCProbe {
	return &ssifMVCProbe{mapping: videoCollection{ctx: ctx, pmtPID: 0xffff}}
}

func (p *ssifMVCProbe) packet(packet []byte, offset int) {
	if p.invalid {
		return
	}
	ts := packet[offset:]
	if len(ts) != 188 || ts[0] != 0x47 {
		p.invalid = true
		return
	}
	pid := uint16(ts[1]&31)<<8 | uint16(ts[2])
	var psi *collectionPSI
	if pid == 0 {
		psi = &p.pat
	} else if pid == p.mapping.pmtPID {
		psi = &p.pmt
	} else {
		return
	}
	control := ts[3] >> 4 & 3
	if control == 0 || ts[1]&0x80 != 0 || ts[3]&0xc0 != 0 {
		p.invalid = true
		return
	}
	at := 4
	announced := false
	if control&2 != 0 {
		at = 5 + int(ts[4])
		if at > 188 || (control == 2 && at != 188) || (control == 3 && at == 188) || !validCollectionAdaptation(ts[5:at]) {
			p.invalid = true
			return
		}
		announced = ts[4] > 0 && ts[5]&0x80 != 0
	}
	duplicate, reason := psi.continuity.observe(ts, announced)
	if duplicate {
		return
	}
	if reason != "" || announced {
		p.invalid = true
		return
	}
	if control&1 == 0 {
		return
	}
	if !psi.feed(ts[at:], ts[1]&0x40 != 0, func(section []byte) bool {
		oldPID, oldProgram := p.mapping.pmtPID, p.mapping.pmtProgram
		if !p.mapping.consumePSI(pid, section, 0, len(packet)) {
			return false
		}
		if section[5]&1 == 0 {
			return true
		}
		if pid == 0 {
			return oldPID == 0xffff || (oldPID == p.mapping.pmtPID && oldProgram == p.mapping.pmtProgram)
		}
		_, _, entries, ok := parsePMTSection(section)
		if !ok || (p.entries != nil && !slices.Equal(p.entries, entries)) {
			return false
		}
		p.entries = entries
		return true
	}) {
		p.invalid = true
	}
}

func (p *ssifMVCProbe) streams() []uint16 {
	if p.invalid || p.pat.n != 0 || p.pmt.n != 0 {
		return nil
	}
	var pids []uint16
	for _, entry := range p.entries {
		if stream.StreamType(entry.StreamType) == stream.StreamTypeMVCVideo {
			pids = append(pids, entry.PID)
		}
	}
	return pids
}
