// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

import "testing"

// TestClockMode checks the mode words against two that are known independently, not
// against this function's own output. 0x010007fb is the one the BACKEND falls back to
// when a program asks for nothing -- read out of the transpiled sources, where the
// 160 MHz default is written -- so reproducing it means the encoding agrees with the
// compiler that consumes it. 0x010009fb was measured on a P2-EDGE, which reported
// 200000288 Hz for it. A golden made of this function's own answers would agree with
// it forever and prove nothing.
func TestClockMode(t *testing.T) {
	for _, tc := range []struct {
		xtal, freq int
		mode       uint32
		why        string
	}{
		{20000000, 160000000, 0x010007fb, "the backend's own fallback: 20 MHz times eight"},
		{20000000, 180000000, 0x010008fb, "measured on a P2-EDGE at 180068584 Hz"},
		{20000000, 200000000, 0x010009fb, "measured on a P2-EDGE at 200000288 Hz"},
	} {
		got, err := clockMode(tc.xtal, tc.freq)
		if err != nil {
			t.Errorf("clockMode(%d, %d): %v", tc.xtal, tc.freq, err)
			continue
		}
		if got.mode != tc.mode {
			t.Errorf("clockMode(%d, %d) = %#08x, want %#08x (%s)",
				tc.xtal, tc.freq, got.mode, tc.mode, tc.why)
		}
		if got.freq != uint32(tc.freq) {
			t.Errorf("clockMode(%d, %d) freq = %d", tc.xtal, tc.freq, got.freq)
		}
	}
}

// TestClockModeExact is the property that matters more than any single word: the
// setting must produce the frequency that was ASKED for, never one near it. A clock
// off by a percent is a program whose every wait, baud rate and sample period is
// quietly wrong, and nothing downstream reports it.
func TestClockModeExact(t *testing.T) {
	for freq := 1000000; freq <= 200000000; freq += 1000000 {
		got, err := clockMode(defaultXtal, freq)
		if err != nil {
			continue // not representable, which is a refusal and not a wrong answer
		}
		div := int((got.mode>>18)&0x3F) + 1
		mul := int((got.mode>>8)&0x3FF) + 1
		ppp := int(got.mode>>4) & 0xF
		divp := 1
		if ppp != 15 {
			divp = (ppp + 1) * 2
		}
		if have := defaultXtal / div * mul / divp; have != freq {
			t.Errorf("clockMode(%d) encodes %d Hz (div %d mul %d divp %d)",
				freq, have, div, mul, divp)
		}
	}
}

// TestClockModeRefuses checks that what cannot be made exactly is refused rather
// than rounded.
func TestClockModeRefuses(t *testing.T) {
	for _, freq := range []int{
		123456789, // prime-ish, no exact divisor chain
		400000000, // past what the PLL will run at
		-1,        // nonsense
	} {
		if got, err := clockMode(defaultXtal, freq); err == nil {
			t.Errorf("clockMode(%d) = %#08x, want a refusal", freq, got.mode)
		}
	}
}
