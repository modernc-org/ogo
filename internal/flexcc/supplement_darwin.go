// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build darwin && (amd64 || arm64)

package flexcc

// This file supplies the C-library functions the transpiled darwin backends
// (ccgo_darwin_<goarch>.go) reference but that modernc.org/libc does not provide
// for darwin. generator.go transpiles with -ignore-link-errors and
// --prefix-undefined=_, so any C function absent from libc's darwin capi table is
// emitted as a bare, underscore-prefixed package-level name (e.g. stpcpy ->
// _stpcpy) for this file to define. The two darwin architectures share the macOS
// ABI, so one file serves both.
//
// Most are Go stdlib for the self-contained ones or thin forwarders to the libc
// darwin primitives that DO exist (Xwctomb, Xfseek, Xftell). Main is
// single-threaded and pins its OS thread, so the process-global static buffers
// need no locking, matching the non-reentrant C originals.

import (
	"math"
	"sync"
	"unsafe"

	"modernc.org/libc"
)

// __NSGetExecutablePath backs flexcc's executable-path probe (used to find a
// default include directory relative to the compiler binary). The in-repo flexcc
// adds the embedded P2 tree with an explicit -I regardless, so the path is never
// needed; report failure (-1), which the C caller handles by falling back.
func __NSGetExecutablePath(tls *libc.TLS, buf, bufsize uintptr) int32 {
	return -1
}

// _stpcpy copies the C string src to dst including the terminating NUL and returns
// a pointer to that NUL — standard stpcpy(3), done directly on the C-heap bytes.
func _stpcpy(tls *libc.TLS, dst, src uintptr) uintptr {
	for {
		c := *(*byte)(unsafe.Pointer(src))
		*(*byte)(unsafe.Pointer(dst)) = c
		if c == 0 {
			return dst
		}
		src++
		dst++
	}
}

// _wcrtomb converts the wide character wc to its multibyte encoding at s. flexcc
// uses no shift state, so the mbstate_t argument is ignored and this forwards to
// libc's stateless Xwctomb, returning the byte count as a size_t.
func _wcrtomb(tls *libc.TLS, s uintptr, wc _wchar_t, ps uintptr) uint64 {
	return uint64(libc.Xwctomb(tls, s, int32(wc)))
}

// _fseeko / _ftello are the off_t (64-bit) seek/tell. libc's darwin build exports
// only the long-typed Xfseek/Xftell, which on the LP64 macOS ABI are the same
// width, so these adapt the names.
func _fseeko(tls *libc.TLS, stream uintptr, off int64, whence int32) int32 {
	return libc.Xfseek(tls, stream, off, whence)
}

func _ftello(tls *libc.TLS, stream uintptr) int64 {
	return int64(libc.Xftell(tls, stream))
}

// _powl / _frexpl are the long double math routines; on arm64/amd64 darwin ccgo
// aliases long double to float64, so Go's math package is an exact stand-in.
func _powl(tls *libc.TLS, x, y float64) float64 { return math.Pow(x, y) }

func _frexpl(tls *libc.TLS, x float64, exp uintptr) float64 {
	frac, e := math.Frexp(x)
	*(*int32)(unsafe.Pointer(exp)) = int32(e)
	return frac
}

// asctime static storage: asctime returns a pointer to a single reused buffer (the
// C original writes a file-static array); it plus the strftime format are allocated
// once on the C heap and never freed, matching the lifetime of the C statics.
var timeStatic struct {
	once sync.Once
	buf  uintptr // >= 26 bytes: "Www Mmm dd hh:mm:ss yyyy\n\0"
	fmt  uintptr // strftime format producing that exact layout
}

// asctimeFmt reproduces asctime's fixed 24-char layout via strftime: %e is the
// space-padded day of month, so column offsets match asctime byte for byte.
// mcpp's __DATE__/__TIME__ initialization slices the result at fixed offsets, so
// the layout — not just the content — has to be right.
const asctimeFmt = "%a %b %e %H:%M:%S %Y\n\x00"

// _asctime formats a struct tm into the shared static buffer and returns it.
func _asctime(tls *libc.TLS, tm uintptr) uintptr {
	timeStatic.once.Do(func() {
		timeStatic.buf = libc.Xmalloc(tls, 32)
		timeStatic.fmt = libc.Xmalloc(tls, libc.Tsize_t(len(asctimeFmt)))
		copy(unsafe.Slice((*byte)(unsafe.Pointer(timeStatic.fmt)), len(asctimeFmt)), asctimeFmt)
	})
	libc.Xstrftime(tls, timeStatic.buf, 32, timeStatic.fmt, tm)
	return timeStatic.buf
}

// _freopen and xFreopen: modernc.org/libc exports no Xfreopen for darwin, so both
// approximate C freopen as close-then-open. _freopen is the transpiled call (ccgo
// could not resolve freopen, so it is a --prefix-undefined dangler); xFreopen is
// the darwin half of the platform freopen split flexcc.go's freopen wrapper calls
// (see freopen_notwindows.go, which serves the linux half). The returned stream is
// a fresh FILE*, not the original object C freopen would re-associate — inert here,
// as flexcc's output redirection is barely exercised.
func _freopen(tls *libc.TLS, filename, mode, file uintptr) uintptr {
	libc.Xfclose(tls, file)
	return libc.Xfopen(tls, filename, mode)
}

func xFreopen(tls *libc.TLS, filename, mode, file uintptr) uintptr {
	return _freopen(tls, filename, mode, file)
}

// xUngetc and xAbort replace the two libc functions that are panic(todo()) stubs
// for darwin. generator.go's main2lib redirects the transpiled calls
// (libc.Xungetc -> xUngetc, libc.Xabort -> xAbort); on linux those libc entries
// are real and are left alone.

// xUngetc pushes one character back onto stream. flexcc only ever ungets a
// character it just read from a seekable source file, and at most two in LIFO
// order (UTF-8 BOM detection), so stepping the file position back one byte
// reproduces ungetc: the next fgetc re-reads that byte. EOF is a no-op, as in C.
// 1 is SEEK_CUR.
func xUngetc(tls *libc.TLS, c int32, stream uintptr) int32 {
	if c == -1 {
		return -1
	}
	if libc.Xfseek(tls, stream, -1, 1) != 0 {
		return -1
	}
	return c
}

// xAbort ends the compile the way flexcc's abort(3) callers intend — abnormal
// termination — but without taking the host process down: it unwinds through the
// same exitCode panic flexcc.Main recovers (see flexcc.go), reporting failure.
// 134 is the conventional 128+SIGABRT status.
func xAbort(tls *libc.TLS) {
	panic(exitCode(134))
}
