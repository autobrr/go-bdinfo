// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

// Package video defines optional, synchronous elementary-stream collection.
// It contains transport facts, not decoded frames or HDR metadata.
package video

import (
	"context"
	"errors"
)

// Transport errors support errors.Is through StreamResult.Err and End.Err.
var (
	ErrInvalidTransport   = errors.New("invalid video transport")
	ErrIncomplete         = errors.New("incomplete video input")
	ErrUnsupportedMapping = errors.New("unsupported video source mapping")
)

// Factory selects a source/PID before its first byte is delivered. Returning
// (nil, nil) declines collection. Different sources may invoke Factory concurrently;
// each accepted Consumer must own independent state. StreamInfo is an owned snapshot.
type Factory func(context.Context, StreamInfo) (Consumer, error)

// Consumer receives ordered calls for one source/PID with synchronous backpressure.
// Consume and Discontinuity must observe cancellation; a blocked callback cannot
// be interrupted by BDInfo. Finish is called exactly once, including after callback
// failure or cancellation, and must release resources without waiting for input.
// No callbacks remain active when Run returns. Panics follow normal Go semantics.
type Consumer interface {
	// Consume borrows Data and Starts until it returns. Retaining them requires a copy.
	Consume(context.Context, Chunk) error
	// Discontinuity follows all preceding valid bytes. Reset decoder state at this boundary.
	Discontinuity(context.Context, Discontinuity) error
	// Finish permits normal decoder finalization only when CleanEOF is true.
	// It has no context so canceled scans can still release owned resources.
	Finish(End) error
}

// Codec is the original Blu-ray/MPEG transport stream coding_type.
type Codec uint8

// Video coding types used by Blu-ray transport streams.
const (
	MPEG1 Codec = 0x01
	MPEG2 Codec = 0x02
	AVC   Codec = 0x1b
	MVC   Codec = 0x20
	HEVC  Codec = 0x24
	VC1   Codec = 0xea
)

// Source identifies the actual disc-relative input container within one disc.
// Path plus bdinfo.DiscInfo.Path is a storage identity, not a content fingerprint.
// Kind is "m2ts" or "ssif". LogicalClip remains the original M2TS clip name.
type Source struct {
	Path        string
	Kind        string
	LogicalClip string
}

// Role is a per-occurrence MPLS video role. Unknown also covers hidden streams.
type Role string

const (
	Primary   Role = "primary"
	Secondary Role = "secondary"
	Unknown   Role = "unknown"
)

// Mapping retains an MPLS stream-entry mapping. EntryType 1 references the main
// clip; types 2/3/4 carry subpath references. HasSubclip distinguishes an absent
// subclip reference from reference zero. Unsupported entry types retain Unknown.
type Mapping struct {
	PID        uint16
	Codec      Codec
	Role       Role
	EntryType  uint8
	Subpath    uint8
	Subclip    uint8
	HasSubclip bool
}

// STCSequence identifies a CLPI clock epoch in the logical M2TS packet domain.
// StartPacket is inclusive and EndPacket exclusive. PresentationStart45 and
// PresentationEnd45 are original unsigned 45 kHz boundaries. HasEndPacket is
// false when neither a next sequence nor logical file length establishes an end.
type STCSequence struct {
	// ATCIndex and ATCStartPacket retain the enclosing arrival-clock sequence.
	ATCIndex            int
	ATCStartPacket      uint32
	ID                  uint8
	PCRPID              uint16
	StartPacket         uint32
	EndPacket           uint64
	HasEndPacket        bool
	PresentationStart45 uint32
	PresentationEnd45   uint32
}

// Occurrence identifies a candidate playlist use, including alternate angles.
// Roles may conflict between occurrences; no global main PID is implied.
// STC is nil when clock association is unavailable. AssociationError explains
// missing/contradictory context or unsupported SSIF packet-coordinate mapping.
type Occurrence struct {
	Playlist         string
	Index            int
	Angle            int
	STCID            uint8
	Mapping          Mapping
	STC              *STCSequence
	AssociationError error
}

// StreamInfo identifies one physical source/PID and every candidate use known
// before scanning. Final report selection can depend on scan results.
// Final StreamResult.Info can invalidate an occurrence association when observed
// container framing contradicts the metadata's logical M2TS packet coordinates.
type StreamInfo struct {
	Source      Source
	PID         uint16
	Codec       Codec
	Occurrences []Occurrence
}

// PESStart identifies an original PES boundary, even when it carries no payload
// or timestamps. Offset is an absolute ES byte offset; PacketIndex is zero-based
// in the actual container and SourceOffset includes any four-byte M2TS prefix.
// PTS/DTS use 90,000 ticks/second modulo 2^33, without unwrapping, substitution,
// reordering or running maxima. Zero is valid when the corresponding flag is set.
// Segment starts at zero and increases on every discontinuity.
type PESStart struct {
	// Header borrows the original complete PES header (at most 264 bytes),
	// including the prefix, fixed fields and optional fields. It excludes the
	// elementary payload and remains valid until Consume returns. Consumers
	// can inspect unsupported modes without reopening the physical source.
	Header       []byte
	Offset       uint64
	PacketIndex  uint64
	SourceOffset uint64
	PTS          uint64
	DTS          uint64
	HasPTS       bool
	HasDTS       bool
	Segment      uint64
}

// Chunk carries only ES bytes, excluding TS/adaptation/PES headers and bounded
// PES padding. Offset starts at zero and advances across discontinuities. Starts
// uses absolute offsets in the same coordinate system; a chunk can split any NAL
// or access unit. Empty chunks can carry PES boundaries, including at final EOF.
type Chunk struct {
	Offset uint64
	Data   []byte
	Starts []PESStart
}

// Discontinuity identifies a reset at Offset and its source packet position.
// Segment is the new monotonically increasing segment ID. DataLoss is false only
// for an announced boundary with no interrupted header or bounded PES.
type Discontinuity struct {
	Offset       uint64
	PacketIndex  uint64
	SourceOffset uint64
	Segment      uint64
	Reason       string
	DataLoss     bool
}

// End is the terminal transport state. CleanEOF is false for damage, missing
// selected input, read failure, callback failure or cancellation. Err retains
// the first transport/callback cause. A clean transport does not imply HDR10+.
type End struct {
	CleanEOF bool
	Err      error
	// SourcePacketCount is the observed physical container packet count at EOF.
	// It includes all PIDs and adaptation-only packets; it is valid only when
	// CleanEOF is true and is zero otherwise. It is not a CLPI declared extent.
	SourcePacketCount uint64
}

// Status describes transport/consumer execution, independently of HDR outcomes.
type Status string

const (
	Declined   Status = "declined"
	Complete   Status = "complete"
	Incomplete Status = "incomplete"
	Failed     Status = "failed"
	Canceled   Status = "canceled"
)

// StreamResult is a compact record of one factory attempt. DeliveredBytes and
// Boundaries count successful Consume calls; Discontinuities counts delivered
// reset events. Segment is the final segment. Err preserves callback, transport
// and Finish failures with errors.Is/errors.As. Consumer failures do not replace
// BDInfo report errors. Declined streams have no transport completion claim.
type StreamResult struct {
	Info            StreamInfo
	Status          Status
	DeliveredBytes  uint64
	Boundaries      uint64
	Discontinuities uint64
	Segment         uint64
	CleanEOF        bool
	Err             error
	// SourcePacketCount has the same observed-EOF meaning as End.SourcePacketCount.
	SourcePacketCount uint64
}
