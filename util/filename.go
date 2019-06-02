package util

import "strings"

var srtExtensions = []string{
	".srt",         // SubRip text file
	".ssa", ".ass", // Advanced Substation
	".usf", // Universal Subtitle Format
	".cdg",
	".idx", // VobSub
	".sub", // MicroDVD or SubViewer
	".utf",
	".aqt", // AQTitle
	".jss", // JacoSub
	".psb", // PowerDivX
	".rt",  // RealText
	".smi", // SAMI
	// ".txt", // MPEG 4 Timed Text
	".smil",
	".stl", // Spruce Subtitle Format
	".dks",
	".pjs", // Phoenix Subtitle
	".mpl2",
	".mks",
}

// ToFileName ...
func ToFileName(filename string) string {
	reserved := []string{"<", ">", ":", "\"", "/", "\\", "|", "?", "*", "%", "+"}
	for _, reservedchar := range reserved {
		filename = strings.Replace(filename, reservedchar, "", -1)
	}
	return filename
}

// IsSubtitlesExt checks if extension belong to Subtitles type
func IsSubtitlesExt(ext string) bool {
	for _, e := range srtExtensions {
		if ext == e {
			return true
		}
	}

	return false
}

// HasSubtitlesExt searches different subtitles extensions in file name
func HasSubtitlesExt(filename string) bool {
	for _, e := range srtExtensions {
		if strings.HasSuffix(filename, e) {
			return true
		}
	}

	return false
}
