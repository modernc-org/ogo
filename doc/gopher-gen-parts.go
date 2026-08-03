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
// If the picture is a diagonal line, the two probes are on the same pin or the
// scope is not in X/Y mode. If it is a bright dot, the frame loop is not running --
// check the ground lead. If it is squashed into one corner, see dacLevel below.

import "p2"

// Wiring and analog setup. These are the four numbers to change.
const (
	// xPin and yPin drive the two DACs. Any two free pins.
	xPin = 0
	yPin = 1

	// dacMode configures a pin as a DAC: P_DAC_990R_3V (0x140000) selects the
	// 990-ohm 3.3V range, and P_DAC_DITHER_PWM (0x06) the smart-pin mode that takes
	// a 16-bit level. Both are from flexcc's smartpins.h.
	dacMode = 0x140006

	// dwell is how many clock cycles the beam rests on each sample. Too few and a
	// sampling scope misses points, leaving the outline dotted; too many and the
	// frame rate drops until the picture flickers. At 200 MHz, 40 cycles is 200 ns.
	dwell = 40

	// steps is how many samples a segment between two points is drawn with. More is
	// a smoother line and a slower frame.
	steps = 5

	// frameMs is how long one step of the dance is held. Eight frames at 90 ms is a
	// loop a bit over a second long.
	frameMs = 90
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
