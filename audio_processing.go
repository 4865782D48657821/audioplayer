package main

import (
	"math"
	"sync"

	"github.com/faiface/beep"
)

const (
	minEQGainDB = -12.0
	maxEQGainDB = 12.0
	minVolumeDB = -60.0
	maxVolumeDB = 6.0
)

var (
	eqFrequencies       = []float64{60, 250, 1000, 4000, 10000}
	spectrumFrequencies = []float64{60, 120, 250, 500, 1000, 2000, 4000, 8000, 12000, 16000}
)

const spectrumComputeEvery = 4

type volumeStreamer struct {
	mu     sync.RWMutex
	source beep.Streamer
	gain   float64
}

func newVolumeStreamer(source beep.Streamer, db float64) *volumeStreamer {
	v := &volumeStreamer{}
	v.SetSource(source)
	v.SetDB(db)
	return v
}

func (v *volumeStreamer) SetSource(source beep.Streamer) {
	v.mu.Lock()
	v.source = source
	v.mu.Unlock()
}

func (v *volumeStreamer) SetDB(db float64) {
	clamped := clampFloat(db, minVolumeDB, maxVolumeDB)
	gain := math.Pow(10, clamped/20)
	v.mu.Lock()
	v.gain = gain
	v.mu.Unlock()
}

func (v *volumeStreamer) Stream(samples [][2]float64) (int, bool) {
	v.mu.RLock()
	source := v.source
	gain := v.gain
	v.mu.RUnlock()
	if source == nil {
		return 0, false
	}
	n, ok := source.Stream(samples)
	if gain == 1 || n == 0 {
		return n, ok
	}
	for i := 0; i < n; i++ {
		samples[i][0] *= gain
		samples[i][1] *= gain
	}
	return n, ok
}

func (v *volumeStreamer) Err() error {
	v.mu.RLock()
	source := v.source
	v.mu.RUnlock()
	if source == nil {
		return nil
	}
	if errer, ok := source.(interface{ Err() error }); ok {
		return errer.Err()
	}
	return nil
}

type biquad struct {
	b0 float64
	b1 float64
	b2 float64
	a1 float64
	a2 float64
	z1 float64
	z2 float64
}

func (b *biquad) Reset() {
	b.z1 = 0
	b.z2 = 0
}

func (b *biquad) Process(x float64) float64 {
	y := b.b0*x + b.z1
	b.z1 = b.b1*x - b.a1*y + b.z2
	b.z2 = b.b2*x - b.a2*y
	return y
}

func (b *biquad) SetPeakingEQ(freq, q, gainDB float64, sampleRate beep.SampleRate) {
	if freq <= 0 || q <= 0 || sampleRate <= 0 {
		b.b0, b.b1, b.b2, b.a1, b.a2 = 1, 0, 0, 0, 0
		b.Reset()
		return
	}
	omega := 2 * math.Pi * freq / float64(sampleRate)
	sn := math.Sin(omega)
	cs := math.Cos(omega)
	alpha := sn / (2 * q)
	amp := math.Pow(10, gainDB/40)

	b0 := 1 + alpha*amp
	b1 := -2 * cs
	b2 := 1 - alpha*amp
	a0 := 1 + alpha/amp
	a1 := -2 * cs
	a2 := 1 - alpha/amp

	b.b0 = b0 / a0
	b.b1 = b1 / a0
	b.b2 = b2 / a0
	b.a1 = a1 / a0
	b.a2 = a2 / a0
	b.Reset()
}

type eqBand struct {
	freq   float64
	q      float64
	gainDB float64
	left   biquad
	right  biquad
}

type equalizer struct {
	mu     sync.RWMutex
	source beep.Streamer
	rate   beep.SampleRate
	bands  []eqBand
	dirty  bool
	bypass bool
}

func newEqualizer(freqs []float64, q float64) *equalizer {
	bands := make([]eqBand, len(freqs))
	for i, freq := range freqs {
		bands[i] = eqBand{freq: freq, q: q}
	}
	return &equalizer{bands: bands, dirty: true, bypass: true}
}

func (e *equalizer) SetSource(source beep.Streamer) {
	e.mu.Lock()
	e.source = source
	e.mu.Unlock()
}

func (e *equalizer) SetSampleRate(rate beep.SampleRate) {
	e.mu.Lock()
	if e.rate != rate {
		e.rate = rate
		e.dirty = true
	}
	e.mu.Unlock()
}

func (e *equalizer) SetGain(band int, gainDB float64) {
	e.mu.Lock()
	if band >= 0 && band < len(e.bands) {
		e.bands[band].gainDB = clampFloat(gainDB, minEQGainDB, maxEQGainDB)
		e.dirty = true
		e.updateBypassLocked()
	}
	e.mu.Unlock()
}

func (e *equalizer) SetGains(gains []float64) {
	e.mu.Lock()
	for i := 0; i < len(e.bands) && i < len(gains); i++ {
		e.bands[i].gainDB = clampFloat(gains[i], minEQGainDB, maxEQGainDB)
	}
	e.dirty = true
	e.updateBypassLocked()
	e.mu.Unlock()
}

func (e *equalizer) applyCoefficients() {
	if !e.dirty {
		return
	}
	for i := range e.bands {
		band := &e.bands[i]
		band.left.SetPeakingEQ(band.freq, band.q, band.gainDB, e.rate)
		band.right.SetPeakingEQ(band.freq, band.q, band.gainDB, e.rate)
	}
	e.dirty = false
}

func (e *equalizer) Stream(samples [][2]float64) (int, bool) {
	e.mu.RLock()
	source := e.source
	bypass := e.bypass
	e.mu.RUnlock()
	if source == nil {
		return 0, false
	}
	if bypass {
		return source.Stream(samples)
	}
	n, ok := source.Stream(samples)
	if n == 0 {
		return n, ok
	}
	e.mu.Lock()
	e.applyCoefficients()
	for i := 0; i < n; i++ {
		l := samples[i][0]
		r := samples[i][1]
		for b := range e.bands {
			l = e.bands[b].left.Process(l)
			r = e.bands[b].right.Process(r)
		}
		samples[i][0] = l
		samples[i][1] = r
	}
	e.mu.Unlock()
	return n, ok
}

func (e *equalizer) updateBypassLocked() {
	const epsilon = 1e-6
	bypass := true
	for i := range e.bands {
		if math.Abs(e.bands[i].gainDB) > epsilon {
			bypass = false
			break
		}
	}
	e.bypass = bypass
}

func (e *equalizer) Err() error {
	e.mu.RLock()
	source := e.source
	e.mu.RUnlock()
	if source == nil {
		return nil
	}
	if errer, ok := source.(interface{ Err() error }); ok {
		return errer.Err()
	}
	return nil
}

type spectrumAnalyzer struct {
	mu           sync.RWMutex
	source       beep.Streamer
	sampleRate   beep.SampleRate
	frequencies  []float64
	buffer       []float64
	bufPos       int
	levels       []float64
	enabled      bool
	computeEvery int
	computeCount int
}

func newSpectrumAnalyzer(freqs []float64, rate beep.SampleRate, windowSize int) *spectrumAnalyzer {
	a := &spectrumAnalyzer{
		frequencies:  freqs,
		levels:       make([]float64, len(freqs)),
		enabled:      true,
		computeEvery: max(1, spectrumComputeEvery),
	}
	a.SetSampleRate(rate, windowSize)
	return a
}

func (a *spectrumAnalyzer) SetSource(source beep.Streamer) {
	a.mu.Lock()
	a.source = source
	a.mu.Unlock()
}

func (a *spectrumAnalyzer) SetSampleRate(rate beep.SampleRate, windowSize int) {
	if windowSize <= 0 {
		windowSize = 1024
	}
	a.mu.Lock()
	a.sampleRate = rate
	a.buffer = make([]float64, windowSize)
	a.bufPos = 0
	a.computeCount = 0
	a.mu.Unlock()
}

func (a *spectrumAnalyzer) SetEnabled(enabled bool) {
	a.mu.Lock()
	a.enabled = enabled
	if !enabled {
		for i := range a.levels {
			a.levels[i] = 0
		}
		a.bufPos = 0
	}
	a.mu.Unlock()
}

func (a *spectrumAnalyzer) Snapshot() []float64 {
	a.mu.RLock()
	out := make([]float64, len(a.levels))
	copy(out, a.levels)
	a.mu.RUnlock()
	return out
}

func (a *spectrumAnalyzer) Stream(samples [][2]float64) (int, bool) {
	a.mu.RLock()
	source := a.source
	enabled := a.enabled
	a.mu.RUnlock()
	if source == nil {
		return 0, false
	}
	if !enabled {
		return source.Stream(samples)
	}
	n, ok := source.Stream(samples)
	if n == 0 {
		return n, ok
	}
	a.mu.Lock()
	for i := 0; i < n; i++ {
		mono := 0.5 * (samples[i][0] + samples[i][1])
		a.buffer[a.bufPos] = mono
		a.bufPos++
		if a.bufPos >= len(a.buffer) {
			a.computeCount++
			if a.computeCount >= a.computeEvery {
				a.computeLevels()
				a.computeCount = 0
			}
			a.bufPos = 0
		}
	}
	a.mu.Unlock()
	return n, ok
}

func (a *spectrumAnalyzer) Err() error {
	a.mu.RLock()
	source := a.source
	a.mu.RUnlock()
	if source == nil {
		return nil
	}
	if errer, ok := source.(interface{ Err() error }); ok {
		return errer.Err()
	}
	return nil
}

func (a *spectrumAnalyzer) computeLevels() {
	if a.sampleRate <= 0 || len(a.buffer) == 0 {
		return
	}
	maxPower := 0.0
	powers := make([]float64, len(a.frequencies))
	for i, freq := range a.frequencies {
		power := goertzelPower(a.buffer, freq, float64(a.sampleRate))
		power = math.Log10(1 + power)
		powers[i] = power
		if power > maxPower {
			maxPower = power
		}
	}
	if maxPower == 0 {
		for i := range a.levels {
			a.levels[i] = 0
		}
		return
	}
	for i, power := range powers {
		a.levels[i] = clampFloat(power/maxPower, 0, 1)
	}
}

func goertzelPower(samples []float64, freq float64, sampleRate float64) float64 {
	n := len(samples)
	if n == 0 || freq <= 0 || sampleRate <= 0 {
		return 0
	}
	k := 0.5 + float64(n)*freq/sampleRate
	omega := 2 * math.Pi * k / float64(n)
	coeff := 2 * math.Cos(omega)
	var s1, s2 float64
	for i, x := range samples {
		w := 0.54 - 0.46*math.Cos(2*math.Pi*float64(i)/float64(n-1))
		s := x*w + coeff*s1 - s2
		s2 = s1
		s1 = s
	}
	return s1*s1 + s2*s2 - coeff*s1*s2
}

func clampFloat(v, min, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
