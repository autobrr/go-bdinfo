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
	if filepath.Ext(reportName) == "" {
		reportName = reportName + ".bdinfo"
	}
	if path != "" {
		reportName = path
	}

	if _, err := os.Stat(reportName); err == nil {
		backup := fmt.Sprintf("%s.%d", reportName, time.Now().Unix())
		_ = os.Rename(reportName, backup)
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
	fmt.Fprintf(&b, "%-16s%d bytes\n", "Disc Size:", bd.Size)
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
	fmt.Fprintf(&b, "%-16s%s\n\n", "BDInfo:", productVersion)

	if scan.ScanError != nil {
		fmt.Fprintf(&b, "WARNING: Report is incomplete because: %s\n", scan.ScanError.Error())
	}
	if len(scan.FileErrors) > 0 {
		b.WriteString("WARNING: File errors were encountered during scan:\n")
		for name, err := range scan.FileErrors {
			fmt.Fprintf(&b, "\n%s\t%s\n", name, err.Error())
		}
	}

	sort.Slice(playlists, func(i, j int) bool {
		return playlists[i].FileSize() > playlists[j].FileSize()
	})

	separator := strings.Repeat("#", 10)
	for _, playlist := range playlists {
		if settings.FilterLoopingPlaylists && !playlist.IsValid() {
			continue
		}

		playlistLength := playlist.TotalLength()
		totalLength := util.FormatTime(playlistLength, true)
		totalLengthShort := util.FormatTime(playlistLength, false)

		totalSize := playlist.TotalSize()
		discSize := bd.Size
		totalBitrate := formatMbps(playlist.TotalBitRate())

		videoCodec := ""
		videoBitrate := ""
		if len(playlist.VideoStreams) > 0 {
			vs := playlist.VideoStreams[0]
			videoCodec = stream.CodecAltNameForInfo(vs)
			if vs.BitRate > 0 {
				videoBitrate = fmt.Sprintf("%d", int(math.Round(float64(vs.BitRate)/1000)))
			}
		}

		mainAudio := ""
		secondaryAudio := ""
		mainLang := ""
		if len(playlist.AudioStreams) > 0 {
			as := playlist.AudioStreams[0]
			mainLang = as.LanguageCode()
			mainAudio = fmt.Sprintf("%s %s", stream.CodecAltNameForInfo(as), as.ChannelDescription())
			if as.BitRate > 0 {
				mainAudio += fmt.Sprintf(" %dKbps", int(math.Round(float64(as.BitRate)/1000)))
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
					secondaryAudio += fmt.Sprintf(" %dKbps", int(math.Round(float64(as.BitRate)/1000)))
				}
				if as.SampleRate > 0 && as.BitDepth > 0 {
					secondaryAudio += fmt.Sprintf(" (%dkHz/%d-bit)", as.SampleRate/1000, as.BitDepth)
				}
				break
			}
		}

		b.WriteString("\n********************\n")
		fmt.Fprintf(&b, "PLAYLIST: %s\n", playlist.Name)
		b.WriteString("********************\n\n")
		b.WriteString("<--- BEGIN FORUMS PASTE --->\n")
		b.WriteString("[code]\n")
		fmt.Fprintf(&b, "%-64s%-8s%-8s%-16s%-16s%-8s%-8s%-42s%s\n", "", "", "", "", "", "Total", "Video", "", "")
		fmt.Fprintf(&b, "%-64s%-8s%-8s%-16s%-16s%-8s%-8s%-42s%s\n", "Title", "Codec", "Length", "Movie Size", "Disc Size", "Bitrate", "Bitrate", "Main Audio Track", "Secondary Audio Track")
		fmt.Fprintf(&b, "%-64s%-8s%-8s%-16s%-16s%-8s%-8s%-42s%s\n", "-----", "------", "-------", "--------------", "--------------", "-------", "-------", "------------------", "---------------------")
		fmt.Fprintf(&b, "%-64s%-8s%-8s%-16s%-16s%-8s%-8s%-42s%s\n", playlist.Name, videoCodec, totalLengthShort, fmt.Sprintf("%d", totalSize), fmt.Sprintf("%d", discSize), totalBitrate, videoBitrate, mainAudio, secondaryAudio)
		b.WriteString("[/code]\n\n")
		b.WriteString("[code]\n\n")
		if settings.GroupByTime {
			fmt.Fprintf(&b, "\n%sStart group %.0f%s\n", separator, playlistLength*1000, separator)
		}

		b.WriteString("\nDISC INFO:\n\n")
		if bd.DiscTitle != "" {
			fmt.Fprintf(&b, "%-16s%s\n", "Disc Title:", bd.DiscTitle)
		}
		fmt.Fprintf(&b, "%-16s%s\n", "Disc Label:", bd.VolumeLabel)
		fmt.Fprintf(&b, "%-16s%d bytes\n", "Disc Size:", bd.Size)
		fmt.Fprintf(&b, "%-16s%s\n", "Protection:", protection)
		if len(extra) > 0 {
			fmt.Fprintf(&b, "%-16s%s\n", "Extras:", strings.Join(extra, ", "))
		}
		fmt.Fprintf(&b, "%-16s%s\n\n", "BDInfo:", productVersion)

		b.WriteString("PLAYLIST REPORT:\n\n")
		fmt.Fprintf(&b, "%-24s%s\n", "Name:", playlist.Name)
		fmt.Fprintf(&b, "%-24s%s (h:m:s.ms)\n", "Length:", totalLength)
		fmt.Fprintf(&b, "%-24s%d bytes\n", "Size:", totalSize)
		fmt.Fprintf(&b, "%-24s%s Mbps\n", "Total Bitrate:", totalBitrate)

		if playlist.HasHiddenTracks {
			b.WriteString("\n(*) Indicates included stream hidden by this playlist.\n")
		}

		if len(playlist.VideoStreams) > 0 {
			b.WriteString("\nVIDEO:\n\n")
			fmt.Fprintf(&b, "%-24s%-20s%-16s\n", "Codec", "Bitrate", "Description")
			fmt.Fprintf(&b, "%-24s%-20s%-16s\n", "-----", "-------", "-----------")
			for _, st := range playlist.SortedStreams {
				if !st.Base().IsVideoStream() {
					continue
				}
				name := stream.CodecNameForInfo(st)
				bitrate := fmt.Sprintf("%d", int(math.Round(float64(st.Base().BitRate)/1000)))
				fmt.Fprintf(&b, "%-24s%-20s%-16s\n", name, bitrate, st.Description())
			}
		}

		if len(playlist.AudioStreams) > 0 {
			b.WriteString("\nAUDIO:\n\n")
			fmt.Fprintf(&b, "%-24s%-8s%-12s%-16s%-16s\n", "Codec", "PID", "Language", "Bitrate", "Description")
			fmt.Fprintf(&b, "%-24s%-8s%-12s%-16s%-16s\n", "-----", "---", "--------", "-------", "-----------")
			for _, st := range playlist.SortedStreams {
				if !st.Base().IsAudioStream() {
					continue
				}
				fmt.Fprintf(&b, "%-24s%-8d%-12s%-16s%-16s\n",
					stream.CodecNameForInfo(st),
					st.Base().PID,
					st.Base().LanguageName,
					fmt.Sprintf("%d", int(math.Round(float64(st.Base().BitRate)/1000))),
					st.Description(),
				)
			}
		}

		if len(playlist.GraphicsStreams) > 0 || len(playlist.TextStreams) > 0 {
			b.WriteString("\nSUBTITLES:\n\n")
			fmt.Fprintf(&b, "%-24s%-8s%-12s%-16s\n", "Codec", "PID", "Language", "Description")
			fmt.Fprintf(&b, "%-24s%-8s%-12s%-16s\n", "-----", "---", "--------", "-----------")
			for _, st := range playlist.SortedStreams {
				if !(st.Base().IsGraphicsStream() || st.Base().IsTextStream()) {
					continue
				}
				fmt.Fprintf(&b, "%-24s%-8d%-12s%-16s\n",
					stream.CodecNameForInfo(st),
					st.Base().PID,
					st.Base().LanguageName,
					st.Description(),
				)
			}
		}

		b.WriteString("\nFILES:\n\n")
		fmt.Fprintf(&b, "%-10s%-16s%-16s%-16s\n", "File", "Length", "Size", "Bitrate")
		fmt.Fprintf(&b, "%-10s%-16s%-16s%-16s\n", "----", "------", "----", "-------")
		for _, clip := range playlist.StreamClips {
			if clip.AngleIndex != 0 {
				continue
			}
			length := util.FormatTime(clip.Length, true)
			bitrate := "0"
			if clip.PacketSeconds > 0 {
				bitrate = fmt.Sprintf("%d", int(math.Round(float64(clip.PacketBitRate())/1000)))
			}
			fmt.Fprintf(&b, "%-10s%-16s%-16d%-16s\n", clip.DisplayName(), length, clip.PacketSize(), bitrate)
		}

		if len(playlist.Chapters) > 0 {
			b.WriteString("\nCHAPTERS:\n\n")
			fmt.Fprintf(&b, "%-10s%-16s\n", "Number", "Time")
			fmt.Fprintf(&b, "%-10s%-16s\n", "------", "----")
			for idx, chapter := range playlist.Chapters {
				fmt.Fprintf(&b, "%-10d%-16s\n", idx+1, util.FormatTime(chapter, true))
			}
		}

		if settings.GenerateStreamDiagnostics {
			b.WriteString("\n\nSTREAM DIAGNOSTICS:\n\n")
			fmt.Fprintf(&b, "%-16s%-16s%-16s%-16s%-24s%-24s%-24s%-16s%-16s\n",
				"File", "PID", "Type", "Codec", "Language", "Seconds", "Bitrate", "Bytes", "Packets")
			fmt.Fprintf(&b, "%-16s%-16s%-16s%-16s%-24s%-24s%-24s%-16s%-16s\n",
				"----", "---", "----", "-----", "--------", "--------------", "--------------", "-------------", "-------")

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
				for pid, clipStream := range clip.StreamFile.Streams {
					if _, ok := playlist.Streams[pid]; !ok {
						continue
					}
					playlistStream := playlist.Streams[pid]

					clipSeconds := "0"
					clipBitRate := "0"
					if clip.StreamFile.Length > 0 {
						clipSeconds = fmt.Sprintf("%.3f", clip.StreamFile.Length)
						clipBitRate = util.FormatNumber(int64(math.Round(float64(clipStream.Base().PayloadBytes)*8/clip.StreamFile.Length/1000)))
					}

					language := ""
					if code := playlistStream.Base().LanguageCode(); code != "" {
						language = fmt.Sprintf("%s (%s)", code, playlistStream.Base().LanguageName)
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

		b.WriteString("\n[/code]\n<---- END FORUMS PASTE ---->\n\n")

		if settings.GenerateTextSummary {
			b.WriteString("QUICK SUMMARY:\n\n")
			if bd.DiscTitle != "" {
				fmt.Fprintf(&b, "Disc Title: %s\n", bd.DiscTitle)
			}
			fmt.Fprintf(&b, "Disc Label: %s\n", bd.VolumeLabel)
			fmt.Fprintf(&b, "Disc Size: %d bytes\n", bd.Size)
			fmt.Fprintf(&b, "Protection: %s\n", protection)
			fmt.Fprintf(&b, "Playlist: %s\n", playlist.Name)
			fmt.Fprintf(&b, "Size: %d bytes\n", totalSize)
			fmt.Fprintf(&b, "Length: %s\n", totalLength)
			fmt.Fprintf(&b, "Total Bitrate: %s Mbps\n", totalBitrate)
			b.WriteString("\n")
		}

		if settings.GroupByTime {
			b.WriteString("\n")
			fmt.Fprintf(&b, "%sEnd group%s\n\n\n", separator, separator)
		}
	}

	return reportName, os.WriteFile(reportName, []byte(b.String()), 0o644)
}

func formatMbps(bitrate uint64) string {
	if bitrate == 0 {
		return "0"
	}
	val := math.Round(float64(bitrate)/10000.0) / 100.0
	return fmt.Sprintf("%.2f", val)
}
