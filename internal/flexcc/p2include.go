// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package flexcc

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// p2includeArchive is flexprop's P2 include/lib tree (headers, libc sources and
// the prebuilt libc.a), packed as a deterministic gzip'd tar by
// internal/generator.go and refreshed on every backend regen so it stays locked
// to the pinned flexcc. It lets the in-repo flexcc compile+link P2 programs with
// no external flexprop install: Main extracts it and adds it to the include path.
// Attribution/license: LICENSE-flexprop (flexprop is MIT-licensed).
//
//go:embed p2include.tar.gz
var p2includeArchive []byte

var (
	p2includeOnce sync.Once
	p2includeDir  string
	p2includeErr  error
)

// p2IncludeDir extracts the embedded flexprop include tree to a per-user cache
// directory (once per process) and returns its path. The cache dir is keyed by a
// content hash of the archive, so a regenerated backend extracts to a fresh dir
// automatically and stale trees are never reused. Override the cache root with
// OGO_FLEXCC_CACHE.
func p2IncludeDir() (string, error) {
	p2includeOnce.Do(func() { p2includeDir, p2includeErr = extractP2Include() })
	return p2includeDir, p2includeErr
}

// stdlibIncludeArgs returns the "-I <dir>" pair that makes the in-repo flexcc
// self-contained, or nil when the caller opts out. Resolution order:
//
//   - an informational flag ("-h", "--help", "--version") or "--nostdlib"
//     anywhere in args: the caller either isn't compiling or explicitly declines
//     the standard include tree, so add nothing (and skip the extraction).
//   - FLEXPROP_INCLUDE set: use that external tree instead of the embedded one
//     (escape hatch for a bleeding-edge or custom flexprop).
//   - otherwise: extract and use the embedded tree.
func stdlibIncludeArgs(args []string) ([]string, error) {
	for _, a := range args {
		switch a {
		case "--nostdlib", "-h", "--help", "--version":
			return nil, nil
		}
	}
	if v := os.Getenv("FLEXPROP_INCLUDE"); v != "" {
		return []string{"-I", v}, nil
	}
	dir, err := p2IncludeDir()
	if err != nil {
		return nil, fmt.Errorf("flexcc: preparing embedded P2 include tree: %w", err)
	}
	return []string{"-I", dir}, nil
}

func cacheRoot() string {
	if v := os.Getenv("OGO_FLEXCC_CACHE"); v != "" {
		return v
	}
	if d, err := os.UserCacheDir(); err == nil {
		return filepath.Join(d, "ogo")
	}
	return filepath.Join(os.TempDir(), "ogo")
}

func extractP2Include() (string, error) {
	sum := sha256.Sum256(p2includeArchive)
	ver := hex.EncodeToString(sum[:])[:16]
	root := cacheRoot()
	dir := filepath.Join(root, "flexcc-p2include-"+ver)
	done := filepath.Join(dir, ".ok")
	if _, err := os.Stat(done); err == nil {
		return dir, nil // already extracted by an earlier run/process
	}

	// Extract into a sibling temp dir, then atomically rename into place, so a
	// concurrent build that is mid-extraction never exposes a partial tree.
	os.RemoveAll(dir) // clear any partial leftover from a crash
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(root, "p2inc-*")
	if err != nil {
		return "", err
	}
	if err := untar(p2includeArchive, tmp); err != nil {
		os.RemoveAll(tmp)
		return "", err
	}
	if err := os.WriteFile(filepath.Join(tmp, ".ok"), nil, 0o644); err != nil {
		os.RemoveAll(tmp)
		return "", err
	}
	if err := os.Rename(tmp, dir); err != nil {
		os.RemoveAll(tmp)
		if _, e := os.Stat(done); e == nil {
			return dir, nil // lost the race; the winner's tree is complete
		}
		return "", err
	}
	return dir, nil
}

func untar(gz []byte, dest string) error {
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		return err
	}
	defer zr.Close()

	destAbs, err := filepath.Abs(dest)
	if err != nil {
		return err
	}
	tr := tar.NewReader(zr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		target := filepath.Join(dest, hdr.Name)
		if rel, err := filepath.Rel(destAbs, target); err != nil ||
			rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return fmt.Errorf("flexcc: unsafe path in embedded include archive: %q", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		default:
			// The tree has no symlinks or specials (generator.go rejects them).
		}
	}
	return nil
}
