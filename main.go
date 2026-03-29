package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/faiface/beep"
	"github.com/faiface/beep/mp3"
	"github.com/faiface/beep/speaker"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

const (
	padding  = 2
	maxWidth = 80
)

var helpStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#626262")).Render

type song struct {
	Filename string
	Track    int
	Title    string
	Artist   string
	Album    string
}

type model struct {
	dir            string
	songs          []song
	table          table.Model
	progress       progress.Model
	playingIndex   int
	nowPlaying     string
	err            string
	speakerInited  bool
	speakerRate    beep.SampleRate
	currentDecoder beep.StreamSeekCloser
}

type playMsg struct {
	path    string
	stream  beep.StreamSeekCloser
	format  beep.Format
	loadErr error
}

type reloadMsg struct {
	songs []song
	err   error
}

type tickMsg time.Time

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

func listMP3Songs(dir string) ([]song, error) {
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
		if strings.EqualFold(filepath.Ext(name), ".mp3") {
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
		songs, err := listMP3Songs(dir)
		return reloadMsg{songs: songs, err: err}
	}
}

func playCmd(dir, name string) tea.Cmd {
	return func() tea.Msg {
		path := filepath.Join(dir, name)
		f, err := os.Open(path)
		if err != nil {
			return playMsg{path: path, loadErr: err}
		}
		stream, format, err := mp3.Decode(f)
		if err != nil {
			_ = f.Close()
			return playMsg{path: path, loadErr: fmt.Errorf("failed to decode mp3: %w", err)}
		}
		return playMsg{path: path, stream: stream, format: format}
	}
}

func initialModel(dir string) model {
	columns := []table.Column{
		{Title: "Track", Width: 4},
		{Title: "Title", Width: 10},
		{Title: "Artist", Width: 10},
		{Title: "Album", Width: 10},
	}

	rows := []table.Row{}

	km := table.DefaultKeyMap()
	km.PageDown = key.NewBinding(
		key.WithKeys("f", "pgdown"),
		key.WithHelp("f/pgdn", "page down"),
	)

	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rows),
		table.WithFocused(true),
		table.WithHeight(7),
		table.WithWidth(42),
		table.WithKeyMap(km),
	)

	s := table.DefaultStyles()
	s.Header = s.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("240")).
		BorderBottom(true).
		Bold(false)

	s.Selected = s.Selected.
		Foreground(lipgloss.Color("229")).
		Background(lipgloss.Color("57")).
		Bold(false)
	t.SetStyles(s)

	return model{
		dir:          dir,
		table:        t,
		progress:     progress.New(progress.WithDefaultBlend()),
		playingIndex: -1,
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(reloadCmd(m.dir), tickCmd())
}

func (m model) renderTrackCell(index int, track int) string {
	prefix := " "
	if m.playingIndex == index && m.nowPlaying != "" {
		prefix = "->"
	}
	if track <= 0 {
		return prefix
	}
	if track < 10 {
		return fmt.Sprintf("%s0%d", prefix, track)
	}
	if track < 100 {
		return fmt.Sprintf("%s%d", prefix, track)
	}
	return fmt.Sprintf("%s%d", prefix, track)
}

func (m *model) syncTableRows() {
	rows := make([]table.Row, 0, len(m.songs))
	for i, s := range m.songs {
		rows = append(rows, table.Row{
			m.renderTrackCell(i, s.Track),
			s.Title,
			s.Artist,
			s.Album,
		})
	}
	m.table.SetRows(rows)
}

func (m model) playbackPercent() float64 {
	if !m.speakerInited || m.currentDecoder == nil {
		return 0
	}

	speaker.Lock()
	pos := m.currentDecoder.Position()
	length := m.currentDecoder.Len()
	speaker.Unlock()

	if length <= 0 || pos <= 0 {
		return 0
	}
	if pos >= length {
		return 1
	}
	return float64(pos) / float64(length)
}

func (m *model) stopAudio() {
	if !m.speakerInited {
		return
	}

	speaker.Clear()
	if m.currentDecoder != nil {
		_ = m.currentDecoder.Close()
		m.currentDecoder = nil
	}
}

func (m *model) shutdownAudio() {
	if !m.speakerInited {
		return
	}
	m.stopAudio()
	speaker.Close()
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {

	switch msg := msg.(type) {
	case reloadMsg:
		if msg.err != nil {
			m.err = msg.err.Error()
			m.songs = nil
			m.playingIndex = -1
			m.nowPlaying = ""
			m.syncTableRows()
			return m, nil
		}
		m.err = ""
		m.songs = msg.songs
		m.syncTableRows()

		if len(m.songs) > 0 && m.table.Cursor() < 0 {
			m.table.SetCursor(0)
		}

		if len(m.songs) == 0 {
			m.playingIndex = -1
			m.nowPlaying = ""
			m.table.SetCursor(0)
		}
		return m, nil

	case playMsg:
		if msg.loadErr != nil {
			m.err = msg.loadErr.Error()
			m.playingIndex = -1
			m.nowPlaying = ""
			m.syncTableRows()
			return m, m.progress.SetPercent(0)
		}

		m.err = ""
		m.stopAudio()

		rate := msg.format.SampleRate
		if !m.speakerInited {
			if err := speaker.Init(rate, rate.N(time.Second/10)); err != nil {
				_ = msg.stream.Close()
				m.err = err.Error()
				return m, nil
			}
			m.speakerInited = true
			m.speakerRate = rate
		}

		streamer := beep.Streamer(beep.Loop(-1, msg.stream))
		if m.speakerRate != rate {
			streamer = beep.Resample(4, rate, m.speakerRate, streamer)
		}

		speaker.Play(streamer)
		m.currentDecoder = msg.stream
		m.nowPlaying = filepath.Base(msg.path)
		m.syncTableRows()
		return m, m.progress.SetPercent(m.playbackPercent())

	case tea.KeyPressMsg:

		switch msg.String() {

		case "ctrl+c", "q":
			m.shutdownAudio()
			return m, tea.Quit
		case "r":
			return m, reloadCmd(m.dir)
		case "enter", "space":
			if len(m.songs) == 0 {
				return m, nil
			}
			selected := m.table.Cursor()
			if selected < 0 || selected >= len(m.songs) {
				return m, nil
			}

			if m.playingIndex == selected && m.nowPlaying != "" {
				m.nowPlaying = ""
				m.stopAudio()
				m.playingIndex = -1
				m.syncTableRows()
				return m, tea.Batch(reloadCmd(m.dir), m.progress.SetPercent(0))
			}
			m.playingIndex = selected
			m.syncTableRows()
			return m, tea.Batch(m.progress.SetPercent(0), playCmd(m.dir, m.songs[selected].Filename))
		}
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		available := msg.Width - padding*2
		if available > maxWidth {
			available = maxWidth
		}
		if available < 0 {
			available = 0
		}
		m.table.SetWidth(available)
		m.progress.SetWidth(max(0, available-4))
	case tickMsg:
		cmds := []tea.Cmd{tickCmd()}
		if m.nowPlaying != "" {
			cmds = append(cmds, m.progress.SetPercent(m.playbackPercent()))
		}
		return m, tea.Batch(cmds...)
	case progress.FrameMsg:
		var cmd tea.Cmd
		m.progress, cmd = m.progress.Update(msg)
		return m, cmd
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m model) View() tea.View {
	s := fmt.Sprintf("MP3 files in %s\n\n", m.dir)

	if m.err != "" {
		s += fmt.Sprintf("Error: %s\n\n", m.err)
	}

	if len(m.songs) == 0 {
		s += "No .mp3 files found. \n\n"
		s += "Press r to rescan, q to quit. \n"
		return tea.NewView(s)
	}

	s += lipgloss.NewStyle().PaddingLeft(padding).Render(m.table.View())

	if m.nowPlaying != "" {
		s += "\n\n" + lipgloss.NewStyle().PaddingLeft(padding).Render(fmt.Sprintf("Now playing: %s", m.nowPlaying))
		s += "\n" + lipgloss.NewStyle().PaddingLeft(padding).Render(m.progress.View())
	}

	s += "\n\n" + lipgloss.NewStyle().PaddingLeft(padding).Render(helpStyle("Press enter to play/stop, r to rescan, q to quit."))

	return tea.NewView(s)
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Millisecond*150, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

type Volume struct {
	Streamer beep.Streamer
	Base     float64
	Volume   float64
	Silent   bool
}

func main() {
	dir := flag.String("dir", ".", "Directory to scan for .mp3 files")
	flag.Parse()

	if flag.NArg() != 0 {
		fmt.Fprintf(os.Stderr, "Usage: %s [-dir path]\n", filepath.Base(os.Args[0]))
		os.Exit(2)
	}

	p := tea.NewProgram(initialModel(*dir))
	if _, err := p.Run(); err != nil {
		fmt.Printf("Alas, there's been an error: %v", err)
		os.Exit(1)
	}
}
