package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
)

type song struct {
	Filename string
	Track    int
	Title    string
	Artist   string
	Album    string
}

type reloadMsg struct {
	songs []song
	err   error
}

var trackTitleRe = regexp.MustCompile(`^(\d{1,3})\s+(.*)$`)

func parseSongFromFilename(filename string) song {
	base := strings.TrimSuffix(filename, filepath.Ext(filename))
	parts := strings.Split(base, " - ")

	s := song{
		Filename: filename,
		Title:    base,
	}

	if len(parts) >= 1 {
		s.Artist = strings.TrimSpace(parts[0])
	}

	if len(parts) >= 2 {
		s.Album = strings.TrimSpace(parts[1])
	}

	if len(parts) >= 3 {
		rest := strings.TrimSpace(strings.Join(parts[2:], " - "))
		if m := trackTitleRe.FindStringSubmatch(rest); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil {
				s.Track = n
			}
			s.Title = strings.TrimSpace(m[2])
		} else if rest != "" {
			s.Title = rest
		}
	}

	if s.Title == "" {
		s.Title = base
	}

	return s
}

func listAudioSongs(dir string) ([]song, error) {
	entries, err := os.ReadDir(dir)

	if err != nil {
		return nil, err
	}

	songs := make([]song, 0, len(entries))

	for _, e := range entries {
		if e.IsDir() {
			continue
		}

		name := e.Name()
		if isSupportedAudioExt(name) {
			songs = append(songs, parseSongFromFilename(name))
		}
	}

	sort.Slice(songs, func(i, j int) bool {
		a, b := songs[i], songs[j]
		switch {
		case a.Artist != b.Artist:
			return a.Artist < b.Artist
		case a.Album != b.Album:
			return a.Album < b.Album
		case a.Track != 0 && b.Track != 0 && a.Track != b.Track:
			return a.Track < b.Track
		default:
			return a.Filename < b.Filename
		}
	})
	return songs, nil
}

func reloadCmd(dir string) tea.Cmd {
	return func() tea.Msg {
		songs, err := listAudioSongs(dir)
		return reloadMsg{songs: songs, err: err}
	}
}
