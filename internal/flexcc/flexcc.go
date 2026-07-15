// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package flexcc // import "modernc.org/ogo/lib/internal/flexcc"

import (
	"fmt"
	"io"
	"runtime"
	"unsafe"

	"modernc.org/libc"
)

// ccCurrent holds the *CC of the in-flight Main call so C-ABI callbacks (qsort
// comparators registered via __ccgo_fp) can recover it. The *CC-threading rewrite
// in internal/generator.go adds a cc parameter to every function, but libc's
// callback dispatch uses the original C signature and cannot pass cc through.
// Safe because Main is documented as not concurrent and pins its OS thread.
var ccCurrent *CC

// Main executes the equivalent of the original flexcc C main function.
//
// # Note
//
// Main is not safe for concurrent use by multiple goroutines.
//
// # Working Features and Known Limitations
//
//   - The 'stdin' argument is ignored.
func Main(stdin io.Reader, stdout, stderr io.Writer, args []string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			if code, ok := r.(exitCode); ok {
				if code != 0 {
					err = code
				}
				return
			}

			panic(r)
		}
	}()

	runtime.LockOSThread()

	defer runtime.UnlockOSThread()

	tls := libc.NewTLS()

	defer tls.Close()

	var pinner runtime.Pinner
	cc := newCC(&pinner)
	ccCurrent = cc // let __ccgo_fp qsort-comparator trampolines recover cc
	cc.stdin = stdin
	cc.stdout = stdout
	cc.stderr = stderr

	defer func() {
		for p := range cc.fopen {
			libc.Xfclose(tls, p)
		}
		for p := range cc.mallocs {
			libc.Xfree(tls, p)
		}
		pinner.Unpin()

	}()

	// Make the in-repo flexcc self-contained: unless the caller opted out, add
	// the embedded flexprop P2 include/lib tree to the front of the argument
	// list (options before the source files, matching flexcc's parser). Without
	// this the compiler cannot resolve <stdio.h>/<propeller2.h> or link libc.a.
	stdlib, err := stdlibIncludeArgs(args)
	if err != nil {
		return err
	}
	args = append(stdlib, args...)

	argv := allocArgs(tls, cc, append([]string{"flexcc"}, args...))
	if argv == 0 {
		return fmt.Errorf("failed to allocate 'args'")
	}

	exit(tls, cc, x__main(tls, cc, int32(len(args)+1), argv))
	panic("unreachable")
}

func allocArgs(tls *libc.TLS, cc *CC, args []string) (r uintptr) {
	nPtrs := len(args) + 1
	pPtrs := calloc(tls, cc, 1, libc.Tsize_t(uintptr(nPtrs)*unsafe.Sizeof(uintptr(0))))
	if pPtrs == 0 {
		return 0
	}

	ptrs := unsafe.Slice((*uintptr)(unsafe.Pointer(pPtrs)), nPtrs)
	nBytes := 0
	for _, v := range args {
		nBytes += len(v) + 1
	}
	pBytes := calloc(tls, cc, 1, libc.Tsize_t(nBytes))
	if pBytes == 0 {
		return 0
	}

	b := unsafe.Slice((*byte)(unsafe.Pointer(pBytes)), nBytes)
	for i, v := range args {
		copy(b, v)
		b = b[len(v)+1:]
		ptrs[i] = pBytes
		pBytes += uintptr(len(v)) + 1
	}
	return pPtrs
}

func calloc(t *libc.TLS, cc *CC, n, size libc.Tsize_t) (r uintptr) {
	if r = libc.Xcalloc(t, n, size); r != 0 {
		cc.mallocs[r] = struct{}{}
	}
	return r
}

type exitCode int32

func (ec exitCode) Error() string {
	return fmt.Sprintf("flexcc returned with status %v", int(ec))
}

func exit(t *libc.TLS, cc *CC, status int32) {
	panic(exitCode(status))
}

func fclose(tls *libc.TLS, cc *CC, f uintptr) (r1 int32) {
	delete(cc.fopen, f)
	return libc.Xfclose(tls, f)
}

func fopen(tls *libc.TLS, cc *CC, filename uintptr, mode uintptr) (r uintptr) {
	if r = libc.Xfopen(tls, filename, mode); r != 0 {
		cc.fopen[r] = struct{}{}
	}
	return r
}

func free(t *libc.TLS, cc *CC, p uintptr) {
	if p != 0 {
		delete(cc.mallocs, p)
		libc.Xfree(t, p)
	}
}

func freopen(tls *libc.TLS, cc *CC, filename uintptr, mode, file uintptr) (r uintptr) {
	delete(cc.fopen, file)
	if r = libc.Xfreopen(tls, filename, mode, file); r != 0 {
		cc.fopen[r] = struct{}{}
	}
	return r
}

const printbufSize = 1 << 20

var printbuf [printbufSize]byte

func fprintf(tls *libc.TLS, cc *CC, file, fmt uintptr, va uintptr) (r int32) {
	switch {
	case file == libc.Xstdout:
		return printf(tls, cc, fmt, va)
	case file == libc.Xstderr:
		r = libc.Xsnprintf(tls, uintptr(unsafe.Pointer(&printbuf[0])), printbufSize, fmt, va)
		if r > 0 {
			cc.stderr.Write(printbuf[:r])
		}
		return r
	default:
		return libc.Xfprintf(tls, file, fmt, va)
	}
}

func malloc(t *libc.TLS, cc *CC, size libc.Tsize_t) (r uintptr) {
	if r = libc.Xmalloc(t, size); r != 0 {
		cc.mallocs[r] = struct{}{}
	}
	return r
}

func printf(tls *libc.TLS, cc *CC, fmt uintptr, va uintptr) (r int32) {
	r = libc.Xsnprintf(tls, uintptr(unsafe.Pointer(&printbuf[0])), printbufSize, fmt, va)
	if r > 0 {
		cc.stdout.Write(printbuf[:r])
	}
	return r
}

func realloc(t *libc.TLS, cc *CC, p uintptr, size libc.Tsize_t) (r uintptr) {
	delete(cc.mallocs, p)
	if r = libc.Xrealloc(t, p, size); r != 0 {
		cc.mallocs[r] = struct{}{}
	}
	return r
}

func vfprintf(tls *libc.TLS, cc *CC, f uintptr, fmt uintptr, va uintptr) (r int32) {
	return fprintf(tls, cc, f, fmt, va)
}
