package codec

import (
	"fmt"
	"math"

	"github.com/autobrr/go-bdinfo/internal/buffer"
	"github.com/autobrr/go-bdinfo/internal/settings"
	"github.com/autobrr/go-bdinfo/internal/stream"
)

const (
	hevcNALUnitTypeVPS       = 32
	hevcNALUnitTypeSPS       = 33
	hevcNALUnitTypePrefixSEI = 39
	hevcNALUnitTypeSuffixSEI = 40
)

const (
	// Defensive bounds for malformed Exp-Golomb values in fuzzed streams.
	// Real HEVC bitstreams stay far below these.
	hevcMaxShortTermRefPicSets = 64
	hevcMaxLongTermRefPicsSps  = 64
	hevcMaxRefPics             = 512
)

const (
	seiMasteringDisplayColourVolume       = 137
	seiContentLightLevel                  = 144
	seiAlternativeTransferCharacteristics = 147
	seiUserDataRegisteredITUT35           = 4
)

type hevcVUI struct {
	videoSignalTypePresent   bool
	videoFullRangeFlag       byte
	colourDescriptionPresent bool
	colourPrimaries          byte
	transferCharacteristics  byte
	matrixCoefficients       byte
}

func ScanHEVC(v *stream.VideoStream, data []byte, settings settings.Settings) {
	if v.IsInitialized {
		return
	}
	if len(data) < 4 {
		return
	}

	ext := &stream.HEVCExtendedData{}
	v.ExtendedData = ext

	var chromaFormat string
	bitDepth := 0
	var masteringDisplayColorPrimaries string
	var masteringDisplayLuminance string
	var maxCLL uint32
	var maxFALL uint32
	lightLevelAvailable := false
	preferredTransferCharacteristics := byte(2)
	isHDR10Plus := false
	var vui hevcVUI
	vuiPresent := false
	bitDepthMatch := false
	spsFound := false

	nalUnits := findNALUnits(data)
	for _, nal := range nalUnits {
		if len(nal) < 3 {
			continue
		}
		nalType := (nal[0] >> 1) & 0x3F
		switch nalType {
		case hevcNALUnitTypeVPS:
			// ignore for now
		case hevcNALUnitTypeSPS:
			rbsp := RemoveEmulationBytes(nal[2:])
			br := buffer.NewBitReader(rbsp)
			_, _ = br.ReadBits(4) // sps_video_parameter_set_id
			maxSubLayersMinus1, _ := br.ReadBits(3)
			_, _ = br.ReadBits(1) // sps_temporal_id_nesting_flag
			profile := parseHEVCProfileTierLevel(br, int(maxSubLayersMinus1))
			if profile != "" {
				v.EncodingProfile = profile
			}

			_, _ = br.ReadExpGolomb() // sps_seq_parameter_set_id
			chromaFormatIDC, _ := br.ReadExpGolomb()
			if chromaFormatIDC == 3 {
				_, _ = br.ReadBits(1)
			}
			picWidth, _ := br.ReadExpGolomb()
			picHeight, _ := br.ReadExpGolomb()
			width := int(picWidth)
			height := int(picHeight)

			confWinFlag, _ := br.ReadBits(1)
			if confWinFlag == 1 {
				confWinLeft, _ := br.ReadExpGolomb()
				confWinRight, _ := br.ReadExpGolomb()
				confWinTop, _ := br.ReadExpGolomb()
				confWinBottom, _ := br.ReadExpGolomb()
				subWidthC := 1
				subHeightC := 1
				if chromaFormatIDC == 1 {
					subWidthC = 2
					subHeightC = 2
				} else if chromaFormatIDC == 2 {
					subWidthC = 2
					subHeightC = 1
				}
				width -= subWidthC * int(confWinLeft+confWinRight)
				height -= subHeightC * int(confWinTop+confWinBottom)
			}

			bitDepthLumaMinus8, _ := br.ReadExpGolomb()
			bitDepthChromaMinus8, _ := br.ReadExpGolomb()
			bitDepth = int(bitDepthLumaMinus8 + 8)
			bitDepthMatch = bitDepthLumaMinus8 == bitDepthChromaMinus8
			log2MaxPicOrderCntLsbMinus4, _ := br.ReadExpGolomb()
			vui, vuiPresent = parseHEVCSPSVUI(br, maxSubLayersMinus1, uint64(log2MaxPicOrderCntLsbMinus4))

			if width > 0 {
				v.Width = width
			}
			if height > 0 {
				v.Height = height
			}

			switch chromaFormatIDC {
			case 1:
				chromaFormat = "4:2:0"
			case 2:
				chromaFormat = "4:2:2"
			case 3:
				chromaFormat = "4:4:4"
			}

			spsFound = true
		case hevcNALUnitTypePrefixSEI, hevcNALUnitTypeSuffixSEI:
			rbsp := RemoveEmulationBytes(nal[2:])
			parseHEVCSEI(rbsp, &masteringDisplayColorPrimaries, &masteringDisplayLuminance, &maxCLL, &maxFALL, &lightLevelAvailable, &preferredTransferCharacteristics, &isHDR10Plus)
		}
	}

	if chromaFormat != "" && settings.ExtendedStreamDiagnostics {
		ext.ExtendedFormatInfo = append(ext.ExtendedFormatInfo, chromaFormat)
	}
	if bitDepth > 0 && bitDepthMatch {
		ext.ExtendedFormatInfo = append(ext.ExtendedFormatInfo, fmt.Sprintf("%d bits", bitDepth))
	}
	if bitDepth == 10 && chromaFormat == "4:2:0" &&
		vuiPresent &&
		vui.videoSignalTypePresent &&
		vui.colourDescriptionPresent &&
		vui.colourPrimaries == 9 &&
		vui.transferCharacteristics == 16 &&
		(vui.matrixCoefficients == 9 || vui.matrixCoefficients == 10) &&
		masteringDisplayColorPrimaries != "" {
		hdr := "HDR10"
		if isHDR10Plus {
			hdr = "HDR10+"
		}
		if v.PID >= 4117 {
			hdr = "Dolby Vision"
		}
		ext.ExtendedFormatInfo = append(ext.ExtendedFormatInfo, hdr)
	}

	if vuiPresent && vui.videoSignalTypePresent {
		if settings.ExtendedStreamDiagnostics {
			if vui.videoFullRangeFlag == 1 {
				ext.ExtendedFormatInfo = append(ext.ExtendedFormatInfo, "Full Range")
			} else {
				ext.ExtendedFormatInfo = append(ext.ExtendedFormatInfo, "Limited Range")
			}
		}
		if vui.colourDescriptionPresent {
			if primaries := hevcColourPrimaries(vui.colourPrimaries); primaries != "" {
				ext.ExtendedFormatInfo = append(ext.ExtendedFormatInfo, primaries)
			}
			if settings.ExtendedStreamDiagnostics {
				if transfer := hevcTransferCharacteristics(vui.transferCharacteristics); transfer != "" {
					ext.ExtendedFormatInfo = append(ext.ExtendedFormatInfo, transfer)
				}
				if matrix := hevcMatrixCoefficients(vui.matrixCoefficients); matrix != "" {
					ext.ExtendedFormatInfo = append(ext.ExtendedFormatInfo, matrix)
				}
			}
		}
	}

	if settings.ExtendedStreamDiagnostics {
		if masteringDisplayColorPrimaries != "" {
			ext.ExtendedFormatInfo = append(ext.ExtendedFormatInfo, "Mastering display color primaries: "+masteringDisplayColorPrimaries)
		}
		if masteringDisplayLuminance != "" {
			ext.ExtendedFormatInfo = append(ext.ExtendedFormatInfo, "Mastering display luminance: "+masteringDisplayLuminance)
		}
		if lightLevelAvailable && maxCLL > 0 {
			ext.ExtendedFormatInfo = append(ext.ExtendedFormatInfo, fmt.Sprintf("Maximum Content Light Level: %d cd / m2", maxCLL))
			ext.ExtendedFormatInfo = append(ext.ExtendedFormatInfo, fmt.Sprintf("Maximum Frame-Average Light Level: %d cd/m2", maxFALL))
		}
	}

	_ = preferredTransferCharacteristics

	if spsFound {
		v.IsVBR = true
		v.IsInitialized = true
	}
}

func parseHEVCSPSVUI(br *buffer.BitReader, maxSubLayersMinus1 uint64, log2MaxPicOrderCntLsbMinus4 uint64) (hevcVUI, bool) {
	readBool := func() bool {
		v, _ := br.ReadBit()
		return v == 1
	}
	readUE := func() uint64 {
		v, _ := br.ReadUE()
		return v
	}
	skipExpMulti := func(n int) {
		for range n {
			_ = readUE()
		}
	}

	spsSubLayerOrderingInfoPresentFlag := readBool()
	start := uint64(0)
	if !spsSubLayerOrderingInfoPresentFlag {
		start = maxSubLayersMinus1
	}
	for subLayerPos := start; subLayerPos <= maxSubLayersMinus1; subLayerPos++ {
		skipExpMulti(3)
	}
	skipExpMulti(6)

	if readBool() { // scaling_list_enabled_flag
		if readBool() { // sps_scaling_list_data_present_flag
			skipHEVCScalingListData(br)
		}
	}

	_ = br.SkipBits(2) // amp_enabled_flag, sample_adaptive_offset_enabled_flag
	if readBool() {    // pcm_enabled_flag
		_ = br.SkipBits(8)
		skipExpMulti(2)
		_ = br.SkipBits(1)
	}

	numShortTermRefPicSets := readUE()
	if numShortTermRefPicSets > hevcMaxShortTermRefPicSets {
		numShortTermRefPicSets = hevcMaxShortTermRefPicSets
	}
	skipHEVCShortTermRefPicSets(br, numShortTermRefPicSets)
	if readBool() { // long_term_ref_pics_present_flag
		numLongTermRefPicsSps := readUE()
		if numLongTermRefPicsSps > hevcMaxLongTermRefPicsSps {
			numLongTermRefPicsSps = hevcMaxLongTermRefPicsSps
		}
		for range numLongTermRefPicsSps {
			_ = br.SkipBits(int(log2MaxPicOrderCntLsbMinus4 + 4))
			_ = br.SkipBits(1)
		}
	}

	_ = br.SkipBits(2) // sps_temporal_mvp_enabled_flag, strong_intra_smoothing_enabled_flag
	if !readBool() {   // vui_parameters_present_flag
		return hevcVUI{}, false
	}

	return parseHEVCVUI(br), true
}

func parseHEVCVUI(br *buffer.BitReader) hevcVUI {
	readBool := func() bool {
		v, _ := br.ReadBit()
		return v == 1
	}
	readBits := func(n int) uint64 {
		v, _ := br.ReadBits(n)
		return v
	}
	vui := hevcVUI{}

	if readBool() { // aspect_ratio_info_present_flag
		aspectRatioIDC := readBits(8)
		if aspectRatioIDC == 0xFF {
			_ = readBits(16)
			_ = readBits(16)
		}
	}
	if readBool() { // overscan_info_present_flag
		_ = readBits(1)
	}
	vui.videoSignalTypePresent = readBool()
	if vui.videoSignalTypePresent {
		_ = readBits(3) // video_format
		vui.videoFullRangeFlag = byte(readBits(1))
		vui.colourDescriptionPresent = readBool()
		if vui.colourDescriptionPresent {
			vui.colourPrimaries = byte(readBits(8))
			vui.transferCharacteristics = byte(readBits(8))
			vui.matrixCoefficients = byte(readBits(8))
		}
	}
	return vui
}

func skipHEVCShortTermRefPicSets(br *buffer.BitReader, numShortTermRefPicSets uint64) {
	readBool := func() bool {
		v, _ := br.ReadBit()
		return v == 1
	}
	readUE := func() uint64 {
		v, _ := br.ReadUE()
		return v
	}

	if numShortTermRefPicSets > hevcMaxShortTermRefPicSets {
		numShortTermRefPicSets = hevcMaxShortTermRefPicSets
	}
	numPics := uint64(0)
	for stRpsIdx := range numShortTermRefPicSets {
		interRefPicSetPredictionFlag := false
		if stRpsIdx > 0 {
			interRefPicSetPredictionFlag = readBool()
		}
		if interRefPicSetPredictionFlag {
			deltaIdxMinus1 := uint64(0)
			if stRpsIdx == numShortTermRefPicSets {
				deltaIdxMinus1 = readUE()
			}
			if deltaIdxMinus1+1 > stRpsIdx {
				return
			}
			_ = br.SkipBits(1)
			_ = readUE()
			numPicsNew := uint64(0)
			if numPics > hevcMaxRefPics {
				numPics = hevcMaxRefPics
			}
			for picPos := uint64(0); picPos <= numPics; picPos++ {
				usedByCurrPic := readBool()
				useDeltaFlag := false
				if !usedByCurrPic {
					useDeltaFlag = readBool()
				}
				if usedByCurrPic || useDeltaFlag {
					numPicsNew++
				}
			}
			if numPicsNew > hevcMaxRefPics {
				numPicsNew = hevcMaxRefPics
			}
			numPics = numPicsNew
		} else {
			numNegativePics := readUE()
			numPositivePics := readUE()
			if numNegativePics > hevcMaxRefPics {
				numNegativePics = hevcMaxRefPics
			}
			if numPositivePics > hevcMaxRefPics {
				numPositivePics = hevcMaxRefPics
			}
			numPics = numNegativePics + numPositivePics
			if numPics > hevcMaxRefPics {
				numPics = hevcMaxRefPics
			}
			for range numNegativePics {
				_ = readUE()
				_ = br.SkipBits(1)
			}
			for range numPositivePics {
				_ = readUE()
				_ = br.SkipBits(1)
			}
		}
	}
}

func skipHEVCScalingListData(br *buffer.BitReader) {
	readBool := func() bool {
		v, _ := br.ReadBit()
		return v == 1
	}
	readUE := func() uint64 {
		v, _ := br.ReadUE()
		return v
	}

	for sizeID := range 4 {
		matrixCount := 6
		if sizeID == 3 {
			matrixCount = 2
		}
		for matrixID := 0; matrixID < matrixCount; matrixID++ {
			if !readBool() { // scaling_list_pred_mode_flag
				_ = readUE()
			} else {
				coefNum := min(1<<(4+(sizeID<<1)), 64)
				if sizeID > 1 {
					_ = readUE()
				}
				for i := 0; i < coefNum; i++ {
					_ = readUE()
				}
			}
		}
	}
}

func hevcColourPrimaries(colourPrimaries byte) string {
	switch colourPrimaries {
	case 1:
		return "BT.709"
	case 4:
		return "BT.470 System M"
	case 5:
		return "BT.601 PAL"
	case 6:
		return "BT.601 NTSC"
	case 7:
		return "SMPTE 240M"
	case 8:
		return "Generic film"
	case 9:
		return "BT.2020"
	case 10:
		return "XYZ"
	case 11:
		return "DCI P3"
	case 12:
		return "Display P3"
	case 22:
		return "EBU Tech 3213"
	default:
		return ""
	}
}

func hevcTransferCharacteristics(transferCharacteristics byte) string {
	switch transferCharacteristics {
	case 1:
		return "BT.709"
	case 4:
		return "BT.470 System M"
	case 5:
		return "BT.470 System B/G"
	case 6:
		return "BT.601"
	case 7:
		return "SMPTE 240M"
	case 8:
		return "Linear"
	case 9:
		return "Logarithmic (100:1)"
	case 10:
		return "Logarithmic (316.22777:1)"
	case 11:
		return "xvYCC"
	case 12:
		return "BT.1361"
	case 13:
		return "sRGB/sYCC"
	case 14:
		return "BT.2020 (10-bit)"
	case 15:
		return "BT.2020 (12-bit)"
	case 16:
		return "PQ"
	case 17:
		return "SMPTE 428M"
	case 18:
		return "HLG"
	default:
		return ""
	}
}

func hevcMatrixCoefficients(matrixCoefficients byte) string {
	switch matrixCoefficients {
	case 0:
		return "Identity"
	case 1:
		return "BT.709"
	case 4:
		return "FCC 73.682"
	case 5:
		return "BT.470 System B/G"
	case 6:
		return "BT.601"
	case 7:
		return "SMPTE 240M"
	case 8:
		return "YCgCo"
	case 9:
		return "BT.2020 non-constant"
	case 10:
		return "BT.2020 constant"
	case 11:
		return "Y'D'zD'x"
	case 12:
		return "Chromaticity-derived non-constant"
	case 13:
		return "Chromaticity-derived constant"
	case 14:
		return "ICtCp"
	default:
		return ""
	}
}

func parseHEVCProfileTierLevel(br *buffer.BitReader, maxSubLayersMinus1 int) string {
	_, _ = br.ReadBits(2) // general_profile_space
	tierFlag, _ := br.ReadBits(1)
	profileIDC, _ := br.ReadBits(5)
	_, _ = br.ReadBits(32) // compatibility flags
	_, _ = br.ReadBits(48) // constraint flags
	levelIDC, _ := br.ReadBits(8)

	profile := ""
	switch profileIDC {
	case 1:
		profile = "Main"
	case 2:
		profile = "Main 10"
	case 3:
		profile = "Main Still"
	default:
		profile = ""
	}

	if levelIDC > 0 {
		calcLevel := float64(levelIDC) / 30.0
		level := ""
		if math.Mod(calcLevel, 1.0) == 0 {
			level = fmt.Sprintf("%.0f", calcLevel)
		} else {
			level = fmt.Sprintf("%.1f", calcLevel)
		}
		tier := "Main"
		if tierFlag == 1 {
			tier = "High"
		}
		profile = fmt.Sprintf("%s @ Level %s @ %s", profile, level, tier)
	}

	if maxSubLayersMinus1 > 0 {
		for range maxSubLayersMinus1 {
			_, _ = br.ReadBits(1)
			_, _ = br.ReadBits(1)
		}
		if maxSubLayersMinus1 < 8 {
			_, _ = br.ReadBits(2 * (8 - maxSubLayersMinus1))
		}
		for range maxSubLayersMinus1 {
			_, _ = br.ReadBits(88)
			_, _ = br.ReadBits(8)
		}
	}

	return profile
}

func parseHEVCSEI(data []byte, primaries *string, luminance *string, maxCLL *uint32, maxFALL *uint32, lightLevel *bool, preferredTransfer *byte, hdr10plus *bool) {
	br := buffer.NewBitReader(data)
	for br.Position() < br.Length()-2 {
		payloadType := uint32(0)
		for {
			b, ok := br.ReadByteValue()
			if !ok {
				return
			}
			payloadType += uint32(b)
			if b != 0xFF {
				break
			}
		}

		payloadSize := uint32(0)
		for {
			b, ok := br.ReadByteValue()
			if !ok {
				return
			}
			payloadSize += uint32(b)
			if b != 0xFF {
				break
			}
		}

		startPos := br.Position()

		switch payloadType {
		case seiMasteringDisplayColourVolume:
			parseMasteringDisplayColorVolume(br, primaries, luminance)
		case seiContentLightLevel:
			parseContentLightLevel(br, maxCLL, maxFALL, lightLevel)
		case seiAlternativeTransferCharacteristics:
			parseAlternativeTransferCharacteristics(br, preferredTransfer)
		case seiUserDataRegisteredITUT35:
			parseUserDataRegisteredITUT35(br, payloadSize, hdr10plus)
		}

		consumed := br.Position() - startPos
		if consumed < int(payloadSize) {
			_ = br.SkipBits((int(payloadSize) - consumed) * 8)
		}
	}
}

func parseMasteringDisplayColorVolume(br *buffer.BitReader, primaries *string, luminance *string) {
	vals := make([]uint16, 8)
	for i := range 8 {
		v, _ := br.ReadBits(16)
		vals[i] = uint16(v)
	}
	maxLum, _ := br.ReadBits(32)
	minLum, _ := br.ReadBits(32)
	*primaries = formatMasteringDisplay(vals)
	*luminance = fmt.Sprintf("min: %.4f cd/m2, max: %.4f cd/m2", float64(minLum)/10000.0, float64(maxLum)/10000.0)
}

func parseContentLightLevel(br *buffer.BitReader, maxCLL *uint32, maxFALL *uint32, lightLevel *bool) {
	maxContent, _ := br.ReadBits(16)
	maxAvg, _ := br.ReadBits(16)
	*maxCLL = uint32(maxContent)
	*maxFALL = uint32(maxAvg)
	*lightLevel = true
}

func parseAlternativeTransferCharacteristics(br *buffer.BitReader, preferredTransfer *byte) {
	val, _ := br.ReadBits(8)
	*preferredTransfer = byte(val)
}

func parseUserDataRegisteredITUT35(br *buffer.BitReader, payloadSize uint32, hdr10plus *bool) {
	if payloadSize < 8 {
		return
	}
	countryCode, _ := br.ReadBits(8)
	terminalProviderCode, _ := br.ReadBits(16)
	terminalProviderOrientedCode, _ := br.ReadBits(16)
	applicationID, _ := br.ReadBits(8)
	applicationVersion, _ := br.ReadBits(8)
	numWindows, _ := br.ReadBits(2)
	_, _ = br.ReadBits(6)
	if countryCode == 0xB5 && terminalProviderCode == 0x003C && terminalProviderOrientedCode == 0x0001 && applicationID == 4 && (applicationVersion == 0 || applicationVersion == 1) && numWindows == 1 {
		*hdr10plus = true
	}
}

func formatMasteringDisplay(primaries []uint16) string {
	common := []struct {
		name string
		vals [8]uint16
	}{
		{"BT.2020", [8]uint16{8500, 39850, 6550, 2300, 35400, 14600, 15635, 16450}},
		{"DCI P3", [8]uint16{13250, 34500, 7500, 3000, 34000, 16000, 15700, 17550}},
		{"Display P3", [8]uint16{13250, 34500, 7500, 3000, 34000, 16000, 15635, 16450}},
	}
	for _, cs := range common {
		match := true
		for i := range 8 {
			if primaries[i] < cs.vals[i]-25 || primaries[i] > cs.vals[i]+25 {
				match = false
				break
			}
		}
		if match {
			return cs.name
		}
	}
	return fmt.Sprintf("G(%.6f,%.6f) B(%.6f,%.6f) R(%.6f,%.6f) W(%.6f,%.6f)",
		float64(primaries[0])/50000.0, float64(primaries[1])/50000.0,
		float64(primaries[2])/50000.0, float64(primaries[3])/50000.0,
		float64(primaries[4])/50000.0, float64(primaries[5])/50000.0,
		float64(primaries[6])/50000.0, float64(primaries[7])/50000.0)
}

func findNALUnits(data []byte) [][]byte {
	var nalUnits [][]byte
	var current []byte
	inNAL := false

	for i := 0; i < len(data)-3; i++ {
		if data[i] == 0x00 && data[i+1] == 0x00 {
			if data[i+2] == 0x01 {
				if inNAL && len(current) > 0 {
					nalUnits = append(nalUnits, current)
				}
				current = []byte{}
				inNAL = true
				i += 2
				continue
			} else if i < len(data)-4 && data[i+2] == 0x00 && data[i+3] == 0x01 {
				if inNAL && len(current) > 0 {
					nalUnits = append(nalUnits, current)
				}
				current = []byte{}
				inNAL = true
				i += 3
				continue
			}
		}
		if inNAL {
			current = append(current, data[i])
		}
	}

	if inNAL && len(current) > 0 {
		nalUnits = append(nalUnits, current)
	}
	return nalUnits
}
