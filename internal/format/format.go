// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package format // import "modernc.org/octogo/lib/internal/format"

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"

	"modernc.org/ogo/internal/octogo"
	"modernc.org/opt"
)

type limiter chan struct{}

func newLimiter(limit int) limiter {
	if limit > 0 {
		return make(limiter, limit)
	}
	return nil
}

func (n limiter) limit() func() {
	if n == nil {
		return func() {}
	}

	n <- struct{}{}
	return func() { <-n }
}

// SubCommand implements "ogo format".
func SubCommand(args []string, stdin io.Reader, stdout, stderr io.Writer) (rc int, err error) {
	var write, list bool
	var paths []string
	var exclude *regexp.Regexp

	set := opt.NewSet()
	set.Arg("-exclude", false, func(_, arg string) error { exclude, err = regexp.Compile(arg); return err })
	set.Opt("l", func(_ string) error { list = true; return nil })
	set.Opt("w", func(_ string) error { write = true; return nil })

	if err := set.Parse(args, func(arg string) error {
		if strings.HasPrefix(arg, "-") {
			rc = 2
			return fmt.Errorf("unexpected flag: %v", arg)
		}
		paths = append(paths, arg)
		return nil
	}); err != nil {
		return 2, fmt.Errorf("%v", err)
	}

	// Default behavior: format stdin to stdout
	if len(paths) == 0 {
		b := bytes.NewBuffer(nil)
		if _, err = io.Copy(b, stdin); err != nil {
			return 1, fmt.Errorf("read stdin err: %v", err)
		}
		if err := octogo.FormatFile("<stdin>", b.Bytes(), stdout); err != nil {
			return 1, fmt.Errorf("fmt err: %v", err)
		}
		return rc, nil
	}

	// Gather all .ogo files
	var files []string
	for _, p := range paths {
		err := filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				fmt.Fprintf(stderr, "error accessing path %q: %v\n", path, err)
				return nil
			}
			if !d.IsDir() && strings.HasSuffix(path, ".ogo") && (exclude == nil || !exclude.MatchString(path)) {
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			return 1, fmt.Errorf("walk err: %v", err)
		}
	}

	// Concurrency setup
	lim := newLimiter(runtime.GOMAXPROCS(0))
	var wg sync.WaitGroup
	var mu sync.Mutex // Protects stdout/stderr writes

	for _, file := range files {
		wg.Add(1)
		go func(f string) {
			defer wg.Done()

			// Block until a worker slot opens
			done := lim.limit()
			defer done()

			src, err := os.ReadFile(f)
			if err != nil {
				mu.Lock()
				fmt.Fprintf(stderr, "read error %s: %v\n", f, err)
				mu.Unlock()
				rc = 1
				return
			}

			var buf bytes.Buffer
			if err := octogo.FormatFile(f, src, &buf); err != nil {
				mu.Lock()
				fmt.Fprintf(stderr, "format error %s: %v\n", f, err)
				mu.Unlock()
				rc = 1
				return
			}

			res := buf.Bytes()
			if !bytes.Equal(src, res) {
				// Formatting changed the file
				if list {
					mu.Lock()
					fmt.Fprintln(stdout, f)
					mu.Unlock()
				}
				if write {
					// Use same permissions as standard tools
					if err := os.WriteFile(f, res, 0644); err != nil {
						mu.Lock()
						fmt.Fprintf(stderr, "write error %s: %v\n", f, err)
						mu.Unlock()
						rc = 1
					}
				}
				if !list && !write {
					mu.Lock()
					stdout.Write(res)
					mu.Unlock()
				}
			}
		}(file)
	}

	wg.Wait()
	return rc, nil
}
