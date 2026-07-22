// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !windows && !darwin

package flexcc

import "modernc.org/libc"

// xFreopen is the linux (and other non-windows, non-darwin) half of the platform
// freopen split. The shared freopen wrapper in flexcc.go keeps the cc.fopen
// bookkeeping; only the raw C call diverges, because modernc.org/libc exports
// Xfreopen for linux but not for windows (served by supplement_windows_amd64.go)
// or darwin (served by supplement_darwin.go).
func xFreopen(tls *libc.TLS, filename, mode, file uintptr) uintptr {
	return libc.Xfreopen(tls, filename, mode, file)
}
