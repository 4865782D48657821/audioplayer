package main

import (
	"path/filepath"
	"strings"
)

var supportedExts = map[string]struct{}{
	".mp3":  {},
	".flac": {},
	".wav":  {},
}

func isSupportedAudioExt(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	_, ok := supportedExts[ext]
	return ok
}
