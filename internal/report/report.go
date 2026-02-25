package report

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/autobrr/go-bdinfo/internal/bdrom"
	"github.com/autobrr/go-bdinfo/internal/settings"
	"github.com/autobrr/go-bdinfo/internal/stream"
	"github.com/autobrr/go-bdinfo/internal/util"
)

const productVersion = "0.8.0.0"

func WriteReport(path string, bd *bdrom.BDROM, playlists []*bdrom.PlaylistFile, scan bdrom.ScanResult, settings settings.Settings) (string, error) {
	reportName := settings.ReportFileName
	if strings.Contains(reportName, "{0}") {
		reportName = strings.ReplaceAll(reportName, "{0}", bd.VolumeLabel)
	} else if regexp.MustCompile(`\{\d+\}`).MatchString(reportName) {
		reportName = fmt.Sprintf(reportName, bd.VolumeLabel)
	}
	if reportName != "-" && filepath.Ext(reportName) == "" {
		// Match BDInfo: default extension.
		reportName = reportName + ".bdinfo"
	}
	if path != "" {
		reportName = path
	}

	if reportName != "-" {
		if _, err := os.Stat(reportName); err == nil {
			backup := fmt.Sprintf("%s.%d", reportName, time.Now().Unix())
			_ = os.Rename(reportName, backup)
		}
	}

	if settings.SummaryOnly {
		output := buildSummaryOnly(bd, playlists, settings)
		if reportName == "-" {
			_, err := os.Stdout.WriteString(output)
			return reportName, err
		}
		return reportName, os.WriteFile(reportName, []byte(output), 0o644)
	}

	var b strings.Builder
	protection := "AACS"
	if bd.IsBDPlus {
		protection = "BD+"
	} else if bd.IsUHD {
		protection = "AACS2"
	}

	if bd.DiscTitle != "" {
		fmt.Fprintf(&b, "%-16s%s\n", "Disc Title:", bd.DiscTitle)
	}
	fmt.Fprintf(&b, "%-16s%s\n", "Disc Label:", bd.VolumeLabel)
	fmt.Fprintf(&b, "%-16s%s bytes\n", "Disc Size:", util.FormatNumber(int64(bd.Size)))
	fmt.Fprintf(&b, "%-16s%s\n", "Protection:", protection)

	extra := []string{}
	if bd.IsUHD {
		extra = append(extra, "Ultra HD")
	}
	if bd.IsBDJava {
		extra = append(extra, "BD-Java")
	}
	if bd.Is50Hz {
		extra = append(extra, "50Hz Content")
	}
	if bd.Is3D {
		extra = append(extra, "Blu-ray 3D")
	}
	if bd.IsDBOX {
		extra = append(extra, "D-BOX Motion Code")
	}
	if bd.IsPSP {
		extra = append(extra, "PSP Digital Copy")
	}
	if len(extra) > 0 {
		fmt.Fprintf(&b, "%-16s%s\n", "Extras:", strings.Join(extra, ", "))
	}
	fmt.Fprintf(&b, "%-16s%s\n\n\n", "BDInfo:", productVersion)

	if settings.IncludeVersionAndNotes {
		fmt.Fprintf(&b, "%-16s%s\n\n\n", "Notes:", "")
		b.WriteString("BDINFO HOME:\n")
		b.WriteString("  Cinema Squid (old)\n")
		b.WriteString("    http://www.cinemasquid.com/blu-ray/tools/bdinfo\n")
		b.WriteString("  UniqProject GitHub (new)\n")
		b.WriteString("   https://github.com/UniqProject/BDInfo\n")
		b.WriteString("\n")
		b.WriteString("INCLUDES FORUMS REPORT FOR:\n")
		b.WriteString("  AVS Forum Blu-ray Audio and Video Specifications Thread\n")
		b.WriteString("    http://www.avsforum.com/avs-vb/showthread.php?t=1155731\n")
		b.WriteString("\n\n")
	}

	if scan.ScanError != nil {
		fmt.Fprintf(&b, "WARNING: Report is incomplete because: %s\n", scan.ScanError.Error())
	}
	if len(scan.FileErrors) > 0 {
		b.WriteString("WARNING: File errors were encountered during scan:\n")
		for name, err := range scan.FileErrors {
			// C# appends stack trace; Go errors generally don't include one.
			fmt.Fprintf(&b, "\n%s\t%s\n", name, err.Error())
		}
	}

	if settings.MainPlaylistOnly || settings.BigPlaylistOnly {
		playlists = selectMainPlaylist(playlists, settings)
	}

	sort.SliceStable(playlists, func(i, j int) bool {
		return playlists[i].FileSize() > playlists[j].FileSize()
	})

	separator := strings.Repeat("#", 10)
	for _, playlist := range playlists {
		if settings.FilterLoopingPlaylists && !playlist.IsValid() {
			continue
		}
		var summary strings.Builder

		playlistLength := playlist.TotalLength()
		totalLength := util.FormatTime(playlistLength, true)
		totalLengthShort := util.FormatTime(playlistLength, false)

		totalSize := playlist.TotalSize()
		discSize := bd.Size
		totalSizeStr := util.FormatNumber(int64(totalSize))
		discSizeStr := util.FormatNumber(int64(discSize))
		totalBitrate := formatMbps(playlist.TotalBitRate())

		videoCodec := ""
		videoBitrate := ""
		if len(playlist.VideoStreams) > 0 {
			vs := playlist.VideoStreams[0]
			videoCodec = stream.CodecAltNameForInfo(vs)
			videoBitrate = formatMbps(uint64(vs.BitRate))
		}

		mainAudio := ""
		secondaryAudio := ""
		mainLang := ""
		if len(playlist.AudioStreams) > 0 {
			as := playlist.AudioStreams[0]
			mainLang = as.LanguageCode()
			mainAudio = fmt.Sprintf("%s %s", stream.CodecAltNameForInfo(as), as.ChannelDescription())
			if as.BitRate > 0 {
				mainAudio += fmt.Sprintf(" %dKbps", int(math.RoundToEven(float64(as.BitRate)/1000)))
			}
			if as.SampleRate > 0 && as.BitDepth > 0 {
				mainAudio += fmt.Sprintf(" (%dkHz/%d-bit)", as.SampleRate/1000, as.BitDepth)
			}
		}
		if len(playlist.AudioStreams) > 1 {
			for i := 1; i < len(playlist.AudioStreams); i++ {
				as := playlist.AudioStreams[i]
				if as.LanguageCode() != mainLang {
					continue
				}
				if as.StreamType == stream.StreamTypeAC3PlusSecondaryAudio ||
					as.StreamType == stream.StreamTypeDTSHDSecondaryAudio ||
					(as.StreamType == stream.StreamTypeAC3Audio && as.ChannelCount == 2) {
					continue
				}
				secondaryAudio = fmt.Sprintf("%s %s", stream.CodecAltNameForInfo(as), as.ChannelDescription())
				if as.BitRate > 0 {
					secondaryAudio += fmt.Sprintf(" %dKbps", int(math.RoundToEven(float64(as.BitRate)/1000)))
				}
				if as.SampleRate > 0 && as.BitDepth > 0 {
					secondaryAudio += fmt.Sprintf(" (%dkHz/%d-bit)", as.SampleRate/1000, as.BitDepth)
				}
				break
			}
		}

		b.WriteString("\n\n********************\n")
		fmt.Fprintf(&b, "PLAYLIST: %s\n", playlist.Name)
		b.WriteString("********************\n\n\n")
		b.WriteString("<--- BEGIN FORUMS PASTE --->\n")
		b.WriteString("[code]\n")
		fmt.Fprintf(&b, "%-64s%-8s%-8s%-16s%-16s%-8s%-8s%-42s%s\n", "", "", "", "", "", "Total", "Video", "", "")
		fmt.Fprintf(&b, "%-64s%-8s%-8s%-16s%-16s%-8s%-8s%-42s%s\n", "Title", "Codec", "Length", "Movie Size", "Disc Size", "Bitrate", "Bitrate", "Main Audio Track", "Secondary Audio Track")
		fmt.Fprintf(&b, "%-64s%-8s%-8s%-16s%-16s%-8s%-8s%-42s%s\n", "-----", "------", "-------", "--------------", "--------------", "-------", "-------", "------------------", "---------------------")
		fmt.Fprintf(&b, "%-64s%-8s%-8s%-16s%-16s%-8s%-8s%-42s%s\n", playlist.Name, videoCodec, totalLengthShort, totalSizeStr, discSizeStr, totalBitrate, videoBitrate, mainAudio, secondaryAudio)
		b.WriteString("[/code]\n\n\n")
		b.WriteString("[code]\n\n\n")
		if settings.GroupByTime {
			fmt.Fprintf(&b, "\n%sStart group %.0f%s\n", separator, playlistLength*1000, separator)
		}

		b.WriteString("DISC INFO:\n\n\n")
		if bd.DiscTitle != "" {
			fmt.Fprintf(&b, "%-16s%s\n", "Disc Title:", bd.DiscTitle)
		}
		fmt.Fprintf(&b, "%-16s%s\n", "Disc Label:", bd.VolumeLabel)
		fmt.Fprintf(&b, "%-16s%s bytes\n", "Disc Size:", util.FormatNumber(int64(bd.Size)))
		fmt.Fprintf(&b, "%-16s%s\n", "Protection:", protection)
		if len(extra) > 0 {
			fmt.Fprintf(&b, "%-16s%s\n", "Extras:", strings.Join(extra, ", "))
		}
		// BDInfo prints the product version in every playlist block.
		fmt.Fprintf(&b, "%-16s%s\n\n\n", "BDInfo:", productVersion)

		b.WriteString("PLAYLIST REPORT:\n\n\n")
		fmt.Fprintf(&b, "%-24s%s\n", "Name:", playlist.Name)
		fmt.Fprintf(&b, "%-24s%s (h:m:s.ms)\n", "Length:", totalLength)
		fmt.Fprintf(&b, "%-24s%s bytes\n", "Size:", totalSizeStr)
		fmt.Fprintf(&b, "%-24s%s Mbps\n", "Total Bitrate:", totalBitrate)

		if playlist.HasHiddenTracks {
			// Match official BDInfo: it inserts a CRLF line-break before the hidden-tracks note.
			// The surrounding report uses LF; this specific CRLF is a quirk in the official output.
			b.WriteString("\r\n(*) Indicates included stream hidden by this playlist.\n")
		}

		if len(playlist.VideoStreams) > 0 {
			b.WriteString("\n\nVIDEO:\n\n\n")
			fmt.Fprintf(&b, "%-24s%-20s%-16s\n", "Codec", "Bitrate", "Description")
			fmt.Fprintf(&b, "%-24s%-20s%-16s\n", "-----", "-------", "-----------")
			for _, st := range playlist.SortedStreams {
				if !st.Base().IsVideoStream() {
					continue
				}
				name := stream.CodecNameForInfo(st)
				if st.Base().AngleIndex > 0 {
					name = fmt.Sprintf("%s (%d)", name, st.Base().AngleIndex)
				}
				bitrate := fmt.Sprintf("%d", int(math.RoundToEven(float64(st.Base().BitRate)/1000)))
				if st.Base().AngleIndex > 0 {
					bitrate = fmt.Sprintf("%s (%d)", bitrate, int(math.RoundToEven(float64(st.Base().ActiveBitRate)/1000)))
				}
				bitrate = fmt.Sprintf("%s kbps", bitrate)
				fmt.Fprintf(&b, "%-24s%-20s%-16s\n", hiddenPrefix(st)+name, bitrate, st.Description())
				if settings.GenerateTextSummary {
					fmt.Fprintf(&summary, "%sVideo: %s / %s / %s\n", hiddenPrefix(st), name, bitrate, st.Description())
				}
			}
		}

		if len(playlist.AudioStreams) > 0 {
			b.WriteString("\n\nAUDIO:\n\n\n")
			fmt.Fprintf(&b, "%-32s%-16s%-16s%-16s\n", "Codec", "Language", "Bitrate", "Description")
			fmt.Fprintf(&b, "%-32s%-16s%-16s%-16s\n", "-----", "--------", "-------", "-----------")
			for _, st := range playlist.SortedStreams {
				if !st.Base().IsAudioStream() {
					continue
				}
				bitrate := fmt.Sprintf("%d kbps", int(math.RoundToEven(float64(st.Base().BitRate)/1000)))
				fmt.Fprintf(&b, "%-32s%-16s%-16s%-16s\n",
					hiddenPrefix(st)+stream.CodecNameForInfo(st),
					st.Base().LanguageName,
					bitrate,
					st.Description(),
				)
				if settings.GenerateTextSummary {
					fmt.Fprintf(&summary, "%sAudio: %s / %s / %s\n", hiddenPrefix(st), st.Base().LanguageName, stream.CodecNameForInfo(st), st.Description())
				}
			}
		}

		if len(playlist.GraphicsStreams) > 0 {
			b.WriteString("\n\nSUBTITLES:\n\n\n")
			fmt.Fprintf(&b, "%-32s%-16s%-16s%-16s\n", "Codec", "Language", "Bitrate", "Description")
			fmt.Fprintf(&b, "%-32s%-16s%-16s%-16s\n", "-----", "--------", "-------", "-----------")
			for _, st := range playlist.SortedStreams {
				if !st.Base().IsGraphicsStream() {
					continue
				}
				bitrate := fmt.Sprintf("%.3f kbps", float64(st.Base().BitRate)/1000.0)
				fmt.Fprintf(&b, "%-32s%-16s%-16s%-16s\n",
					hiddenPrefix(st)+stream.CodecNameForInfo(st),
					st.Base().LanguageName,
					bitrate,
					st.Description(),
				)
				if settings.GenerateTextSummary {
					fmt.Fprintf(&summary, "%sSubtitle: %s / %s\n", hiddenPrefix(st), st.Base().LanguageName, bitrate)
				}
			}
		}

		if len(playlist.TextStreams) > 0 {
			b.WriteString("\n\nTEXT:\n\n\n")
			fmt.Fprintf(&b, "%-32s%-16s%-16s%-16s\n", "Codec", "Language", "Bitrate", "Description")
			fmt.Fprintf(&b, "%-32s%-16s%-16s%-16s\n", "-----", "--------", "-------", "-----------")
			for _, st := range playlist.SortedStreams {
				if !st.Base().IsTextStream() {
					continue
				}
				bitrate := fmt.Sprintf("%.3f kbps", float64(st.Base().BitRate)/1000.0)
				fmt.Fprintf(&b, "%-32s%-16s%-16s%-16s\n",
					hiddenPrefix(st)+stream.CodecNameForInfo(st),
					st.Base().LanguageName,
					bitrate,
					st.Description(),
				)
			}
		}

		b.WriteString("\n\nFILES:\n\n\n")
		fmt.Fprintf(&b, "%-16s%-16s%-16s%-16s%-16s\n", "Name", "Time In", "Length", "Size", "Total Bitrate")
		fmt.Fprintf(&b, "%-16s%-16s%-16s%-16s%-16s\n", "----", "-------", "------", "----", "-------------")
		for _, clip := range playlist.StreamClips {
			clipName := clip.DisplayName()
			if clip.AngleIndex > 0 {
				clipName = fmt.Sprintf("%s (%d)", clipName, clip.AngleIndex)
			}
			length := util.FormatTime(clip.Length, true)
			timeIn := util.FormatTime(clip.RelativeTimeIn, true)
			clipSize := util.FormatNumber(int64(clip.PacketSize()))
			bitrate := util.FormatNumber(int64(math.RoundToEven(float64(clip.PacketBitRate()) / 1000)))
			fmt.Fprintf(&b, "%-16s%-16s%-16s%-16s%-16s\n", clipName, timeIn, length, clipSize, bitrate)
		}

		if settings.GroupByTime {
			b.WriteString("\n")
			fmt.Fprintf(&b, "%sEnd group%s\n\n\n", separator, separator)
		}

		// Match official BDInfo: always print the CHAPTERS section (even when empty).
		b.WriteString("\n\nCHAPTERS:\n\n\n")
		fmt.Fprintf(&b, "%-16s%-16s%-16s%-16s%-16s%-16s%-16s%-16s%-16s%-16s%-16s%-16s%-16s\n",
			"Number",
			"Time In",
			"Length",
			"Avg Video Rate",
			"Max 1-Sec Rate",
			"Max 1-Sec Time",
			"Max 5-Sec Rate",
			"Max 5-Sec Time",
			"Max 10Sec Rate",
			"Max 10Sec Time",
			"Avg Frame Size",
			"Max Frame Size",
			"Max Frame Time",
		)
		fmt.Fprintf(&b, "%-16s%-16s%-16s%-16s%-16s%-16s%-16s%-16s%-16s%-16s%-16s%-16s%-16s\n",
			"------",
			"-------",
			"------",
			"--------------",
			"--------------",
			"--------------",
			"--------------",
			"--------------",
			"--------------",
			"--------------",
			"--------------",
			"--------------",
			"--------------",
		)
		writeChapters(&b, playlist)

		if settings.GenerateStreamDiagnostics {
			b.WriteString("\n\nSTREAM DIAGNOSTICS:\n\n\n")
			fmt.Fprintf(&b, "%-16s%-16s%-16s%-16s%-24s%-24s%-24s%-16s%-16s\n",
				"File", "PID", "Type", "Codec", "Language", "Seconds", "Bitrate", "Bytes", "Packets")
			fmt.Fprintf(&b, "%-16s%-16s%-16s%-16s%-24s%-24s%-24s%-16s%-16s\n",
				"----", "---", "----", "-----", "--------", "--------------", "--------------", "-------------", "-----")

			reported := map[string]bool{}
			for _, clip := range playlist.StreamClips {
				if clip.StreamFile == nil {
					continue
				}
				if reported[clip.Name] {
					continue
				}
				reported[clip.Name] = true

				clipName := clip.DisplayName()
				if clip.AngleIndex > 0 {
					clipName = fmt.Sprintf("%s (%d)", clipName, clip.AngleIndex)
				}

				// Match official BDInfo ordering: when stream insertion order is known, use it directly.
				// Fallback to deterministic kind/PID ordering.
				pids := make([]uint16, 0, len(clip.StreamFile.Streams))
				hasStreamOrder := len(clip.StreamFile.StreamOrder) > 0
				if hasStreamOrder {
					for _, pid := range clip.StreamFile.StreamOrder {
						clipStream := clip.StreamFile.Streams[pid]
						if clipStream == nil {
							continue
						}
						if _, ok := playlist.Streams[pid]; !ok {
							continue
						}
						pids = append(pids, pid)
					}
				} else {
					for pid, clipStream := range clip.StreamFile.Streams {
						if clipStream == nil {
							continue
						}
						if _, ok := playlist.Streams[pid]; !ok {
							continue
						}
						pids = append(pids, pid)
					}
				}
				streamWeight := func(pid uint16) int {
					if playlistStream := playlist.Streams[pid]; playlistStream != nil {
						base := playlistStream.Base()
						if base.IsVideoStream() && base.IsHidden {
							return 5
						}
					}
					info := clip.StreamFile.Streams[pid]
					if info == nil {
						return 9
					}
					base := info.Base()
					switch {
					case base.IsVideoStream():
						return 0
					case base.IsAudioStream():
						return 1
					case base.IsGraphicsStream():
						return 2
					case base.IsTextStream():
						return 3
					default:
						return 4
					}
				}
				if !hasStreamOrder {
					sort.Slice(pids, func(i, j int) bool {
						wi := streamWeight(pids[i])
						wj := streamWeight(pids[j])
						if wi != wj {
							return wi < wj
						}
						return pids[i] < pids[j]
					})
				}

				for _, pid := range pids {
					clipStream := clip.StreamFile.Streams[pid]
					if clipStream == nil {
						continue
					}

					clipSeconds := "0"
					clipBitRate := "0"
					if clip.StreamFile.Length > 0 {
						seconds := clip.StreamFile.Length
						clipSeconds = fmt.Sprintf("%.3f", seconds)
						clipBitRate = util.FormatNumber(int64(math.RoundToEven(float64(clipStream.Base().PayloadBytes) * 8 / seconds / 1000)))
					}

					language := ""
					if playlistStream := playlist.Streams[pid]; playlistStream != nil {
						if code := playlistStream.Base().LanguageCode(); code != "" {
							language = fmt.Sprintf("%s (%s)", code, playlistStream.Base().LanguageName)
						}
					}

					fmt.Fprintf(&b, "%-16s%-16s%-16s%-16s%-24s%-24s%-24s%-16s%-16s\n",
						clipName,
						fmt.Sprintf("%d (0x%X)", clipStream.Base().PID, clipStream.Base().PID),
						fmt.Sprintf("0x%02X", byte(clipStream.Base().StreamType)),
						stream.CodecShortNameForInfo(clipStream),
						language,
						clipSeconds,
						clipBitRate,
						util.FormatNumber(int64(clipStream.Base().PayloadBytes)),
						util.FormatNumber(int64(clipStream.Base().PacketCount)),
					)
				}
			}
		}

		b.WriteString("\n\n[/code]\n<---- END FORUMS PASTE ---->\n\n\n")

		if settings.GenerateTextSummary {
			b.WriteString("QUICK SUMMARY:\n\n\n")
			if bd.DiscTitle != "" {
				fmt.Fprintf(&b, "Disc Title: %s\n", bd.DiscTitle)
			}
			fmt.Fprintf(&b, "Disc Label: %s\n", bd.VolumeLabel)
			fmt.Fprintf(&b, "Disc Size: %s bytes\n", util.FormatNumber(int64(bd.Size)))
			fmt.Fprintf(&b, "Protection: %s\n", protection)
			fmt.Fprintf(&b, "Playlist: %s\n", playlist.Name)
			fmt.Fprintf(&b, "Size: %s bytes\n", totalSizeStr)
			fmt.Fprintf(&b, "Length: %s\n", totalLength)
			fmt.Fprintf(&b, "Total Bitrate: %s Mbps\n", totalBitrate)
			if summary.Len() > 0 {
				b.WriteString(summary.String())
			}
			b.WriteString("\n\n\n\n\n")
		}
	}

	output := b.String()
	if settings.SummaryOnly {
		output = extractQuickSummary(output)
	} else if settings.ForumsOnly {
		output = extractForumsBlocks(output)
	}
	if reportName == "-" {
		_, err := os.Stdout.WriteString(output)
		return reportName, err
	}
	return reportName, os.WriteFile(reportName, []byte(output), 0o644)
}

func selectMainPlaylist(playlists []*bdrom.PlaylistFile, settings settings.Settings) []*bdrom.PlaylistFile {
	if len(playlists) == 0 {
		return playlists
	}
	candidates := playlists
	if settings.FilterLoopingPlaylists || settings.FilterShortPlaylists {
		filtered := make([]*bdrom.PlaylistFile, 0, len(playlists))
		for _, p := range playlists {
			if p == nil {
				continue
			}
			if !p.IsValid() {
				continue
			}
			filtered = append(filtered, p)
		}
		if len(filtered) > 0 {
			candidates = filtered
		}
	}
	main := candidates[0]
	for _, p := range candidates[1:] {
		if p == nil {
			continue
		}

		// Official BDInfo `--printonlybigplaylist`: pick by size (fallback to name).
		if settings.BigPlaylistOnly {
			mainSize := main.TotalSize()
			pSize := p.TotalSize()
			if pSize > mainSize {
				main = p
				continue
			}
			if pSize < mainSize {
				continue
			}
			if p.Name < main.Name {
				main = p
			}
			continue
		}

		// `--main`: heuristic main feature selection. Prefer longest, then largest.
		mainLen := main.TotalLength()
		pLen := p.TotalLength()
		if pLen > mainLen {
			main = p
			continue
		}
		if pLen < mainLen {
			continue
		}
		mainSize := main.TotalSize()
		pSize := p.TotalSize()
		if pSize > mainSize {
			main = p
			continue
		}
		if pSize < mainSize {
			continue
		}

		mainBitrate := main.TotalBitRate()
		pBitrate := p.TotalBitRate()
		if pBitrate > mainBitrate {
			main = p
			continue
		}
		if pBitrate < mainBitrate {
			continue
		}
		if p.Name < main.Name {
			main = p
		}
	}
	return []*bdrom.PlaylistFile{main}
}

func extractForumsBlocks(report string) string {
	const startMarker = "<--- BEGIN FORUMS PASTE --->"
	const endMarker = "<---- END FORUMS PASTE ---->"
	var out strings.Builder
	rest := report
	for {
		start := strings.Index(rest, startMarker)
		if start == -1 {
			break
		}
		rest = rest[start:]
		end := strings.Index(rest, endMarker)
		if end == -1 {
			break
		}
		end += len(endMarker)
		if out.Len() > 0 {
			out.WriteString("\n\n")
		}
		out.WriteString(rest[:end])
		rest = rest[end:]
	}
	if out.Len() == 0 {
		return report
	}
	out.WriteString("\n")
	return out.String()
}

func extractQuickSummary(report string) string {
	const marker = "QUICK SUMMARY:"
	start := strings.Index(report, marker)
	if start == -1 {
		return report
	}
	out := strings.TrimSpace(report[start:])
	if out == "" {
		return report
	}
	return out + "\n"
}

func buildSummaryOnly(bd *bdrom.BDROM, playlists []*bdrom.PlaylistFile, settings settings.Settings) string {
	if settings.MainPlaylistOnly {
		playlists = selectMainPlaylist(playlists, settings)
	}

	sort.SliceStable(playlists, func(i, j int) bool {
		return playlists[i].FileSize() > playlists[j].FileSize()
	})

	protection := "AACS"
	if bd.IsBDPlus {
		protection = "BD+"
	} else if bd.IsUHD {
		protection = "AACS2"
	}

	var out strings.Builder
	for _, playlist := range playlists {
		if settings.FilterLoopingPlaylists && !playlist.IsValid() {
			continue
		}
		var summary strings.Builder

		playlistLength := playlist.TotalLength()
		totalLength := util.FormatTime(playlistLength, true)

		totalSize := playlist.TotalSize()
		totalSizeStr := util.FormatNumber(int64(totalSize))
		totalBitrate := formatMbps(playlist.TotalBitRate())

		if len(playlist.VideoStreams) > 0 {
			for _, st := range playlist.SortedStreams {
				if !st.Base().IsVideoStream() {
					continue
				}
				name := stream.CodecNameForInfo(st)
				if st.Base().AngleIndex > 0 {
					name = fmt.Sprintf("%s (%d)", name, st.Base().AngleIndex)
				}
				bitrate := fmt.Sprintf("%d", int(math.RoundToEven(float64(st.Base().BitRate)/1000)))
				if st.Base().AngleIndex > 0 {
					bitrate = fmt.Sprintf("%s (%d)", bitrate, int(math.RoundToEven(float64(st.Base().ActiveBitRate)/1000)))
				}
				bitrate = fmt.Sprintf("%s kbps", bitrate)
				if settings.GenerateTextSummary {
					fmt.Fprintf(&summary, "%sVideo: %s / %s / %s\n", hiddenPrefix(st), name, bitrate, st.Description())
				}
			}
		}

		if len(playlist.AudioStreams) > 0 {
			for _, st := range playlist.SortedStreams {
				if !st.Base().IsAudioStream() {
					continue
				}
				if settings.GenerateTextSummary {
					fmt.Fprintf(&summary, "%sAudio: %s / %s / %s\n", hiddenPrefix(st), st.Base().LanguageName, stream.CodecNameForInfo(st), st.Description())
				}
			}
		}

		if len(playlist.GraphicsStreams) > 0 {
			for _, st := range playlist.SortedStreams {
				if !st.Base().IsGraphicsStream() {
					continue
				}
				if settings.GenerateTextSummary {
					bitrate := fmt.Sprintf("%.3f kbps", float64(st.Base().BitRate)/1000.0)
					fmt.Fprintf(&summary, "%sSubtitle: %s / %s\n", hiddenPrefix(st), st.Base().LanguageName, bitrate)
				}
			}
		}

		if settings.GenerateTextSummary {
			out.WriteString("QUICK SUMMARY:\n\n")
			if bd.DiscTitle != "" {
				fmt.Fprintf(&out, "Disc Title: %s\n", bd.DiscTitle)
			}
			fmt.Fprintf(&out, "Disc Label: %s\n", bd.VolumeLabel)
			fmt.Fprintf(&out, "Disc Size: %s bytes\n", util.FormatNumber(int64(bd.Size)))
			fmt.Fprintf(&out, "Protection: %s\n", protection)
			fmt.Fprintf(&out, "Playlist: %s\n", playlist.Name)
			fmt.Fprintf(&out, "Size: %s bytes\n", totalSizeStr)
			fmt.Fprintf(&out, "Length: %s\n", totalLength)
			fmt.Fprintf(&out, "Total Bitrate: %s Mbps\n", totalBitrate)
			if summary.Len() > 0 {
				out.WriteString(summary.String())
			}
		}
	}
	return out.String()
}

func formatMbps(bitrate uint64) string {
	if bitrate == 0 {
		return "0.00"
	}
	val := math.RoundToEven(float64(bitrate)/10000.0) / 100.0
	return fmt.Sprintf("%.2f", val)
}

func hiddenPrefix(info stream.Info) string {
	if info == nil {
		return ""
	}
	if info.Base().IsHidden {
		return "* "
	}
	return ""
}

type floatQueue struct {
	vals []float64
}

func (q *floatQueue) Enqueue(v float64) {
	q.vals = append(q.vals, v)
}

func (q *floatQueue) Dequeue() float64 {
	if len(q.vals) == 0 {
		return 0
	}
	v := q.vals[0]
	q.vals = q.vals[1:]
	return v
}

func formatTimeHmsms(seconds float64, padHour bool) string {
	ticks := max(int64(seconds*10000000.0), 0)
	totalMillis := ticks / 10000
	ms := int(totalMillis % 1000)
	totalSeconds := ticks / 10000000
	s := int(totalSeconds % 60)
	totalMinutes := totalSeconds / 60
	m := int(totalMinutes % 60)
	h := int(totalMinutes / 60)
	if padHour {
		return fmt.Sprintf("%02d:%02d:%02d.%03d", h, m, s, ms)
	}
	return fmt.Sprintf("%d:%02d:%02d.%03d", h, m, s, ms)
}

func writeChapters(b *strings.Builder, playlist *bdrom.PlaylistFile) {
	if playlist == nil || len(playlist.Chapters) == 0 {
		return
	}

	window1Bits := &floatQueue{}
	window1Seconds := &floatQueue{}
	window1BitsSum := 0.0
	window1SecondsSum := 0.0
	window1PeakBitrate := 0.0
	window1PeakLocation := 0.0

	window5Bits := &floatQueue{}
	window5Seconds := &floatQueue{}
	window5BitsSum := 0.0
	window5SecondsSum := 0.0
	window5PeakBitrate := 0.0
	window5PeakLocation := 0.0

	window10Bits := &floatQueue{}
	window10Seconds := &floatQueue{}
	window10BitsSum := 0.0
	window10SecondsSum := 0.0
	window10PeakBitrate := 0.0
	window10PeakLocation := 0.0

	chapterPosition := 0.0
	chapterBits := 0.0
	chapterFrameCount := int64(0)
	chapterSeconds := 0.0
	_ = chapterSeconds
	chapterMaxFrameSize := 0.0
	chapterMaxFrameLocation := 0.0

	diagPID := uint16(0)
	if len(playlist.VideoStreams) > 0 {
		diagPID = playlist.VideoStreams[0].PID
	}

	chapterIndex := 0
	clipIndex := 0
	diagIndex := 0

	for chapterIndex < len(playlist.Chapters) {
		var clip *bdrom.StreamClip
		var file *bdrom.StreamFile
		if clipIndex < len(playlist.StreamClips) {
			clip = playlist.StreamClips[clipIndex]
			file = clip.StreamFile
		}

		chapterStart := playlist.Chapters[chapterIndex]
		chapterEnd := playlist.TotalLength()
		if chapterIndex < len(playlist.Chapters)-1 {
			chapterEnd = playlist.Chapters[chapterIndex+1]
		}
		chapterLength := chapterEnd - chapterStart

		var diagList []bdrom.StreamDiagnostics
		if clip != nil && clip.AngleIndex == 0 && file != nil {
			if list, ok := file.StreamDiagnostics[diagPID]; ok {
				diagList = list
			}
		}

		if diagList != nil {
			for diagIndex < len(diagList) && chapterPosition < chapterEnd {
				diag := diagList[diagIndex]
				diagIndex++

				if diag.Marker < clip.TimeIn {
					continue
				}

				chapterPosition = diag.Marker - clip.TimeIn + clip.RelativeTimeIn

				seconds := diag.Interval
				bits := float64(diag.Bytes) * 8.0

				chapterBits += bits
				chapterSeconds += seconds

				if diag.Tag != "" {
					chapterFrameCount++
				}

				window1SecondsSum += seconds
				window1Seconds.Enqueue(seconds)
				window1BitsSum += bits
				window1Bits.Enqueue(bits)

				window5SecondsSum += seconds
				window5Seconds.Enqueue(seconds)
				window5BitsSum += bits
				window5Bits.Enqueue(bits)

				window10SecondsSum += seconds
				window10Seconds.Enqueue(seconds)
				window10BitsSum += bits
				window10Bits.Enqueue(bits)

				if bits > chapterMaxFrameSize*8 {
					chapterMaxFrameSize = bits / 8
					chapterMaxFrameLocation = chapterPosition
				}

				if window1SecondsSum > 1.0 {
					bitrate := window1BitsSum / window1SecondsSum
					if bitrate > window1PeakBitrate && chapterPosition-window1SecondsSum > 0 {
						window1PeakBitrate = bitrate
						window1PeakLocation = chapterPosition - window1SecondsSum
					}
					window1BitsSum -= window1Bits.Dequeue()
					window1SecondsSum -= window1Seconds.Dequeue()
				}
				if window5SecondsSum > 5.0 {
					bitrate := window5BitsSum / window5SecondsSum
					if bitrate > window5PeakBitrate && chapterPosition-window5SecondsSum > 0 {
						window5PeakBitrate = bitrate
						window5PeakLocation = chapterPosition - window5SecondsSum
						if window5PeakLocation < 0 {
							window5PeakLocation = 0
						}
					}
					window5BitsSum -= window5Bits.Dequeue()
					window5SecondsSum -= window5Seconds.Dequeue()
				}
				if window10SecondsSum > 10.0 {
					bitrate := window10BitsSum / window10SecondsSum
					if bitrate > window10PeakBitrate && chapterPosition-window10SecondsSum > 0 {
						window10PeakBitrate = bitrate
						window10PeakLocation = chapterPosition - window10SecondsSum
					}
					window10BitsSum -= window10Bits.Dequeue()
					window10SecondsSum -= window10Seconds.Dequeue()
				}
			}
		}

		if diagList == nil || diagIndex == len(diagList) {
			if clipIndex < len(playlist.StreamClips) {
				clipIndex++
				diagIndex = 0
			} else {
				chapterPosition = chapterEnd
			}
		}

		if chapterPosition >= chapterEnd {
			chapterIndex++

			chapterBitrate := 0.0
			if chapterLength > 0 {
				chapterBitrate = chapterBits / chapterLength
			}
			chapterAvgFrameSize := 0.0
			if chapterFrameCount > 0 {
				chapterAvgFrameSize = chapterBits / float64(chapterFrameCount) / 8
			}

			fmt.Fprintf(b, "%-16d%-16s%-16s%-16s%-16s%-16s%-16s%-16s%-16s%-16s%-16s%-16s%-16s\n",
				chapterIndex,
				formatTimeHmsms(chapterStart, false),
				formatTimeHmsms(chapterLength, false),
				fmt.Sprintf("%s kbps", util.FormatNumber(int64(math.RoundToEven(chapterBitrate/1000)))),
				fmt.Sprintf("%s kbps", util.FormatNumber(int64(math.RoundToEven(window1PeakBitrate/1000)))),
				formatTimeHmsms(window1PeakLocation, true),
				fmt.Sprintf("%s kbps", util.FormatNumber(int64(math.RoundToEven(window5PeakBitrate/1000)))),
				formatTimeHmsms(window5PeakLocation, true),
				fmt.Sprintf("%s kbps", util.FormatNumber(int64(math.RoundToEven(window10PeakBitrate/1000)))),
				formatTimeHmsms(window10PeakLocation, true),
				fmt.Sprintf("%s bytes", util.FormatNumber(int64(math.RoundToEven(chapterAvgFrameSize)))),
				fmt.Sprintf("%s bytes", util.FormatNumber(int64(math.RoundToEven(chapterMaxFrameSize)))),
				formatTimeHmsms(chapterMaxFrameLocation, true),
			)

			window1Bits = &floatQueue{}
			window1Seconds = &floatQueue{}
			window1BitsSum = 0
			window1SecondsSum = 0
			window1PeakBitrate = 0
			window1PeakLocation = 0

			window5Bits = &floatQueue{}
			window5Seconds = &floatQueue{}
			window5BitsSum = 0
			window5SecondsSum = 0
			window5PeakBitrate = 0
			window5PeakLocation = 0

			window10Bits = &floatQueue{}
			window10Seconds = &floatQueue{}
			window10BitsSum = 0
			window10SecondsSum = 0
			window10PeakBitrate = 0
			window10PeakLocation = 0

			chapterBits = 0
			chapterSeconds = 0
			chapterFrameCount = 0
			chapterMaxFrameSize = 0
			chapterMaxFrameLocation = 0
		}
	}
}
