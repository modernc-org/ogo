// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package loadp2

import (
	"slices"
	"testing"
)

func TestBuildArgs(t *testing.T) {
	for _, tc := range []struct {
		name string
		o    Options
		want []string
	}{
		{
			name: "binary only (default clock + baud applied)",
			o:    Options{Binary: "blink.binary"},
			want: []string{"loadp2", "-f", "200000000", "-b", "230400", "blink.binary"},
		},
		{
			name: "ogo run (flash + terminal)",
			o:    Options{Binary: "blink.binary", Port: "/dev/ttyUSB0", Terminal: true},
			want: []string{"loadp2", "-p", "/dev/ttyUSB0", "-f", "200000000", "-b", "230400", "-t", "blink.binary"},
		},
		{
			name: "ogo test (quiet, watch exit sequence)",
			o:    Options{Binary: "blink.binary", Quiet: true, Verbose: true},
			want: []string{"loadp2", "-f", "200000000", "-b", "230400", "-v", "-q", "blink.binary"},
		},
		{
			name: "explicit UserBaud overrides the default",
			o:    Options{Binary: "blink.binary", UserBaud: 921600},
			want: []string{"loadp2", "-f", "200000000", "-b", "921600", "blink.binary"},
		},
		{
			name: "explicit ClockHz overrides the default",
			o:    Options{Binary: "blink.binary", ClockHz: 160000000},
			want: []string{"loadp2", "-f", "160000000", "-b", "230400", "blink.binary"},
		},
		{
			name: "extra flags appended after binary",
			o:    Options{Binary: "blink.binary", Extra: []string{"-a", "arg1", "arg2"}},
			want: []string{"loadp2", "-f", "200000000", "-b", "230400", "blink.binary", "-a", "arg1", "arg2"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := buildArgs(tc.o); !slices.Equal(got, tc.want) {
				t.Errorf("buildArgs(%+v)\n got %q\nwant %q", tc.o, got, tc.want)
			}
		})
	}
}
