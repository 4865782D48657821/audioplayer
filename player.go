package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/faiface/beep"
	"github.com/faiface/beep/flac"
	"github.com/faiface/beep/mp3"
	"github.com/faiface/beep/speaker"
	"github.com/faiface/beep/wav"

	tea "charm.land/bubbletea/v2"
)

const (
	resampleQualityDefault  = 4
	resampleQualityLowPower = 1
)

type audioPlayer struct {
	inited          bool
	rate            beep.SampleRate
	decoder         beep.StreamSeekCloser
	volume          *volumeStreamer
	eq              *equalizer
	analyzer        *spectrumAnalyzer
	volumeDB        float64
	eqGains         []float64
	spectrumEnabled bool
	eqEnabled       bool
	resampleQuality int
}

type playMsg struct {
	path    string
	stream  beep.StreamSeekCloser
	format  beep.Format
	loadErr error
}

func (p *audioPlayer) Stop() {
	if !p.inited {
		return
	}
	speaker.Clear()
	if p.decoder != nil {
		_ = p.decoder.Close()
		p.decoder = nil
	}
}

func (p *audioPlayer) Shutdown() {
	if !p.inited {
		return
	}
	p.Stop()
	speaker.Close()
}

func (p *audioPlayer) Play(format beep.Format, stream beep.StreamSeekCloser, loop bool, done chan<- int, token int) error {
	rate := format.SampleRate
	if !p.inited {
		if err := speaker.Init(rate, rate.N(time.Second/10)); err != nil {
			return err
		}
		p.inited = true
		p.rate = rate
	}

	var streamer beep.Streamer = stream
	if loop {
		streamer = beep.Loop(-1, stream)
	} else if done != nil {
		streamer = beep.Seq(stream, beep.Callback(func() {
			_ = stream.Close()
			select {
			case done <- token:
			default:
			}
		}))
	}
	if p.rate != rate {
		streamer = beep.Resample(p.resampleQualityValue(), rate, p.rate, streamer)
	}
	streamer = p.chain(streamer)
	speaker.Play(streamer)
	p.decoder = stream
	return nil
}

func (p *audioPlayer) SetVolumeDB(db float64) {
	p.volumeDB = clampFloat(db, minVolumeDB, maxVolumeDB)
	if p.volume != nil {
		p.volume.SetDB(p.volumeDB)
	}
}

func (p *audioPlayer) SetEQGain(band int, gainDB float64) {
	if band < 0 {
		return
	}
	for len(p.eqGains) <= band {
		p.eqGains = append(p.eqGains, 0)
	}
	p.eqGains[band] = clampFloat(gainDB, minEQGainDB, maxEQGainDB)
	if p.eq != nil {
		p.eq.SetGain(band, p.eqGains[band])
	}
}

func (p *audioPlayer) Spectrum() []float64 {
	if p.analyzer == nil {
		return nil
	}
	return p.analyzer.Snapshot()
}

func (p *audioPlayer) SetSpectrumEnabled(enabled bool) {
	p.spectrumEnabled = enabled
	if p.analyzer != nil {
		p.analyzer.SetEnabled(enabled)
	}
}

func (p *audioPlayer) SetEQEnabled(enabled bool) {
	p.eqEnabled = enabled
}

func (p *audioPlayer) chain(source beep.Streamer) beep.Streamer {
	if p.eqEnabled {
		if p.eq == nil {
			p.eq = newEqualizer(eqFrequencies, 1.0)
		}
		p.eq.SetSampleRate(p.rate)
		p.eq.SetGains(p.eqGains)
		p.eq.SetSource(source)
		source = p.eq
	}

	if p.volume == nil {
		p.volume = newVolumeStreamer(nil, p.volumeDB)
	}
	p.volume.SetDB(p.volumeDB)
	p.volume.SetSource(source)

	if p.analyzer == nil {
		p.analyzer = newSpectrumAnalyzer(spectrumFrequencies, p.rate, 1024)
	} else {
		p.analyzer.SetSampleRate(p.rate, 1024)
	}
	p.analyzer.SetEnabled(p.spectrumEnabled)
	p.analyzer.SetSource(p.volume)
	return p.analyzer
}

func (p *audioPlayer) resampleQualityValue() int {
	if p.resampleQuality <= 0 {
		return resampleQualityDefault
	}
	return p.resampleQuality
}

func playCmd(dir, name string) tea.Cmd {
	return func() tea.Msg {
		path := filepath.Join(dir, name)
		f, err := os.Open(path)
		if err != nil {
			return playMsg{path: path, loadErr: err}
		}
		stream, format, err := decodeByExtension(path, f)
		if err != nil {
			_ = f.Close()
			return playMsg{path: path, loadErr: err}
		}
		return playMsg{path: path, stream: stream, format: format}
	}
}

func decodeByExtension(path string, f *os.File) (beep.StreamSeekCloser, beep.Format, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp3":
		stream, format, err := mp3.Decode(f)
		if err != nil {
			return nil, beep.Format{}, fmt.Errorf("failed to decode mp3: %w", err)
		}
		return stream, format, nil
	case ".flac":
		stream, format, err := flac.Decode(f)
		if err != nil {
			return nil, beep.Format{}, fmt.Errorf("failed to decode flac: %w", err)
		}
		return stream, format, nil
	case ".wav":
		stream, format, err := wav.Decode(f)
		if err != nil {
			return nil, beep.Format{}, fmt.Errorf("failed to decode wav: %w", err)
		}
		return stream, format, nil
	default:
		return nil, beep.Format{}, fmt.Errorf("unsupported audio format: %s", filepath.Ext(path))
	}
}
