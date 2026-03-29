package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/faiface/beep"
	"github.com/faiface/beep/effects"
	"github.com/faiface/beep/mp3"
	"github.com/faiface/beep/speaker"
)

type Volume struct {
	Streamer beep.Streamer
	Base     float64
	Volume   float64
	Silent   bool
}

func main() {
	f, err := os.Open("JakoJako - Metamorphose - 01 Objekt.mp3")

	if err != nil {
		log.Fatal(err)
	}

	streamer, format, err := mp3.Decode(f)

	if err != nil {
		log.Fatal(err)
	}

	defer streamer.Close()

	speaker.Init(format.SampleRate, format.SampleRate.N(time.Second/10))

	ctrl := &beep.Ctrl{Streamer: beep.Loop(-1, streamer), Paused: false}
	volume := &effects.Volume{
		Streamer: ctrl,
		Base:     2,
		Volume:   0,
		Silent:   false,
	}
	speaker.Play(volume)

	for {
		fmt.Print("Press [ENTER] to pause/resume.")
		fmt.Scanln()
		speaker.Lock()
		ctrl.Paused = !ctrl.Paused
		volume.Volume += 0.5
		speaker.Unlock()
	}

}
