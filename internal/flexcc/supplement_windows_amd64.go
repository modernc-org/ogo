// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build windows && amd64

package flexcc

// This file supplies the C-library functions the transpiled windows backend
// (ccgo_windows_amd64.go) references but that modernc.org/libc does not provide
// for windows/amd64. generator.go cross-transpiles with -ignore-link-errors and
// --prefix-undefined=_, so any C function absent from libc's windows capi table
// is emitted as a bare, underscore-prefixed package-level name (e.g. remove ->
// _remove, getcwd -> __getcwd) for this file to define.
//
// Unlike the linux backend — where every one of these lives in libc — libc's
// windows build genuinely lacks them, so these are real implementations, not
// forwarders: Go stdlib for the self-contained ones (os/math), and the libc
// windows primitives that DO exist (X_strnicmp, Xlocaltime, Xstrftime, Xgetcwd)
// for the rest.
//
// Main is single-threaded and pins its OS thread, so the process-global static
// buffers below need no locking, matching the non-reentrant C originals.

import (
	"math"
	"os"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"modernc.org/libc"
)

// __getcwd is POSIX getcwd under MinGW's spelling; libc's windows Xgetcwd exists
// but takes size as Tsize_t, so this only adapts the int32 length the transpiled
// call passes.
func __getcwd(tls *libc.TLS, buf uintptr, size int32) uintptr {
	return libc.Xgetcwd(tls, buf, libc.Tsize_t(size))
}

// _iscntrl classifies a control character in the C locale: U+0000..U+001F and
// U+007F. EOF (-1) and every printable byte return 0.
func _iscntrl(tls *libc.TLS, c int32) int32 {
	if (c >= 0 && c < 0x20) || c == 0x7f {
		return 1
	}
	return 0
}

// _powl / _frexpl are the long double math routines; ccgo aliases long double to
// float64 on this target (type _double64 = float64), so Go's math package is an
// exact stand-in. _frexpl writes the binary exponent through its int* argument.
func _powl(tls *libc.TLS, x, y float64) float64 { return math.Pow(x, y) }

func _frexpl(tls *libc.TLS, x float64, exp uintptr) float64 {
	frac, e := math.Frexp(x)
	*(*int32)(unsafe.Pointer(exp)) = int32(e)
	return frac
}

// _remove deletes a file, returning 0 on success and -1 on failure, like C
// remove(3).
func _remove(tls *libc.TLS, path uintptr) int32 {
	if os.Remove(libc.GoString(path)) != nil {
		return -1
	}
	return 0
}

// _strncasecmp forwards to the windows CRT's case-insensitive _strnicmp, which
// libc does provide for windows and which has identical semantics.
func _strncasecmp(tls *libc.TLS, s1, s2 uintptr, n uint64) int32 {
	return libc.X_strnicmp(tls, s1, s2, libc.Tsize_t(n))
}

// _strncat appends at most n bytes of the C string src to the C string dst and
// NUL-terminates, returning dst — standard strncat(3) semantics, done directly on
// the C-heap bytes.
func _strncat(tls *libc.TLS, dst, src uintptr, n uint64) uintptr {
	d := dst
	for *(*byte)(unsafe.Pointer(d)) != 0 {
		d++
	}
	for i := uint64(0); i < n; i++ {
		c := *(*byte)(unsafe.Pointer(src + uintptr(i)))
		if c == 0 {
			break
		}
		*(*byte)(unsafe.Pointer(d)) = c
		d++
	}
	*(*byte)(unsafe.Pointer(d)) = 0
	return dst
}

// asctime/ctime static storage. asctime returns a pointer to a single reused
// buffer (the C original writes a file-static array); it plus the format string
// are allocated once on the C heap and never freed, exactly the lifetime of the C
// statics they replace.
var timeStatic struct {
	once sync.Once
	buf  uintptr // >= 26 bytes: "Www Mmm dd hh:mm:ss yyyy\n\0"
	fmt  uintptr // strftime format producing that exact layout
}

// asctimeFmt reproduces asctime's fixed 24-char layout via strftime: %e is the
// space-padded day of month, so column offsets match asctime/ctime byte for byte.
// mcpp's __DATE__/__TIME__ initialization slices ctime's result at fixed offsets,
// so the layout — not just the content — has to be right.
const asctimeFmt = "%a %b %e %H:%M:%S %Y\n\x00"

func timeStaticInit(tls *libc.TLS) {
	timeStatic.buf = libc.Xmalloc(tls, 32)
	timeStatic.fmt = libc.Xmalloc(tls, libc.Tsize_t(len(asctimeFmt)))
	copy(unsafe.Slice((*byte)(unsafe.Pointer(timeStatic.fmt)), len(asctimeFmt)), asctimeFmt)
}

// _asctime formats a struct tm into the shared static buffer and returns it.
func _asctime(tls *libc.TLS, tm uintptr) uintptr {
	timeStatic.once.Do(func() { timeStaticInit(tls) })
	libc.Xstrftime(tls, timeStatic.buf, 32, timeStatic.fmt, tm)
	return timeStatic.buf
}

// _ctime renders a time_t as local time in the same buffer, i.e. asctime of
// localtime, per C ctime(3).
func _ctime(tls *libc.TLS, t uintptr) uintptr {
	return _asctime(tls, libc.Xlocaltime(tls, t))
}

// xFreopen is the windows half of the platform freopen split (see flexcc.go's
// freopen wrapper and freopen_notwindows.go). modernc.org/libc exports Xfreopen
// only for linux, so windows approximates it as close-then-open: the returned
// stream is a fresh FILE*, not the original object C freopen would re-associate.
// That difference is inert today — the transpiled windows compiler never calls
// freopen (flexcc's output redirection reaches it only on other targets) — but a
// faithful-enough body is kept rather than a panic so a future windows code path
// degrades instead of crashing.
func xFreopen(tls *libc.TLS, filename, mode, file uintptr) uintptr {
	libc.Xfclose(tls, file)
	return libc.Xfopen(tls, filename, mode)
}

// The rest of this file replaces four libc functions that modernc.org/libc leaves
// as panic(todo()) stubs for windows. generator.go's main2lib redirects the
// transpiled calls (libc.Xtime -> xTime, ...) to these; on linux those libc
// entries are real and are left alone. Each is reached on an ordinary compile:
// xTime and xUngetc back the preprocessor's __DATE__/__TIME__ and the lexer's
// one/two-character pushback; xGetModuleFileNameA backs flexcc's executable-path
// probe; xAbort backs its fatal-error path.

// xTime returns the current time as a Unix time_t and, when tloc is non-nil,
// stores it there — C time(2). _time_t is the transpiled file's alias for int64.
func xTime(tls *libc.TLS, tloc uintptr) _time_t {
	now := _time_t(time.Now().Unix())
	if tloc != 0 {
		*(*_time_t)(unsafe.Pointer(tloc)) = now
	}
	return now
}

// xUngetc pushes one character back onto stream. flexcc only ever ungets a
// character it just read from a seekable source file, and at most two in LIFO
// order (UTF-8 BOM detection), so stepping the file position back one byte
// reproduces ungetc: the next fgetc re-reads that byte. EOF is a no-op, as in C.
// 1 is SEEK_CUR (libc exports no named constant); the untyped -1 converts to
// whatever offset type libc's windows Xfseek declares.
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

// GetModuleFileNameA is a genuine Win32 call (kernel32), bound directly since
// libc stubs it. lpFilename is an address in libc-managed C-heap memory, which is
// stable, so passing it to the syscall as a uintptr is safe.
var (
	kernel32               = syscall.NewLazyDLL("kernel32.dll")
	procGetModuleFileNameA = kernel32.NewProc("GetModuleFileNameA")
)

// xGetModuleFileNameA writes the path of the running executable into lpFilename
// and returns its length, or 0 on failure. flexcc uses it to locate a default
// include directory relative to the compiler binary; the in-repo flexcc adds the
// embedded P2 tree with an explicit -I regardless, so the result only needs to be
// a valid path, not a meaningful include root.
func xGetModuleFileNameA(tls *libc.TLS, hModule, lpFilename uintptr, nSize uint32) int32 {
	r, _, _ := procGetModuleFileNameA.Call(hModule, lpFilename, uintptr(nSize))
	return int32(r)
}

// xStat is C stat(2). libc's windows Xstat is real but delegates to Xstat64,
// which is itself a panic(todo()) stub, so it is redirected here like the direct
// stubs above. _stat, _time_t and the m__S_IF* mode bits are the transpiled
// file's own declarations. flexcc reads only st_mode (its directory test) and
// st_mtime (include freshness), so filling those from os.Stat — plus the
// permission bits and the other timestamps for good measure — is sufficient; a
// zeroed struct otherwise. Returns 0 on success, -1 if the path cannot be stat'd.
func xStat(tls *libc.TLS, path, buf uintptr) int32 {
	fi, err := os.Stat(libc.GoString(path))
	if err != nil {
		return -1
	}
	st := (*_stat)(unsafe.Pointer(buf))
	*st = _stat{}
	mode := uint16(m__S_IFREG)
	if fi.IsDir() {
		mode = uint16(m__S_IFDIR)
	}
	st.Fst_mode = mode | uint16(fi.Mode().Perm())
	mt := _time_t(fi.ModTime().Unix())
	st.Fst_atime, st.Fst_mtime, st.Fst_ctime = mt, mt, mt
	return 0
}
