package main

import (
	"fmt"
	"image/color"
	"math"
	"math/rand"
	"path/filepath"
	"strings"
	"time"

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

var (
	colorBezel      = lipgloss.Color("#1A1A1A")
	colorPanel      = lipgloss.Color("#1A1A1A")
	colorOlive      = lipgloss.Color("#4A90E2")
	colorOliveDark  = lipgloss.Color("#A8B3A2")
	colorLCD        = lipgloss.Color("#A8B3A2")
	colorLCDMuted   = lipgloss.Color("#A8B3A2")
	colorAccent     = lipgloss.Color("#E8B817")
	colorAccentDark = lipgloss.Color("#4A90E2")
	colorAlert      = lipgloss.Color("#D10000")
)

var (
	baseStyle = lipgloss.NewStyle().
			Background(colorBezel).
			Foreground(colorLCD)
	bezelStyle = lipgloss.NewStyle().
			Border(lipgloss.BlockBorder()).
			BorderForeground(colorOlive).
			Padding(1, 2)
	panelStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(colorOliveDark).
			Background(colorPanel).
			Padding(1, 2)
	titleStyle = lipgloss.NewStyle().
			Foreground(colorBezel).
			Background(colorAccent).
			Bold(true).
			Padding(0, 1)
	badgeStyle = lipgloss.NewStyle().
			Foreground(colorBezel).
			Background(colorAlert).
			Bold(true).
			Padding(0, 1)
	labelStyle = lipgloss.NewStyle().
			Foreground(colorLCDMuted).
			Bold(true)
	valueStyle = lipgloss.NewStyle().
			Foreground(colorLCD)
	keyStyle = lipgloss.NewStyle().
			Foreground(colorBezel).
			Background(colorAccentDark).
			Bold(true).
			Padding(0, 1)
	helpStyle = lipgloss.NewStyle().
			Foreground(colorLCDMuted)
)

var spectrumBoxStyle = lipgloss.NewStyle().
	Border(lipgloss.NormalBorder()).
	BorderForeground(colorOliveDark).
	Background(colorPanel).
	Padding(0, 1)
var spectrumLabelStyle = lipgloss.NewStyle().Foreground(colorLCDMuted).Bold(true)
var spectrumEmptyStyle = lipgloss.NewStyle().Foreground(colorLCDMuted)

type model struct {
	dir          string
	songs        []song
	table        table.Model
	progress     progress.Model
	playingIndex int
	viewMode     viewMode
	nowPlaying   string
	err          string
	player       audioPlayer
	volumeDB     float64
	eqGains      []float64
	eqSelected   int
	spectrum     []float64
	spectrumOn   bool
	eqOn         bool
	playMode     playbackMode
	playOrder    []int
	playPos      int
	rng          *rand.Rand
	doneCh       chan int
	playToken    int
}

type tickMsg time.Time
type trackDoneMsg struct {
	token int
}

type viewMode int

const (
	viewList viewMode = iota
	viewDetail
)

type playbackMode int

const (
	playbackNormal playbackMode = iota
	playbackLoop
	playbackShuffle
)

func newTableModel() table.Model {
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
		BorderForeground(colorOliveDark).
		BorderBottom(true).
		Bold(false).
		Foreground(colorLCDMuted)

	s.Selected = s.Selected.
		Foreground(colorBezel).
		Background(colorAccent).
		Bold(true)
	t.SetStyles(s)
	return t
}

func initialModel(dir string, lowPower bool) *model {
	eqGains := make([]float64, len(eqFrequencies))
	spectrumOn := !lowPower
	eqOn := !lowPower
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	player := audioPlayer{
		spectrumEnabled: spectrumOn,
		eqEnabled:       eqOn,
		resampleQuality: resampleQualityDefault,
	}
	if lowPower {
		player.resampleQuality = resampleQualityLowPower
	}
	return &model{
		dir:          dir,
		table:        newTableModel(),
		progress:     progress.New(progress.WithDefaultBlend()),
		playingIndex: -1,
		viewMode:     viewList,
		eqGains:      eqGains,
		spectrumOn:   spectrumOn,
		eqOn:         eqOn,
		playMode:     playbackNormal,
		playPos:      -1,
		rng:          rng,
		player:       player,
	}
}

func (m *model) Init() tea.Cmd {
	return tea.Batch(reloadCmd(m.dir), tickCmd())
}

func (m model) renderTrackCell(index int, track int) string {
	prefix := "  "
	if m.playingIndex == index && m.nowPlaying != "" {
		prefix = "▶ "
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

func (m *model) playbackPercent() float64 {
	if !m.player.inited || m.player.decoder == nil {
		return 0
	}

	speaker.Lock()
	pos := m.player.decoder.Position()
	length := m.player.decoder.Len()
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
	m.player.Stop()
}

func (m *model) shutdownAudio() {
	m.player.Shutdown()
}

func (m *model) handleReload(msg reloadMsg) tea.Cmd {
	if msg.err != nil {
		m.err = msg.err.Error()
		m.songs = nil
		m.playingIndex = -1
		m.nowPlaying = ""
		m.setViewMode(viewList)
		m.syncTableRows()
		return nil
	}
	m.err = ""
	m.songs = msg.songs
	startIndex := -1
	if m.playingIndex >= 0 && m.playingIndex < len(m.songs) {
		startIndex = m.playingIndex
	} else {
		m.playingIndex = -1
		m.nowPlaying = ""
		m.playPos = -1
	}
	m.rebuildPlayOrder(startIndex)
	m.syncTableRows()

	if len(m.songs) > 0 && m.table.Cursor() < 0 {
		m.table.SetCursor(0)
	}

	if len(m.songs) == 0 {
		m.playingIndex = -1
		m.nowPlaying = ""
		m.table.SetCursor(0)
		m.setViewMode(viewList)
	}
	return nil
}

func (m *model) handlePlay(msg playMsg) tea.Cmd {
	if msg.loadErr != nil {
		m.err = msg.loadErr.Error()
		m.playingIndex = -1
		m.nowPlaying = ""
		m.setViewMode(viewList)
		m.syncTableRows()
		return m.progress.SetPercent(0)
	}

	m.err = ""
	m.stopAudio()
	if m.doneCh != nil {
		select {
		case m.doneCh <- -1:
		default:
		}
	}
	m.playToken++
	token := m.playToken
	m.doneCh = make(chan int, 1)
	doneCmd := waitTrackDoneCmd(m.doneCh)
	if err := m.player.Play(msg.format, msg.stream, false, m.doneCh, token); err != nil {
		_ = msg.stream.Close()
		m.err = err.Error()
		return nil
	}
	m.nowPlaying = filepath.Base(msg.path)
	m.setViewMode(viewDetail)
	m.syncTableRows()
	return tea.Batch(m.progress.SetPercent(m.playbackPercent()), doneCmd)
}

func (m *model) handleTrackDone() tea.Cmd {
	if len(m.songs) == 0 {
		return nil
	}
	next := m.nextIndex()
	if next < 0 || next >= len(m.songs) {
		m.stopAudio()
		m.playingIndex = -1
		m.nowPlaying = ""
		m.playPos = -1
		m.setViewMode(viewList)
		m.syncTableRows()
		return m.progress.SetPercent(0)
	}
	m.playingIndex = next
	m.syncTableRows()
	return tea.Batch(m.progress.SetPercent(0), playCmd(m.dir, m.songs[next].Filename))
}

func (m *model) startPlayback(index int) tea.Cmd {
	if index < 0 || index >= len(m.songs) {
		return nil
	}
	m.playingIndex = index
	m.setPlayPosition(index)
	m.syncTableRows()
	return tea.Batch(m.progress.SetPercent(0), playCmd(m.dir, m.songs[index].Filename))
}

func (m *model) nextIndex() int {
	if len(m.songs) == 0 {
		return -1
	}
	if len(m.playOrder) != len(m.songs) {
		m.rebuildPlayOrder(m.playingIndex)
	}
	if len(m.playOrder) == 0 {
		return -1
	}
	switch m.playMode {
	case playbackShuffle:
		if m.playPos+1 >= len(m.playOrder) {
			m.rebuildPlayOrder(-1)
			if len(m.playOrder) == 0 {
				return -1
			}
			return m.playOrder[0]
		}
	case playbackLoop:
		if m.playPos+1 >= len(m.playOrder) {
			m.playPos = 0
			return m.playOrder[m.playPos]
		}
	default:
		if m.playPos+1 >= len(m.playOrder) {
			return -1
		}
	}
	m.playPos++
	return m.playOrder[m.playPos]
}

func (m *model) prevIndex() int {
	if len(m.songs) == 0 {
		return -1
	}
	if len(m.playOrder) != len(m.songs) {
		m.rebuildPlayOrder(m.playingIndex)
	}
	if len(m.playOrder) == 0 {
		return -1
	}
	if m.playPos-1 < 0 {
		if m.playMode == playbackLoop || m.playMode == playbackShuffle {
			m.playPos = len(m.playOrder) - 1
			return m.playOrder[m.playPos]
		}
		return -1
	}
	m.playPos--
	return m.playOrder[m.playPos]
}

func (m *model) setPlayPosition(index int) {
	if index < 0 || index >= len(m.songs) {
		m.playPos = -1
		return
	}
	if m.playMode == playbackShuffle {
		m.rebuildPlayOrder(index)
		return
	}
	m.playOrder = make([]int, len(m.songs))
	for i := range m.playOrder {
		m.playOrder[i] = i
	}
	m.playPos = index
}

func (m *model) rebuildPlayOrder(startIndex int) {
	if len(m.songs) == 0 {
		m.playOrder = nil
		m.playPos = -1
		return
	}
	if m.playMode != playbackShuffle {
		m.playOrder = make([]int, len(m.songs))
		for i := range m.playOrder {
			m.playOrder[i] = i
		}
		if startIndex >= 0 && startIndex < len(m.playOrder) {
			m.playPos = startIndex
		} else {
			m.playPos = 0
		}
		return
	}

	order := make([]int, len(m.songs))
	for i := range order {
		order[i] = i
	}
	if startIndex >= 0 && startIndex < len(order) {
		order[0], order[startIndex] = order[startIndex], order[0]
		if len(order) > 1 {
			m.rng.Shuffle(len(order)-1, func(i, j int) {
				i++
				j++
				order[i], order[j] = order[j], order[i]
			})
		}
		m.playPos = 0
	} else {
		m.rng.Shuffle(len(order), func(i, j int) {
			order[i], order[j] = order[j], order[i]
		})
		m.playPos = 0
	}
	m.playOrder = order
}

func (m *model) cyclePlaybackMode() {
	switch m.playMode {
	case playbackNormal:
		m.setPlaybackMode(playbackLoop)
	case playbackLoop:
		m.setPlaybackMode(playbackShuffle)
	default:
		m.setPlaybackMode(playbackNormal)
	}
}

func (m *model) setPlaybackMode(mode playbackMode) {
	if m.playMode == mode {
		return
	}
	m.playMode = mode
	startIndex := -1
	if m.playingIndex >= 0 && m.playingIndex < len(m.songs) {
		startIndex = m.playingIndex
	}
	m.rebuildPlayOrder(startIndex)
}

func playbackModeLabel(mode playbackMode) string {
	switch mode {
	case playbackLoop:
		return "LOOP"
	case playbackShuffle:
		return "SHUFFLE"
	default:
		return "NORMAL"
	}
}

func (m *model) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+c", "q":
		m.shutdownAudio()
		return tea.Quit
	case "r":
		return reloadCmd(m.dir)
	case "b":
		m.setViewMode(viewList)
		return nil
	case "-":
		m.adjustVolume(-2)
	case "=", "+":
		m.adjustVolume(2)
	case "h":
		if m.viewMode == viewDetail {
			m.selectEQBand(-1)
		}
	case "l":
		if m.viewMode == viewDetail {
			m.selectEQBand(1)
		}
	case "j":
		if m.viewMode == viewDetail {
			m.adjustEQGain(-1.5)
		}
	case "k":
		if m.viewMode == viewDetail {
			m.adjustEQGain(1.5)
		}
	case "m":
		m.cyclePlaybackMode()
	case "n":
		if m.playingIndex >= 0 {
			next := m.nextIndex()
			if next >= 0 && next < len(m.songs) {
				m.playingIndex = next
				m.syncTableRows()
				return tea.Batch(m.progress.SetPercent(0), playCmd(m.dir, m.songs[next].Filename))
			}
		}
		return nil
	case "v":
		if m.playingIndex >= 0 {
			prev := m.prevIndex()
			if prev >= 0 && prev < len(m.songs) {
				m.playingIndex = prev
				m.syncTableRows()
				return tea.Batch(m.progress.SetPercent(0), playCmd(m.dir, m.songs[prev].Filename))
			}
		}
		return nil
	case "e":
		m.eqOn = !m.eqOn
		m.player.SetEQEnabled(m.eqOn)
	case "s":
		m.spectrumOn = !m.spectrumOn
		m.player.SetSpectrumEnabled(m.spectrumOn)
		if !m.spectrumOn {
			m.spectrum = nil
		}
	case "enter", "space", "p":
		if len(m.songs) == 0 {
			return nil
		}
		selected := m.table.Cursor()
		if selected < 0 || selected >= len(m.songs) {
			return nil
		}

		if m.playingIndex == selected && m.nowPlaying != "" {
			m.nowPlaying = ""
			m.stopAudio()
			if m.doneCh != nil {
				select {
				case m.doneCh <- -1:
				default:
				}
			}
			m.playingIndex = -1
			m.playPos = -1
			m.setViewMode(viewList)
			m.syncTableRows()
			return tea.Batch(reloadCmd(m.dir), m.progress.SetPercent(0))
		}
		return m.startPlayback(selected)
	}
	return nil
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {

	switch msg := msg.(type) {
	case reloadMsg:
		return m, m.handleReload(msg)
	case playMsg:
		return m, m.handlePlay(msg)
	case trackDoneMsg:
		if msg.token != m.playToken {
			return m, nil
		}
		return m, m.handleTrackDone()
	case tea.KeyPressMsg:
		if m.viewMode == viewList && isListNavKey(msg) {
			var cmd tea.Cmd
			m.table, cmd = m.table.Update(msg)
			return m, cmd
		}
		return m, m.handleKey(msg)
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
			if m.spectrumOn {
				m.spectrum = m.player.Spectrum()
			} else {
				m.spectrum = nil
			}
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

func (m *model) View() tea.View {
	header := m.renderHeader()
	var body string
	switch m.viewMode {
	case viewDetail:
		body = lipgloss.JoinVertical(lipgloss.Left,
			m.renderNowPlaying(),
			m.progress.View(),
			m.renderSongInfo(),
			m.renderVolume(),
			m.renderEQ(),
		)
		if m.spectrumOn && len(m.spectrum) > 0 {
			body = lipgloss.JoinVertical(lipgloss.Left, body, m.renderSpectrum())
		}
	default:
		body = m.table.View()
	}

	if m.err != "" {
		body = lipgloss.JoinVertical(lipgloss.Left, body, labelStyle.Render("ERR")+" "+valueStyle.Render(m.err))
	}

	if len(m.songs) == 0 {
		body = lipgloss.JoinVertical(lipgloss.Left,
			labelStyle.Render("NO FILES"),
			valueStyle.Render("Press R to rescan or Q to quit."),
		)
	}

	content := lipgloss.JoinVertical(
		lipgloss.Left,
		header,
		panelStyle.Render(body),
		m.renderHelp(),
	)
	return tea.NewView(baseStyle.Render(bezelStyle.Render(content)))
}

func (m *model) adjustVolume(delta float64) {
	m.volumeDB = clampFloat(m.volumeDB+delta, minVolumeDB, maxVolumeDB)
	m.player.SetVolumeDB(m.volumeDB)
}

func (m *model) selectEQBand(delta int) {
	if len(m.eqGains) == 0 {
		return
	}
	m.eqSelected = (m.eqSelected + delta) % len(m.eqGains)
	if m.eqSelected < 0 {
		m.eqSelected = len(m.eqGains) - 1
	}
}

func (m *model) adjustEQGain(delta float64) {
	if len(m.eqGains) == 0 {
		return
	}
	m.eqGains[m.eqSelected] = clampFloat(m.eqGains[m.eqSelected]+delta, minEQGainDB, maxEQGainDB)
	m.player.SetEQGain(m.eqSelected, m.eqGains[m.eqSelected])
}

func (m *model) renderVolume() string {
	pct := 0.0
	if maxVolumeDB > minVolumeDB {
		pct = (m.volumeDB - minVolumeDB) / (maxVolumeDB - minVolumeDB)
	}
	barWidth := 12
	filled := int(math.Round(pct * float64(barWidth)))
	if filled < 0 {
		filled = 0
	}
	if filled > barWidth {
		filled = barWidth
	}
	meter := strings.Repeat("|", filled) + strings.Repeat(".", barWidth-filled)
	return fmt.Sprintf("%s [%s] %s", labelStyle.Render("VOL"), meter, valueStyle.Render(fmt.Sprintf("%.1f dB", m.volumeDB)))
}

func (m *model) renderEQ() string {
	if !m.eqOn {
		return labelStyle.Render("EQ") + " " + valueStyle.Render("(off)")
	}
	if len(m.eqGains) == 0 {
		return labelStyle.Render("EQ") + " " + valueStyle.Render("(none)")
	}
	parts := make([]string, 0, len(m.eqGains))
	for i, gain := range m.eqGains {
		label := fmt.Sprintf("%.0fHz %.1fdB", eqFrequencies[i], gain)
		if i == m.eqSelected {
			label = keyStyle.Render(label)
		} else {
			label = valueStyle.Render(label)
		}
		parts = append(parts, label)
	}
	return labelStyle.Render("EQ") + " " + strings.Join(parts, " ")
}

func (m *model) renderNowPlaying() string {
	if m.nowPlaying == "" {
		return labelStyle.Render("NOW") + " " + valueStyle.Render("(none)")
	}
	return labelStyle.Render("NOW") + " " + valueStyle.Render(m.nowPlaying)
}

func (m *model) renderSongInfo() string {
	if m.playingIndex < 0 || m.playingIndex >= len(m.songs) {
		return labelStyle.Render("INFO") + " " + valueStyle.Render("(none)")
	}
	track := "—"
	if m.songs[m.playingIndex].Track > 0 {
		track = fmt.Sprintf("%d", m.songs[m.playingIndex].Track)
	}
	info := fmt.Sprintf("%s | %s | %s | Track %s",
		m.songs[m.playingIndex].Title,
		m.songs[m.playingIndex].Artist,
		m.songs[m.playingIndex].Album,
		track,
	)
	return labelStyle.Render("INFO") + " " + valueStyle.Render(info)
}

func (m *model) renderSpectrum() string {
	if len(m.spectrum) == 0 {
		return spectrumBoxStyle.Render(spectrumLabelStyle.Render("Spectrum") + "\n" + spectrumEmptyStyle.Render("(no data)"))
	}
	barHeight := 4
	barWidth := 3
	columns := make([]string, 0, len(m.spectrum))
	for _, level := range m.spectrum {
		filled := int(math.Round(level * float64(barHeight)))
		if filled > barHeight {
			filled = barHeight
		}
		color := spectrumColor(level)
		filledStyle := lipgloss.NewStyle().Foreground(color)
		emptyStyle := spectrumEmptyStyle
		rows := make([]string, 0, barHeight)
		for i := barHeight - 1; i >= 0; i-- {
			if i < filled {
				rows = append(rows, filledStyle.Render(strings.Repeat("|", barWidth)))
			} else {
				rows = append(rows, emptyStyle.Render(strings.Repeat(".", barWidth)))
			}
		}
		columns = append(columns, lipgloss.JoinVertical(lipgloss.Center, rows...))
	}
	bars := lipgloss.JoinHorizontal(lipgloss.Left, columns...)
	body := spectrumLabelStyle.Render("Spectrum") + "\n" + bars
	return spectrumBoxStyle.Render(body)
}

func spectrumColor(level float64) color.Color {
	switch {
	case level >= 0.75:
		return colorAlert
	case level >= 0.5:
		return colorAccent
	case level >= 0.25:
		return colorLCD
	default:
		return colorLCDMuted
	}
}

func (m *model) renderHeader() string {
	mode := "LIST"
	if m.viewMode == viewDetail {
		mode = "PLAY"
	}
	left := lipgloss.JoinHorizontal(lipgloss.Left,
		titleStyle.Render("SIMPLE"),
		badgeStyle.Render("AUDIO"),
		titleStyle.Render("PLAYER"),
	)
	right := labelStyle.Render("MODE") + " " + valueStyle.Render(mode) + "  " +
		labelStyle.Render("PLAY") + " " + valueStyle.Render(playbackModeLabel(m.playMode))
	return lipgloss.JoinHorizontal(lipgloss.Left, left, "  ", right)
}

func (m *model) renderHelp() string {
	if len(m.songs) == 0 {
		return helpStyle.Render(" ")
	}
	if m.viewMode == viewDetail {
		return helpStyle.Render(
			keyStyle.Render("B") + " back  " +
				keyStyle.Render("P") + " play  " +
				keyStyle.Render("R") + " rescan  " +
				keyStyle.Render("Q") + " quit  " +
				keyStyle.Render("+/-") + " vol  " +
				keyStyle.Render("M") + " mode  " +
				keyStyle.Render("N/V") + " next/prev  " +
				keyStyle.Render("E") + " eq  " +
				keyStyle.Render("S") + " spectrum  " +
				keyStyle.Render("H/L") + " band  " +
				keyStyle.Render("J/K") + " gain",
		)
	}
	return helpStyle.Render(
		keyStyle.Render("P") + " play  " +
			keyStyle.Render("R") + " rescan  " +
			keyStyle.Render("Q") + " quit  " +
			keyStyle.Render("+/-") + " vol  " +
			keyStyle.Render("M") + " mode  " +
			keyStyle.Render("N/V") + " next/prev  " +
			keyStyle.Render("E") + " eq  " +
			keyStyle.Render("S") + " spectrum  " +
			keyStyle.Render("H/L") + " band  " +
			keyStyle.Render("J/K") + " gain",
	)
}

func (m *model) setViewMode(mode viewMode) {
	m.viewMode = mode
	if mode == viewList {
		m.table.Focus()
	} else {
		m.table.Blur()
	}
}

func isListNavKey(msg tea.KeyPressMsg) bool {
	switch msg.String() {
	case "up", "down", "j", "k", "pgup", "pgdown", "home", "end", "g", "G", "b", "f", "u", "d":
		return true
	default:
		return false
	}
}

func waitTrackDoneCmd(done <-chan int) tea.Cmd {
	return func() tea.Msg {
		token := <-done
		return trackDoneMsg{token: token}
	}
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Millisecond*500, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
