package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/blang/semver"
	"github.com/creativeprojects/go-selfupdate"
	"github.com/spf13/cobra"

	"github.com/autobrr/go-bdinfo/internal/bdrom"
	"github.com/autobrr/go-bdinfo/internal/report"
	"github.com/autobrr/go-bdinfo/internal/settings"
)

var version = "dev"

type rootOptions struct {
	path             string
	reportFile       string
	filterShortValue int
	genDiag          bool
	extDiag          bool
	enableSSIF       bool
	filterLooping    bool
	filterShort      bool
	keepOrder        bool
	genSummary       bool
	includeNotes     bool
	groupByTime      bool
	forumsOnly       bool
	mainOnly         bool
	summaryOnly      bool
	stdout           bool
	selfUpdate       bool
	progress         bool
}

var opts rootOptions

const helpBanner = "" +
	"                                                                                \n" +
	"██████╗ ██████╗ ██╗███╗   ██╗███████╗ ██████╗\n" +
	"██╔══██╗██╔══██╗██║████╗  ██║██╔════╝██╔═══██╗\n" +
	"██████╔╝██║  ██║██║██╔██╗ ██║█████╗  ██║   ██║\n" +
	"██╔══██╗██║  ██║██║██║╚██╗██║██╔══╝  ██║   ██║\n" +
	"██████╔╝██████╔╝██║██║ ╚████║██║     ╚██████╔╝\n" +
	"╚═════╝ ╚═════╝ ╚═╝╚═╝  ╚═══╝╚═╝      ╚═════╝"

const helpTemplate = helpBanner + `

{{with or .Long .Short}}{{. | trimTrailingWhitespaces}}

{{end}}{{if or .Runnable .HasSubCommands}}{{.UsageString}}{{end}}`

var rootCmd = &cobra.Command{
	Use:           "bdinfo <path>",
	Short:         "Go rewrite of BDInfo.",
	Args:          cobra.MaximumNArgs(1),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          runRoot,
}

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update bdinfo",
	Long:  "Update bdinfo to latest version (release builds only).",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runSelfUpdate(cmd.Context())
	},
	DisableFlagsInUseLine: true,
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Fprintf(cmd.OutOrStdout(), "bdinfo version: %s\n", version)
		return nil
	},
	DisableFlagsInUseLine: true,
}

func init() {
	rootCmd.SetOut(os.Stdout)
	rootCmd.SetErr(os.Stderr)
	rootCmd.SetHelpTemplate(helpTemplate)

	rootCmd.Flags().StringVarP(&opts.reportFile, "reportfilename", "o", "", "The report filename with extension (use - for stdout)")
	rootCmd.Flags().BoolVar(&opts.stdout, "stdout", false, "Write report to stdout")
	rootCmd.Flags().BoolVarP(&opts.genDiag, "generatestreamdiagnostics", "g", false, "Generate the stream diagnostics section")
	rootCmd.Flags().BoolVarP(&opts.extDiag, "extendedstreamdiagnostics", "e", false, "Enable extended video diagnostics (HEVC metadata)")
	rootCmd.Flags().BoolVarP(&opts.enableSSIF, "enablessif", "b", false, "Enable SSIF support (default on; use --enablessif=false to disable)")
	rootCmd.Flags().BoolVarP(&opts.filterLooping, "filterloopingplaylists", "l", false, "Filter looping playlists")
	rootCmd.Flags().BoolVarP(&opts.filterShort, "filtershortplaylist", "y", false, "Filter short playlists (default on; use --filtershortplaylist=false to disable)")
	rootCmd.Flags().IntVarP(&opts.filterShortValue, "filtershortplaylistvalue", "v", 20, "Short playlist length threshold in seconds")
	rootCmd.Flags().BoolVarP(&opts.keepOrder, "keepstreamorder", "k", false, "Keep stream order")
	rootCmd.Flags().BoolVarP(&opts.genSummary, "generatetextsummary", "m", false, "Generate quick summary block (default on; use --generatetextsummary=false to disable)")
	rootCmd.Flags().BoolVarP(&opts.includeNotes, "includeversionandnotes", "q", false, "Include version and scan notes (default on; use --includeversionandnotes=false to disable)")
	rootCmd.Flags().BoolVarP(&opts.groupByTime, "groupbytime", "j", false, "Group by time")
	rootCmd.Flags().BoolVarP(&opts.forumsOnly, "forumsonly", "f", false, "Output only the forums paste block")
	rootCmd.Flags().BoolVar(&opts.mainOnly, "main", false, "Output only the main playlist (likely what you want)")
	rootCmd.Flags().BoolVarP(&opts.summaryOnly, "summaryonly", "s", false, "Output only the quick summary block (likely what you want)")
	rootCmd.Flags().BoolVar(&opts.selfUpdate, "self-update", false, "Update bdinfo to latest version (release builds only)")
	rootCmd.Flags().BoolVar(&opts.selfUpdate, "update", false, "Update bdinfo to latest version (release builds only)")
	rootCmd.Flags().BoolVar(&opts.progress, "progress", false, "Print scan progress to stderr")

	rootCmd.AddCommand(updateCmd)
	rootCmd.AddCommand(versionCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "bdinfo: %s\n", err.Error())
		os.Exit(1)
	}
}

func runRoot(cmd *cobra.Command, args []string) error {
	if opts.selfUpdate {
		return runSelfUpdate(cmd.Context())
	}

	if len(args) == 0 {
		if cmd.Flags().NFlag() == 0 {
			return cmd.Help()
		}
		return errors.New("path is required")
	}

	opts.path = args[0]

	cwd, _ := os.Getwd()
	s := settings.Default(cwd)

	flags := cmd.Flags()
	if flags.Changed("generatestreamdiagnostics") {
		s.GenerateStreamDiagnostics = opts.genDiag
	}
	if flags.Changed("extendedstreamdiagnostics") {
		s.ExtendedStreamDiagnostics = opts.extDiag
	}
	if flags.Changed("enablessif") {
		s.EnableSSIF = opts.enableSSIF
	}
	if flags.Changed("filterloopingplaylists") {
		s.FilterLoopingPlaylists = opts.filterLooping
	}
	if flags.Changed("filtershortplaylist") {
		s.FilterShortPlaylists = opts.filterShort
	}
	s.FilterShortPlaylistsVal = opts.filterShortValue
	if flags.Changed("keepstreamorder") {
		s.KeepStreamOrder = opts.keepOrder
	}
	if flags.Changed("generatetextsummary") {
		s.GenerateTextSummary = opts.genSummary
	}
	if opts.reportFile != "" {
		s.ReportFileName = opts.reportFile
	}
	if opts.stdout {
		s.ReportFileName = "-"
	}
	if flags.Changed("includeversionandnotes") {
		s.IncludeVersionAndNotes = opts.includeNotes
	}
	if flags.Changed("groupbytime") {
		s.GroupByTime = opts.groupByTime
	}
	if flags.Changed("forumsonly") {
		s.ForumsOnly = opts.forumsOnly
	}
	if flags.Changed("main") {
		s.MainPlaylistOnly = opts.mainOnly
	}
	if flags.Changed("summaryonly") {
		s.SummaryOnly = opts.summaryOnly
		if s.SummaryOnly {
			s.GenerateTextSummary = true
		}
	}

	if err := runForPath(opts.path, s, opts.progress); err != nil {
		return err
	}
	if s.ReportFileName == "-" {
		fmt.Fprintln(os.Stderr, "Scan complete.")
	} else {
		fmt.Println("Scan complete.")
	}
	return nil
}

func runSelfUpdate(ctx context.Context) error {
	if version == "" || version == "dev" {
		return errors.New("self-update is only available in release builds")
	}

	if _, err := semver.ParseTolerant(version); err != nil {
		return fmt.Errorf("could not parse version: %w", err)
	}

	latest, found, err := selfupdate.DetectLatest(ctx, selfupdate.ParseSlug("autobrr/go-bdinfo"))
	if err != nil {
		return fmt.Errorf("error occurred while detecting version: %w", err)
	}
	if !found {
		return fmt.Errorf("latest version for %s/%s could not be found from github repository", "autobrr/go-bdinfo", version)
	}

	if latest.LessOrEqual(version) {
		fmt.Printf("Current binary is the latest version: %s\n", version)
		return nil
	}

	exe, err := selfupdate.ExecutablePath()
	if err != nil {
		return fmt.Errorf("could not locate executable path: %w", err)
	}

	if err := selfupdate.UpdateTo(ctx, latest.AssetURL, latest.AssetName, exe); err != nil {
		return fmt.Errorf("error occurred while updating binary: %w", err)
	}

	fmt.Printf("Successfully updated to version: %s\n", latest.Version())
	return nil
}

func runForPath(path string, settings settings.Settings, progress bool) error {
	lower := strings.ToLower(path)
	if strings.HasSuffix(lower, ".iso") {
		reportPath, err := scanAndReport(path, settings, progress)
		if err != nil {
			return err
		}
		if reportPath != "-" {
			fmt.Printf("Report written: %s\n", reportPath)
		}
		return nil
	}

	bdmvDirs := []string{}
	_ = filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && strings.EqualFold(d.Name(), "BDMV") {
			bdmvDirs = append(bdmvDirs, p)
			return filepath.SkipDir
		}
		return nil
	})
	isIsoLevel := false
	isoFiles := []string{}
	if len(bdmvDirs) == 0 {
		_ = filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if !d.IsDir() && strings.HasSuffix(strings.ToLower(p), ".iso") {
				isoFiles = append(isoFiles, p)
			}
			return nil
		})
		if len(isoFiles) > 0 {
			isIsoLevel = true
			bdmvDirs = isoFiles
		}
	}

	if len(bdmvDirs) > 1 || isIsoLevel {
		stdout := settings.ReportFileName == "-"
		oldReport := settings.ReportFileName
		if stdout {
			oldReport = ""
		}
		reports := []string{}
		for _, sub := range bdmvDirs {
			target := sub
			if !isIsoLevel {
				target = filepath.Dir(sub)
			}
			if oldReport != "" {
				parent := filepath.Dir(target)
				if isIsoLevel {
					settings.ReportFileName = filepath.Join(parent, strings.TrimSuffix(filepath.Base(target), filepath.Ext(target))) + "." + strings.TrimPrefix(filepath.Ext(oldReport), ".")
				} else {
					settings.ReportFileName = filepath.Join(parent, filepath.Base(target)) + "." + strings.TrimPrefix(filepath.Ext(oldReport), ".")
				}
				reports = append(reports, settings.ReportFileName)
			}
			reportPath, err := scanAndReport(target, settings, progress)
			if err != nil {
				return err
			}
			if oldReport == "" && reportPath != "-" {
				fmt.Printf("Report written: %s\n", reportPath)
			}
		}
		if oldReport != "" && len(reports) > 0 {
			if len(reports) == 1 {
				_ = os.Rename(reports[0], oldReport)
				fmt.Printf("Report written: %s\n", oldReport)
				return nil
			}
			combined, err := os.Create(oldReport)
			if err != nil {
				return err
			}
			defer combined.Close()
			for _, reportFile := range reports {
				data, err := os.ReadFile(reportFile)
				if err != nil {
					continue
				}
				combined.Write(data)
				combined.WriteString("\n\n\n\n\n")
				_ = os.Remove(reportFile)
			}
			fmt.Printf("Report written: %s\n", oldReport)
			return nil
		}
		return nil
	}

	reportPath, err := scanAndReport(path, settings, progress)
	if err != nil {
		return err
	}
	if reportPath != "-" {
		fmt.Printf("Report written: %s\n", reportPath)
	}
	return nil
}

func scanAndReport(path string, settings settings.Settings, progress bool) (string, error) {
	rom, err := bdrom.New(path, settings)
	if err != nil {
		return "", err
	}
	defer rom.Close()

	start := time.Now()
	if progress {
		fmt.Fprintf(os.Stderr, "Scanning: %s\n", path)
		fmt.Fprintf(os.Stderr, "Found %d playlists, %d clip infos, %d streams\n", len(rom.PlaylistFiles), len(rom.StreamClipFiles), len(rom.StreamFiles))
	}

	result := rom.Scan()

	playlists := make([]*bdrom.PlaylistFile, 0, len(rom.PlaylistFiles))
	if len(rom.PlaylistOrder) > 0 {
		for _, name := range rom.PlaylistOrder {
			if pl, ok := rom.PlaylistFiles[name]; ok {
				playlists = append(playlists, pl)
			}
		}
	} else {
		for _, pl := range rom.PlaylistFiles {
			playlists = append(playlists, pl)
		}
	}
	reportPath, err := report.WriteReport("", rom, playlists, result, settings)
	if err != nil {
		return "", err
	}
	if progress {
		fmt.Fprintf(os.Stderr, "Scan complete in %s\n", time.Since(start).Round(time.Millisecond))
	}
	return reportPath, nil
}
