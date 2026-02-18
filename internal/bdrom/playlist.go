package bdrom

import (
	"fmt"
	"io"
	"math"
	"slices"
	"sort"
	"strings"

	"github.com/autobrr/go-bdinfo/internal/fs"
	"github.com/autobrr/go-bdinfo/internal/settings"
	"github.com/autobrr/go-bdinfo/internal/stream"
	"github.com/autobrr/go-bdinfo/internal/util"
)

type PlaylistFile struct {
	FileInfo        fs.FileInfo
	Name            string
	FileType        string
	IsInitialized   bool
	Settings        settings.Settings
	HasHiddenTracks bool
	HasLoops        bool
	IsCustom        bool
	MVCBaseViewR    bool

	Chapters []float64

	Streams         map[uint16]stream.Info
	PlaylistStreams map[uint16]stream.Info
	StreamClips     []*StreamClip
	AngleStreams    []map[uint16]stream.Info
	AngleClips      []map[float64]*StreamClip
	AngleCount      int

	SortedStreams   []stream.Info
	VideoStreams    []*stream.VideoStream
	AudioStreams    []*stream.AudioStream
	TextStreams     []*stream.TextStream
	GraphicsStreams []*stream.GraphicsStream
}

func NewPlaylistFile(fileInfo fs.FileInfo, settings settings.Settings) *PlaylistFile {
	return &PlaylistFile{
		FileInfo:        fileInfo,
		Name:            strings.ToUpper(fileInfo.Name()),
		Settings:        settings,
		Streams:         make(map[uint16]stream.Info),
		PlaylistStreams: make(map[uint16]stream.Info),
	}
}

func NewCustomPlaylist(name string, clips []*StreamClip, settings settings.Settings) *PlaylistFile {
	pl := &PlaylistFile{
		Name:            name,
		IsCustom:        true,
		Settings:        settings,
		Streams:         make(map[uint16]stream.Info),
		PlaylistStreams: make(map[uint16]stream.Info),
	}
	for _, clip := range clips {
		newClip := NewStreamClip(clip.StreamFile, clip.StreamClipFile, settings)
		newClip.Name = clip.Name
		newClip.TimeIn = clip.TimeIn
		newClip.TimeOut = clip.TimeOut
		newClip.Length = newClip.TimeOut - newClip.TimeIn
		newClip.RelativeTimeIn = pl.TotalLength()
		newClip.RelativeTimeOut = newClip.RelativeTimeIn + newClip.Length
		newClip.AngleIndex = clip.AngleIndex
		newClip.Chapters = append(newClip.Chapters, clip.TimeIn)
		pl.StreamClips = append(pl.StreamClips, newClip)
		if newClip.AngleIndex > pl.AngleCount {
			pl.AngleCount = newClip.AngleIndex
		}
		if newClip.AngleIndex == 0 {
			pl.Chapters = append(pl.Chapters, newClip.RelativeTimeIn)
		}
	}
	pl.loadStreamClips()
	pl.IsInitialized = true
	return pl
}

func (p *PlaylistFile) FileSize() uint64 {
	var size uint64
	for _, clip := range p.StreamClips {
		size += clip.FileSize
	}
	return size
}

func (p *PlaylistFile) InterleavedFileSize() uint64 {
	var size uint64
	for _, clip := range p.StreamClips {
		size += clip.InterleavedFileSize
	}
	return size
}

func (p *PlaylistFile) TotalLength() float64 {
	var length float64
	for _, clip := range p.StreamClips {
		if clip.AngleIndex == 0 {
			length += clip.Length
		}
	}
	return length
}

func (p *PlaylistFile) TotalAngleLength() float64 {
	var length float64
	for _, clip := range p.StreamClips {
		length += clip.Length
	}
	return length
}

func (p *PlaylistFile) TotalSize() uint64 {
	var size uint64
	for _, clip := range p.StreamClips {
		if clip.AngleIndex == 0 {
			size += clip.PacketSize()
		}
	}
	return size
}

func (p *PlaylistFile) TotalAngleSize() uint64 {
	var size uint64
	for _, clip := range p.StreamClips {
		size += clip.PacketSize()
	}
	return size
}

func (p *PlaylistFile) TotalBitRate() uint64 {
	if p.TotalLength() > 0 {
		return uint64(float64(p.TotalSize()) * 8.0 / p.TotalLength())
	}
	return 0
}

func (p *PlaylistFile) TotalAngleBitRate() uint64 {
	if p.TotalAngleLength() > 0 {
		return uint64(float64(p.TotalAngleSize()) * 8.0 / p.TotalAngleLength())
	}
	return 0
}

func (p *PlaylistFile) Scan(streamFiles map[string]*StreamFile, clipFiles map[string]*StreamClipFile) error {
	if p.FileInfo == nil {
		return fmt.Errorf("playlist file missing")
	}
	f, err := p.FileInfo.OpenRead()
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return err
	}
	pos := 0
	p.FileType = util.ReadString(data, 8, &pos)
	if p.FileType != "MPLS0100" && p.FileType != "MPLS0200" && p.FileType != "MPLS0300" {
		return fmt.Errorf("playlist %s has unknown file type %s", p.Name, p.FileType)
	}
	playlistOffset := int(util.ReadUint32(data, &pos))
	chaptersOffset := int(util.ReadUint32(data, &pos))
	_ = util.ReadUint32(data, &pos) // extensions offset

	pos = 0x38
	if pos < len(data) {
		miscFlags := data[pos]
		p.MVCBaseViewR = (miscFlags & 0x10) != 0
	}

	pos = playlistOffset
	_ = util.ReadUint32(data, &pos) // playlist length
	_ = util.ReadUint16(data, &pos) // reserved
	itemCount := int(util.ReadUint16(data, &pos))
	_ = util.ReadUint16(data, &pos) // subitem count

	chapterClips := []*StreamClip{}
	for range itemCount {
		itemStart := pos
		itemLength := int(util.ReadUint16(data, &pos))
		itemName := util.ReadString(data, 5, &pos)
		_ = util.ReadString(data, 4, &pos) // item type

		streamFileName := strings.ToUpper(fmt.Sprintf("%s.M2TS", itemName))
		streamFile := streamFiles[streamFileName]
		if streamFile == nil {
			// keep scanning, but skip if missing
		}

		clipFileName := strings.ToUpper(fmt.Sprintf("%s.CLPI", itemName))
		clipFile := clipFiles[clipFileName]
		if clipFile == nil {
			return fmt.Errorf("playlist %s missing clip file %s", p.Name, clipFileName)
		}

		pos += 1
		multiangle := (data[pos] >> 4) & 0x01
		_ = data[pos] & 0x0F
		pos += 2

		inTime := int32(util.ReadUint32(data, &pos))
		if inTime < 0 {
			inTime &= 0x7fffffff
		}
		timeIn := float64(inTime) / 45000.0

		outTime := int32(util.ReadUint32(data, &pos))
		if outTime < 0 {
			outTime &= 0x7fffffff
		}
		timeOut := float64(outTime) / 45000.0

		clip := NewStreamClip(streamFile, clipFile, p.Settings)
		clip.Name = streamFileName
		clip.TimeIn = timeIn
		clip.TimeOut = timeOut
		clip.Length = clip.TimeOut - clip.TimeIn
		clip.RelativeTimeIn = p.TotalLength()
		clip.RelativeTimeOut = clip.RelativeTimeIn + clip.Length
		if p.TotalLength() > 0 {
			clip.RelativeLength = clip.Length / p.TotalLength()
		}
		p.StreamClips = append(p.StreamClips, clip)
		chapterClips = append(chapterClips, clip)

		pos += 12
		if multiangle > 0 {
			angles := int(data[pos])
			pos += 2
			for angle := 0; angle < angles-1; angle++ {
				angleName := util.ReadString(data, 5, &pos)
				_ = util.ReadString(data, 4, &pos)
				pos += 1

				angleFileName := strings.ToUpper(fmt.Sprintf("%s.M2TS", angleName))
				angleFile := streamFiles[angleFileName]
				if angleFile == nil {
					return fmt.Errorf("playlist %s missing angle file %s", p.Name, angleFileName)
				}
				angleClipName := strings.ToUpper(fmt.Sprintf("%s.CLPI", angleName))
				angleClipFile := clipFiles[angleClipName]
				if angleClipFile == nil {
					return fmt.Errorf("playlist %s missing angle clip %s", p.Name, angleClipName)
				}

				angleClip := NewStreamClip(angleFile, angleClipFile, p.Settings)
				angleClip.AngleIndex = angle + 1
				angleClip.TimeIn = clip.TimeIn
				angleClip.TimeOut = clip.TimeOut
				angleClip.RelativeTimeIn = clip.RelativeTimeIn
				angleClip.RelativeTimeOut = clip.RelativeTimeOut
				angleClip.Length = clip.Length
				p.StreamClips = append(p.StreamClips, angleClip)
			}
			if angles-1 > p.AngleCount {
				p.AngleCount = angles - 1
			}
		}

		_ = util.ReadUint16(data, &pos) // stream info length
		pos += 2
		streamCountVideo := int(data[pos])
		pos++
		streamCountAudio := int(data[pos])
		pos++
		streamCountPG := int(data[pos])
		pos++
		streamCountIG := int(data[pos])
		pos++
		streamCountSecondaryAudio := int(data[pos])
		pos++
		streamCountSecondaryVideo := int(data[pos])
		pos++
		streamCountPIP := int(data[pos])
		pos++
		pos += 5

		for range streamCountVideo {
			st := createPlaylistStream(data, &pos)
			if st != nil {
				pid := st.Base().PID
				if _, ok := p.PlaylistStreams[pid]; !ok || clip.RelativeLength > 0.01 {
					p.PlaylistStreams[pid] = st
				}
			}
		}
		for range streamCountAudio {
			st := createPlaylistStream(data, &pos)
			if st != nil {
				pid := st.Base().PID
				if _, ok := p.PlaylistStreams[pid]; !ok || clip.RelativeLength > 0.01 {
					p.PlaylistStreams[pid] = st
				}
			}
		}
		for range streamCountPG {
			st := createPlaylistStream(data, &pos)
			if st != nil {
				pid := st.Base().PID
				if _, ok := p.PlaylistStreams[pid]; !ok || clip.RelativeLength > 0.01 {
					p.PlaylistStreams[pid] = st
				}
			}
		}
		for range streamCountIG {
			st := createPlaylistStream(data, &pos)
			if st != nil {
				pid := st.Base().PID
				if _, ok := p.PlaylistStreams[pid]; !ok || clip.RelativeLength > 0.01 {
					p.PlaylistStreams[pid] = st
				}
			}
		}
		for range streamCountSecondaryAudio {
			st := createPlaylistStream(data, &pos)
			if st != nil {
				pid := st.Base().PID
				if _, ok := p.PlaylistStreams[pid]; !ok || clip.RelativeLength > 0.01 {
					p.PlaylistStreams[pid] = st
				}
			}
			pos += 2
		}
		for range streamCountSecondaryVideo {
			st := createPlaylistStream(data, &pos)
			if st != nil {
				pid := st.Base().PID
				if _, ok := p.PlaylistStreams[pid]; !ok || clip.RelativeLength > 0.01 {
					p.PlaylistStreams[pid] = st
				}
			}
			pos += 6
		}
		for range streamCountPIP {
			_ = createPlaylistStream(data, &pos)
		}

		pos = itemStart + itemLength + 2
	}

	pos = chaptersOffset + 4
	if pos+2 <= len(data) {
		chapterCount := int(util.ReadUint16(data, &pos))
		for range chapterCount {
			if pos+8 > len(data) {
				break
			}
			chapterType := int(data[pos+1])
			if chapterType == 1 {
				streamIndex := int(data[pos+2])<<8 | int(data[pos+3])
				chapterTime := int32(data[pos+4])<<24 | int32(data[pos+5])<<16 | int32(data[pos+6])<<8 | int32(data[pos+7])
				if streamIndex >= 0 && streamIndex < len(chapterClips) {
					clip := chapterClips[streamIndex]
					chapterSeconds := float64(chapterTime) / 45000.0
					relativeSeconds := chapterSeconds - clip.TimeIn + clip.RelativeTimeIn
					if p.TotalLength()-relativeSeconds > 1.0 {
						clip.Chapters = append(clip.Chapters, chapterSeconds)
						p.Chapters = append(p.Chapters, relativeSeconds)
					}
				}
			}
			pos += 14
		}
	}

	p.loadStreamClips()
	p.IsInitialized = true
	return nil
}

func (p *PlaylistFile) Initialize() {
	p.loadStreamClips()

	clipTimes := map[string][]float64{}
	for _, clip := range p.StreamClips {
		if clip.AngleIndex != 0 {
			continue
		}
		if clip.Name == "" {
			continue
		}
		if times, ok := clipTimes[clip.Name]; ok {
			if slices.Contains(times, clip.TimeIn) {
				p.HasLoops = true
			}
			clipTimes[clip.Name] = append(times, clip.TimeIn)
		} else {
			clipTimes[clip.Name] = []float64{clip.TimeIn}
		}
	}
	p.IsInitialized = true
}

func (p *PlaylistFile) ClearBitrates() {
	for _, clip := range p.StreamClips {
		clip.PayloadBytes = 0
		clip.PacketCount = 0
		clip.PacketSeconds = 0
		if clip.StreamFile == nil {
			continue
		}
		for _, st := range clip.StreamFile.Streams {
			st.Base().PayloadBytes = 0
			st.Base().PacketCount = 0
			st.Base().PacketSeconds = 0
		}
		if clip.StreamFile.StreamDiagnostics != nil {
			for pid := range clip.StreamFile.StreamDiagnostics {
				delete(clip.StreamFile.StreamDiagnostics, pid)
			}
		}
	}

	for _, st := range p.SortedStreams {
		st.Base().PayloadBytes = 0
		st.Base().PacketCount = 0
		st.Base().PacketSeconds = 0
	}
}

func (p *PlaylistFile) IsValid() bool {
	if !p.IsInitialized {
		return false
	}
	if p.Settings.FilterShortPlaylists && p.TotalLength() < float64(p.Settings.FilterShortPlaylistsVal) {
		return false
	}
	return !(p.HasLoops && p.Settings.FilterLoopingPlaylists)
}

func (p *PlaylistFile) loadStreamClips() {
	p.AngleClips = nil
	if p.AngleCount > 0 {
		p.AngleClips = make([]map[float64]*StreamClip, p.AngleCount)
		for i := 0; i < p.AngleCount; i++ {
			p.AngleClips[i] = make(map[float64]*StreamClip)
		}
	}

	var reference *StreamClip
	if len(p.StreamClips) > 0 {
		reference = p.StreamClips[0]
	}
	for _, clip := range p.StreamClips {
		if reference == nil || (reference.StreamFile == nil && clip.StreamFile != nil) {
			reference = clip
		}
		if reference != nil && clip.StreamClipFile != nil && reference.StreamClipFile != nil {
			if len(clip.StreamClipFile.Streams) > len(reference.StreamClipFile.Streams) && clip.RelativeLength > 0.01 {
				reference = clip
			}
		}
		if clip.Length > reference.Length && clip.StreamFile != nil {
			reference = clip
		}

		if p.AngleCount > 0 {
			if clip.AngleIndex == 0 {
				for angleIndex := 0; angleIndex < p.AngleCount; angleIndex++ {
					p.AngleClips[angleIndex][clip.RelativeTimeIn] = clip
				}
			} else {
				p.AngleClips[clip.AngleIndex-1][clip.RelativeTimeIn] = clip
			}
		}
	}

	if reference == nil || reference.StreamClipFile == nil {
		return
	}

	if p.Streams == nil {
		p.Streams = make(map[uint16]stream.Info)
	}

	for pid, clipStream := range reference.StreamClipFile.Streams {
		if _, ok := p.Streams[pid]; ok {
			continue
		}
		streamClone := clipStream.Clone()
		p.Streams[pid] = streamClone
		if !p.IsCustom {
			if _, ok := p.PlaylistStreams[pid]; !ok {
				streamClone.Base().IsHidden = true
				p.HasHiddenTracks = true
			}
		}
	}

	if reference.StreamFile != nil {
		if p.Settings.EnableSSIF && reference.StreamFile.InterleavedFile != nil {
			if ssifStream, ok := reference.StreamFile.Streams[4114]; ok {
				if _, exists := p.Streams[4114]; !exists {
					clone := ssifStream.Clone()
					p.Streams[4114] = clone
				}
			}
		}
		for pid, clipStream := range reference.StreamFile.Streams {
			if existing, ok := p.Streams[pid]; ok {
				if existing.Base().StreamType != clipStream.Base().StreamType {
					continue
				}
				if clipStream.Base().BitRate > existing.Base().BitRate {
					existing.Base().BitRate = clipStream.Base().BitRate
				}
				existing.Base().IsVBR = clipStream.Base().IsVBR

				switch ex := existing.(type) {
				case *stream.VideoStream:
					if cs, ok := clipStream.(*stream.VideoStream); ok {
						ex.EncodingProfile = cs.EncodingProfile
						ex.ExtendedData = cs.ExtendedData
					}
				case *stream.AudioStream:
					if cs, ok := clipStream.(*stream.AudioStream); ok {
						if cs.ChannelCount > ex.ChannelCount {
							ex.ChannelCount = cs.ChannelCount
						}
						if cs.LFE > ex.LFE {
							ex.LFE = cs.LFE
						}
						if cs.SampleRate > ex.SampleRate {
							ex.SampleRate = cs.SampleRate
						}
						if cs.BitDepth > ex.BitDepth {
							ex.BitDepth = cs.BitDepth
						}
						ex.DialNorm = cs.DialNorm
						ex.HasExtensions = cs.HasExtensions
						ex.AudioMode = cs.AudioMode
						ex.CoreStream = cs.CoreStream
						ex.ExtendedData = cs.ExtendedData
					}
				case *stream.GraphicsStream:
					if cs, ok := clipStream.(*stream.GraphicsStream); ok {
						ex.Captions = cs.Captions
						ex.ForcedCaptions = cs.ForcedCaptions
						ex.Width = cs.Width
						ex.Height = cs.Height
						ex.CaptionIDs = cs.CaptionIDs
						ex.LastFrame = cs.LastFrame
					}
				}
			}
		}
	}

	p.VideoStreams = p.VideoStreams[:0]
	p.AudioStreams = p.AudioStreams[:0]
	p.GraphicsStreams = p.GraphicsStreams[:0]
	p.TextStreams = p.TextStreams[:0]
	p.SortedStreams = p.SortedStreams[:0]

	for _, streamInfo := range p.Streams {
		switch st := streamInfo.(type) {
		case *stream.VideoStream:
			p.VideoStreams = append(p.VideoStreams, st)
		case *stream.AudioStream:
			p.AudioStreams = append(p.AudioStreams, st)
		case *stream.GraphicsStream:
			p.GraphicsStreams = append(p.GraphicsStreams, st)
		case *stream.TextStream:
			p.TextStreams = append(p.TextStreams, st)
		}
	}

	p.AngleStreams = nil
	if p.AngleCount > 0 {
		p.AngleStreams = make([]map[uint16]stream.Info, p.AngleCount)
		for i := 0; i < p.AngleCount; i++ {
			p.AngleStreams[i] = make(map[uint16]stream.Info)
		}
	}

	if !p.Settings.KeepStreamOrder {
		sort.Slice(p.VideoStreams, func(i, j int) bool {
			return compareVideoStreams(p.VideoStreams[i], p.VideoStreams[j]) < 0
		})
		sort.Slice(p.AudioStreams, func(i, j int) bool {
			return compareAudioStreams(p.AudioStreams[i], p.AudioStreams[j]) < 0
		})
		sort.Slice(p.GraphicsStreams, func(i, j int) bool {
			return compareGraphicsStreams(p.GraphicsStreams[i], p.GraphicsStreams[j]) < 0
		})
		sort.Slice(p.TextStreams, func(i, j int) bool {
			return compareTextStreams(p.TextStreams[i], p.TextStreams[j]) < 0
		})
	}

	for _, st := range p.VideoStreams {
		p.SortedStreams = append(p.SortedStreams, st)
		for i := 0; i < p.AngleCount; i++ {
			clone := st.Clone()
			clone.Base().AngleIndex = i + 1
			p.AngleStreams[i][clone.Base().PID] = clone
			p.SortedStreams = append(p.SortedStreams, clone)
		}
	}
	for _, st := range p.AudioStreams {
		p.SortedStreams = append(p.SortedStreams, st)
	}
	for _, st := range p.GraphicsStreams {
		p.SortedStreams = append(p.SortedStreams, st)
	}
	for _, st := range p.TextStreams {
		p.SortedStreams = append(p.SortedStreams, st)
	}

	p.updateVBRBitrates()
}

func (p *PlaylistFile) updateVBRBitrates() {
	packetSeconds := 0.0
	for _, clip := range p.StreamClips {
		if clip.AngleIndex == 0 {
			packetSeconds += clip.PacketSeconds
		}
	}
	if packetSeconds <= 0 {
		return
	}
	for _, playlistStream := range p.SortedStreams {
		if playlistStream.Base().IsVBR {
			playlistStream.Base().BitRate = int64(math.RoundToEven(float64(playlistStream.Base().PayloadBytes) * 8.0 / packetSeconds))
		}
	}
}

func createPlaylistStream(data []byte, pos *int) stream.Info {
	headerLength := int(data[*pos])
	*pos += 1
	headerPos := *pos
	headerType := int(data[*pos])
	*pos += 1

	pid := 0
	switch headerType {
	case 1:
		pid = int(util.ReadUint16(data, pos))
	case 2:
		*pos += 2
		pid = int(util.ReadUint16(data, pos))
	case 3:
		*pos += 1
		pid = int(util.ReadUint16(data, pos))
	case 4:
		*pos += 2
		pid = int(util.ReadUint16(data, pos))
	default:
		pid = int(util.ReadUint16(data, pos))
	}
	*pos = headerPos + headerLength

	streamLength := int(data[*pos])
	*pos += 1
	streamPos := *pos

	streamType := stream.StreamType(data[*pos])
	*pos += 1
	var st stream.Info
	switch streamType {
	case stream.StreamTypeHEVCVideo, stream.StreamTypeAVCVideo, stream.StreamTypeMPEG1Video, stream.StreamTypeMPEG2Video, stream.StreamTypeVC1Video:
		videoFormat := stream.VideoFormat(data[*pos] >> 4)
		frameRate := stream.FrameRate(data[*pos] & 0x0F)
		aspectRatio := stream.AspectRatio(data[*pos+1] >> 4)
		vs := &stream.VideoStream{}
		vs.StreamType = streamType
		vs.SetVideoFormat(videoFormat)
		vs.SetFrameRate(frameRate)
		vs.AspectRatio = aspectRatio
		st = vs
	case stream.StreamTypeAC3Audio, stream.StreamTypeAC3PlusAudio, stream.StreamTypeAC3PlusSecondaryAudio,
		stream.StreamTypeAC3TrueHDAudio, stream.StreamTypeDTSAudio, stream.StreamTypeDTSHDAudio,
		stream.StreamTypeDTSHDMasterAudio, stream.StreamTypeDTSHDSecondaryAudio,
		stream.StreamTypeLPCMAudio, stream.StreamTypeMPEG1Audio, stream.StreamTypeMPEG2Audio,
		stream.StreamTypeMPEG2AACAudio, stream.StreamTypeMPEG4AACAudio:
		audioFormat := data[*pos]
		*pos++
		channelLayout := stream.ChannelLayout(audioFormat >> 4)
		sampleRate := stream.SampleRate(audioFormat & 0x0F)
		audioLanguage := util.ReadString(data, 3, pos)
		as := &stream.AudioStream{}
		as.StreamType = streamType
		as.ChannelLayout = channelLayout
		as.SampleRate = stream.ConvertSampleRate(sampleRate)
		as.SetLanguageCode(audioLanguage)
		st = as
	case stream.StreamTypeInteractiveGraphics, stream.StreamTypePresentationGraphics:
		graphicsLanguage := util.ReadString(data, 3, pos)
		gs := stream.NewGraphicsStream()
		gs.StreamType = streamType
		gs.SetLanguageCode(graphicsLanguage)
		st = gs
	case stream.StreamTypeSubtitle:
		_ = util.ReadByte(data, pos)
		textLanguage := util.ReadString(data, 3, pos)
		ts := stream.NewTextStream()
		ts.StreamType = streamType
		ts.SetLanguageCode(textLanguage)
		st = ts
	}

	*pos = streamPos + streamLength
	if st == nil {
		return nil
	}
	st.Base().PID = uint16(pid)
	st.Base().StreamType = streamType
	return st
}

func compareAudioStreams(x, y *stream.AudioStream) int {
	if x == nil && y == nil {
		return 0
	}
	if x == nil {
		return -1
	}
	if y == nil {
		return 1
	}
	if x.ChannelCount > y.ChannelCount {
		return -1
	}
	if y.ChannelCount > x.ChannelCount {
		return 1
	}
	sortX := streamTypeSortIndex(x.StreamType)
	sortY := streamTypeSortIndex(y.StreamType)
	if sortX > sortY {
		return -1
	}
	if sortY > sortX {
		return 1
	}
	xEng := x.LanguageCode() == "eng"
	yEng := y.LanguageCode() == "eng"
	if xEng && !yEng {
		return -1
	}
	if yEng && !xEng {
		return 1
	}
	if x.LanguageCode() != y.LanguageCode() {
		return strings.Compare(x.LanguageName, y.LanguageName)
	}
	if x.PID < y.PID {
		return -1
	}
	if y.PID < x.PID {
		return 1
	}
	return 0
}

func compareVideoStreams(x, y *stream.VideoStream) int {
	if x == nil && y == nil {
		return 0
	}
	if x == nil {
		return 1
	}
	if y == nil {
		return -1
	}
	if x.Height > y.Height {
		return -1
	}
	if y.Height > x.Height {
		return 1
	}
	if x.PID > y.PID {
		return 1
	}
	if y.PID > x.PID {
		return -1
	}
	return 0
}

func compareGraphicsStreams(x, y *stream.GraphicsStream) int {
	if x == nil && y == nil {
		return 0
	}
	if x == nil {
		return -1
	}
	if y == nil {
		return 1
	}
	sortX := streamTypeSortIndex(x.StreamType)
	sortY := streamTypeSortIndex(y.StreamType)
	if sortX > sortY {
		return -1
	}
	if sortY > sortX {
		return 1
	}
	xEng := x.LanguageCode() == "eng"
	yEng := y.LanguageCode() == "eng"
	if xEng && !yEng {
		return -1
	}
	if yEng && !xEng {
		return 1
	}
	if x.LanguageCode() != y.LanguageCode() {
		return strings.Compare(x.LanguageName, y.LanguageName)
	}
	if x.PID > y.PID {
		return 1
	}
	if y.PID > x.PID {
		return -1
	}
	return 0
}

func compareTextStreams(x, y *stream.TextStream) int {
	if x == nil && y == nil {
		return 0
	}
	if x == nil {
		return -1
	}
	if y == nil {
		return 1
	}
	xEng := x.LanguageCode() == "eng"
	yEng := y.LanguageCode() == "eng"
	if xEng && !yEng {
		return -1
	}
	if yEng && !xEng {
		return 1
	}
	if x.LanguageCode() != y.LanguageCode() {
		return strings.Compare(x.LanguageName, y.LanguageName)
	}
	if x.PID > y.PID {
		return 1
	}
	if y.PID > x.PID {
		return -1
	}
	return 0
}

func streamTypeSortIndex(streamType stream.StreamType) int {
	switch streamType {
	case stream.StreamTypeUnknown:
		return 0
	case stream.StreamTypeMPEG1Video:
		return 1
	case stream.StreamTypeMPEG2Video:
		return 2
	case stream.StreamTypeAVCVideo:
		return 3
	case stream.StreamTypeVC1Video:
		return 4
	case stream.StreamTypeMVCVideo:
		return 5
	case stream.StreamTypeHEVCVideo:
		return 6
	case stream.StreamTypeMPEG1Audio:
		return 1
	case stream.StreamTypeMPEG2Audio:
		return 2
	case stream.StreamTypeAC3PlusSecondaryAudio:
		return 3
	case stream.StreamTypeDTSHDSecondaryAudio:
		return 4
	case stream.StreamTypeAC3Audio:
		return 5
	case stream.StreamTypeDTSAudio:
		return 6
	case stream.StreamTypeAC3PlusAudio:
		return 7
	case stream.StreamTypeMPEG2AACAudio:
		return 8
	case stream.StreamTypeMPEG4AACAudio:
		return 9
	case stream.StreamTypeDTSHDAudio:
		return 10
	case stream.StreamTypeAC3TrueHDAudio:
		return 11
	case stream.StreamTypeDTSHDMasterAudio:
		return 12
	case stream.StreamTypeLPCMAudio:
		return 13
	case stream.StreamTypeSubtitle:
		return 1
	case stream.StreamTypeInteractiveGraphics:
		return 2
	case stream.StreamTypePresentationGraphics:
		return 3
	default:
		return 0
	}
}
