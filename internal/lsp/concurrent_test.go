// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lsp

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"modernc.org/ogo/internal/octogo"
)

// TestConcurrentCheckAndFormat runs checks beside formatting, as a format request
// answered while a check runs does: under -race it asks that the compiler keeps no
// state the two share.
func TestConcurrentCheckAndFormat(t *testing.T) {
	dir := t.TempDir()
	src := []byte("import \"math\"\n\nfunc main() {\n\tprintln(math.Sqrt(2))\n}\n")
	if err := os.WriteFile(filepath.Join(dir, "main.ogo"), src, 0o644); err != nil {
		t.Fatal(err)
	}
	s := newServer(io.Discard, io.Discard, 0)
	defer s.stop()
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); s.check(dir) }()
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				var b bytes.Buffer
				octogo.FormatFile("x.ogo", src, &b)
			}
		}()
	}
	wg.Wait()
}
