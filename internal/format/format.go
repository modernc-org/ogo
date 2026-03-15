// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package format // import "modernc.org/octogo/lib/internal/format"

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"modernc.org/ogo/internal/octogo"
	"modernc.org/opt"
)

// SubCommand implements "ogo format".
func SubCommand(args []string, stdin io.Reader, stdout, stderr io.Writer) (rc int, err error) {
	set := opt.NewSet()
	if err := set.Parse(args, func(arg string) error {
		switch {
		case strings.HasPrefix(arg, "-"):
			rc = 2
			return fmt.Errorf("unexpected flag: %v", arg)
		default:
			rc = 2
			return fmt.Errorf("no non-flag arguments expected: %v", arg)
		}
		return nil
	}); err != nil {
		return 2, fmt.Errorf("%v", err)
	}

	b := bytes.NewBuffer(nil)
	if _, err = io.Copy(b, stdin); err != nil {
		return 1, fmt.Errorf("fmt err=%v", err)
	}

	if err := octogo.FormatFile("<stdin>", b.Bytes(), stdout); err != nil {
		return 1, fmt.Errorf("fmt err=%v", err)
	}

	return rc, nil
}
