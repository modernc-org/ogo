//go:build ignore

package main

const header = `// The Go gopher, drawn on an oscilloscope in X/Y mode by a Propeller 2.
//
// Two smart pins run as DACs. One is X, one is Y; put the scope in X/Y mode with a
// probe on each and the beam walks the outline, which persistence turns into a
// picture. There is no framebuffer and no display controller -- the figure IS the
// two voltages, drawn about a thousand times a second.
//
// Wiring: probe the two pins below against the board's ground. On a P2-EDGE the
// header pins are labelled; any two free pins work.
//
//	ogo run _examples/gopher
//
// SCOPE SETTINGS, which matter as much as the wiring on a DIGITAL scope:
//
//   - X/Y mode on, one probe per channel.
//   - 1 V/div on both channels, DC coupled. The DAC swings the full 0..3.3 V, so at
//     10 mV/div the figure is 300 screens tall and you see nothing.
//   - Timebase about 1 ms/div. This is the one that catches people out: a digital
//     scope in X/Y mode still captures a WINDOW of samples, and the figure only
//     appears if a whole frame fits in it. One frame here takes about 10 ms
//     (frameSamples * dwell), so a 14-division window at 1 ms/div holds one with
//     room to spare. At 100 ns/div the scope captures 0.01% of a frame and shows a
//     meaningless blob -- correctly.
//   - Persistence off for the dance, or a fraction of a second. Long persistence
//     piles the dance's frames on top of each other into a blur.
//
// If the picture is a diagonal line, both probes are on the same pin or the scope
// is not in X/Y mode. If it is a small fuzzy blob, the timebase is too short (see
// above). If it is a tiny signal of a few tens of millivolts, the pin is not
// driving: dacMode has lost its P_OE bit.

import "p2"

// Wiring and analog setup. These are the four numbers to change.
const (
	// xPin and yPin drive the two DACs. Any two free pins.
	xPin = 0
	yPin = 1

	// dacMode configures a pin as a DAC, from flexcc's smartpins.h:
	//
	//	P_DAC_990R_3V     0x140000   the 990-ohm, 3.3 V output range
	//	P_DAC_DITHER_PWM  0x000006   the smart-pin mode that takes a 16-bit level
	//	P_OE              0x000040   OUTPUT ENABLE -- without it the pin does not
	//	                             drive at all, and a scope sees a few tens of
	//	                             millivolts of dither ripple instead of a picture
	dacMode = 0x140046

	// dwell is how many clock cycles the beam rests on each sample. On a DIGITAL
	// scope this sets whether the picture is possible at all: the scope samples the
	// two channels at its own rate, and every point of the figure has to sit still
	// long enough to be caught at least once. 2000 cycles is 10 us at 200 MHz, which
	// puts one frame at about 10 ms -- a comfortable 1 ms/div.
	//
	// An ANALOG scope wants the opposite: as fast as the beam will follow, so try 40
	// there and turn the timebase down with it.
	dwell = 2000

	// steps is how many samples a segment between two points is drawn with. More is
	// a smoother line and a slower frame.
	steps = 5

	// still selects one frame of the dance and holds it, which is what a photograph
	// wants: the frames superimposed by a slow scope or a long persistence are a
	// blur. Set it to -1 to run the dance.
	still = 0

	// frameMs is how long one step of the dance is held, when it is running. Eight
	// frames at 120 ms is a loop about a second long.
	frameMs = 120
)

// pt is one point of the figure, in the 0..255 square an 8-bit DAC level spans.
type pt struct {
	x, y int
}

`

const footer = `
// setXY puts the beam at one point. The DAC takes a 16-bit level, so an 8-bit
// coordinate is shifted into the top of it.
func setXY(x int, y int) {
	p2.WritePinY(xPin, x<<8)
	p2.WritePinY(yPin, y<<8)
	p2.WaitCycles(dwell)
}

// lineTo walks the beam from a to b, which is what draws a line: the scope has no
// idea what a line is, it only ever shows where the beam is now.
func lineTo(a pt, b pt) {
	for i := 1; i <= steps; i++ {
		setXY(a.x+(b.x-a.x)*i/steps, a.y+(b.y-a.y)*i/steps)
	}
}

// drawFrame draws one step of the dance, once.
func drawFrame(f int) {
	at := 0
	for s := 0; s < nStrokes; s++ {
		n := strokeLen[s]
		// The jump to the start of a stroke is a single sample, so at most one dot
		// of it lands on the screen. Walking it would draw a line across the face.
		setXY(frames[f][at].x, frames[f][at].y)
		for k := 1; k < n; k++ {
			lineTo(frames[f][at+k-1], frames[f][at+k])
		}
		at += n
	}
}

func main() {
	p2.PinStart(xPin, dacMode, 0, 0)
	p2.PinStart(yPin, dacMode, 0, 0)

	if still >= 0 {
		// One frame, drawn over and over. Nothing moves, which is what a still
		// photograph of a moving beam needs.
		for {
			drawFrame(still)
		}
	}

	f := 0
	for {
		// Redraw the same frame until its time is up: one pass is far too fast to
		// see, and it is the repetition that makes the picture stand still.
		until := p2.GetMs() + frameMs
		for p2.GetMs() < until {
			drawFrame(f)
		}
		f++
		if f == nFrames {
			f = 0
		}
	}
}
`
