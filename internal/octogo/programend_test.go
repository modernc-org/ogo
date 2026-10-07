// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octogo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestOnBoardProgramEnd: a program ends as Go's does, every cog with it -- at an
// unrecovered panic on any cog, and where main returns. On the target a stopped cog
// stopped alone: main printed "main survived" after a goroutine's panic, and a
// goroutine printed after main had returned. The table's board check reads until
// the expected text and no further, so this one reads on and asks that nothing
// followed.
func TestOnBoardProgramEnd(t *testing.T) {
	port := os.Getenv("OGO_BOARD_PORT")
	if port == "" {
		t.Skip("set OGO_BOARD_PORT (e.g. /dev/ttyUSB0) to run the on-board tests")
	}
	ogo := buildOgoCLI(t)
	for _, test := range []struct {
		name, src, stop, want, never string
	}{
		{"a goroutine's panic", `import "p2"

var started chan bool

var stop bool

func worker() {
	var a [2]int
	i := 2
	started <- true
	for !stop {
	}
	println(a[i])
}

func main() {
	go worker()
	<-started
	println("before")
	stop = true
	p2.WaitMs(200)
	println("main survived")
}
`, "panic:", "before\npanic: index out of range", "survived"},
		{"main's return", `import "p2"

var started chan bool

func chatter() {
	started <- true
	p2.WaitMs(100)
	println("goroutine survived")
}

func main() {
	go chatter()
	<-started
	println("main returns")
}
`, "main returns", "main returns", "survived"},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "prog.binary")
			if err := boardBuild(ogo, dir, "prog", test.src, bin, ""); err != nil {
				t.Fatal(err)
			}
			for attempt := 1; ; attempt++ {
				out, matched := boardLoadGrace(ogo, port, bin, test.stop, 600*time.Millisecond)
				out = strings.ReplaceAll(out, "\r", "")
				if matched && strings.Contains(out, test.want) {
					if strings.Contains(out, test.never) {
						t.Fatalf("a cog outlived the program:\n%s", out)
					}
					return
				}
				if attempt == boardAttempts {
					t.Fatalf("board output did not contain %q after %d attempts\ngot:\n%s", test.want, boardAttempts, out)
				}
				t.Logf("retry %d/%d (transient serial flake)", attempt, boardAttempts-1)
			}
		})
	}
}
