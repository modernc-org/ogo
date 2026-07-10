// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package flexcc // import "modernc.org/ogo/lib/internal/flexcc"

import (
	"bytes"
	"fmt"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}

func ExampleMain_help() {
	var stdout, stderr bytes.Buffer
	switch x := Main(nil, &stdout, &stderr, []string{"-h"}).(type) {
	case exitCode:
		if g, e := x, exitCode(2); g != e {
			panic(todo("", g, e))
		}
	default:
		panic(todo("%T(%v)", x, x))
	}

	fmt.Printf("%s", stdout.Bytes())
	// Output:
	// FlexC compiler (c) 2011-2026 Total Spectrum Software Inc. and contributors
	// Version 7.6.11-HEAD-v7.6.11 Compiled on: Jul 10 2026
	// usage: flexcc [options] file1.c file2.c ...
	//   [ --help ]         display this help
	//   [ -1bc ]           compile for Prop1 ROM bytecode
	//   [ -2 ]             compile for Prop2
	//   [ -2nu ]           compile for Prop2 bytecode
	//   [ -c ]             output only .o file
	//   [ -D <define> ]    add a define
	//   [ -g ]             include debug info in output
	//   [ -L or -I <path> ] add a directory to the include path
	//   [ -MMD ]           generate Make dependency file
	//   [ -o <name> ]      set output filename to <name>
	//   [ -O# ]            set optimization level:
	//           -O0 = no optimization
	//           -O1 = basic optimization
	//           -O2 = all optimization
	//   [ -Wall ]          enable warnings for language extensions and other features
	//   [ -Werror ]        make warnings into errors
	//   [ -Wabs-paths ]    print absolute paths for file names in errors/warnings
	//   [ -Wmax-errors=N ] allow at most N errors in a pass before stopping
	//   [ -x ]             capture program exit code (for testing)
	//   [ --code=cog ]     compile for COG mode instead of LMM
	//   [ --fcache=N ]     set FCACHE size to N (0 to disable)
	//   [ --fixedreal ]    use 16.16 fixed point in place of floats
	//   [ --lmm=xxx ]      use alternate LMM implementation for P1
	//            xxx = orig uses original flexspin LMM
	//            xxx = slow uses traditional (slow) LMM
	//   [ --nostdlib]      skip searching in the standard library location for include files
	//   [ --sizes]         print info about program sizes
	//   [ --verbose ]      print additional diagnostic messages (for debugging the compiler)
	//   [ --version ]      just show compiler version
	//   [ --zip ]          create zip archive of source files
}
