// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package octosmith // import "modernc.org/ogo/lib/internal/smith"

import (
	"os"
	"testing"
)

func Test(t *testing.T) {
	if err := Main(nil, os.Stdout, os.Stderr); err != nil {
		t.Fatal(err)
	}
}
