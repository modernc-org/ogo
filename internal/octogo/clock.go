// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

import "fmt"

// The P2's clock comes from a crystal through a PLL: the crystal frequency is
// divided by XDIV, multiplied by XMUL, then divided again by XDIVP. The backend
// picks none of this for a program that does not ask -- it falls back to a 20 MHz
// crystal times eight, which is where the 160 MHz default comes from, and which is a
// round multiplier rather than anything the silicon insists on.
//
// A program asks by declaring two CONSTANTS the backend looks up by name, _clkfreq
// and _clkmode, and it must be both or neither. Doing it that way rather than
// through a run-time _clkset is what keeps the console readable: the backend derives
// the serial divisor from the _clkfreq it can see, so the rate is right from the
// first byte. Set at run time instead and everything written before the baud is
// re-set comes out as line noise.
const (
	// defaultXtal is the crystal every P2 board this has met carries, and the one
	// the backend's own fallback assumes. A board with another one must say so:
	// nothing can ask the hardware, so an unstated crystal is believed.
	defaultXtal = 20000000

	// The PLL's internal frequency, XTAL/XDIV*XMUL, is kept inside this range, and
	// since the post-divide only ever lowers the result it also caps the system
	// clock at about 200 MHz.
	//
	// The lower bound is the PLL's. The upper one is a DELIBERATE LIMIT rather than a
	// measured wall: 200 MHz is the fastest this has been run and confirmed on
	// hardware, and the part is rated below it. A P2 will go faster -- boards are
	// commonly pushed past 300 MHz -- but that is overclocking, its margin depends on
	// the individual chip and its cooling, and a compiler should not do it because a
	// number was typed. Raising this is a decision to make on purpose, with a board
	// to check it against.
	vcoMin = 99000000
	vcoMax = 201000000
)

// clockSetting is a clock the hardware can actually produce.
type clockSetting struct {
	freq uint32 // the resulting frequency, exactly
	mode uint32 // the word _clkmode wants
}

// xppp encodes XDIVP the way the mode word carries it: 1 becomes 15, and an even
// divisor 2..30 becomes half of itself minus one. It mirrors smartpins.h's macro.
func xppp(divp int) uint32 { return uint32(((divp >> 1) + 15) & 0xF) }

// clockMode finds a crystal divide, multiply and post-divide that hit freq EXACTLY
// from xtal, and returns the mode word for them. It refuses rather than approximates:
// a clock that is close to what was asked for is a program whose every wait, baud
// rate and sample period is quietly wrong, and nothing downstream would report it.
//
// The search prefers a small post-divide and then a small crystal divide, which is
// what keeps the PLL's own frequency high and its jitter low -- the same preference
// the Parallax tools express.
func clockMode(xtal, freq int) (clockSetting, error) {
	if xtal <= 0 || freq <= 0 {
		return clockSetting{}, fmt.Errorf("clock: a frequency must be positive")
	}
	// XDIVP is 1 or an even number up to 30; XDIV is 1..64 and XMUL is 1..1024.
	divps := []int{1, 2, 4, 6, 8, 10, 12, 14, 16, 18, 20, 22, 24, 26, 28, 30}
	for _, divp := range divps {
		for div := 1; div <= 64; div++ {
			// vco = xtal/div*mul must be freq*divp, so mul follows from the rest.
			if xtal%div != 0 {
				continue
			}
			base := xtal / div
			want := freq * divp
			if want%base != 0 {
				continue
			}
			mul := want / base
			if mul < 1 || mul > 1024 {
				continue
			}
			vco := xtal / div * mul
			if vco < vcoMin || vco > vcoMax || vco/divp != freq {
				continue
			}
			mode := uint32(1)<<24 |
				uint32(div-1)<<18 |
				uint32(mul-1)<<8 |
				xppp(divp)<<4 |
				uint32(2)<<2 | // XOSC: a crystal with the usual loading
				uint32(3) //      XSEL: take the clock from the PLL
			return clockSetting{freq: uint32(freq), mode: mode}, nil
		}
	}
	if freq > vcoMax {
		return clockSetting{}, fmt.Errorf(
			"clock: %d Hz is above the %d Hz this compiler will ask for -- a P2 will run "+
				"faster, but that is overclocking and its margin is the individual board's",
			freq, vcoMax)
	}
	return clockSetting{}, fmt.Errorf(
		"clock: a %d Hz crystal cannot make exactly %d Hz, and a clock near the one asked "+
			"for is worse than none", xtal, freq)
}

// Clock makes the emitted program ask for a clock frequency instead of taking the
// backend's 160 MHz default, by declaring the two constants the backend reads.
func Clock(c clockSetting) EmitOption {
	return func(e *emitter) { e.clock = &c }
}

// ClockFor resolves a crystal and a wanted frequency into the setting Clock takes.
func ClockFor(xtal, freq int) (clockSetting, error) { return clockMode(xtal, freq) }

// DefaultXtal is the crystal frequency assumed when none is given.
const DefaultXtal = defaultXtal
