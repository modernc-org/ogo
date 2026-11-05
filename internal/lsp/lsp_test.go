// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lsp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"modernc.org/ogo/internal/octogo"
)

// client drives a server through a pipe as an editor does.
type client struct {
	t     *testing.T
	in    io.WriteCloser
	msgs  chan map[string]any
	exit  chan int
	id    int
	diags map[string][]any // the last diagnostics published for a URI
}

func newClient(t *testing.T, encodings ...string) (*client, map[string]any) {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	s := newServer(outW, io.Discard, 10*time.Millisecond)
	c := &client{t: t, in: inW, msgs: make(chan map[string]any, 100), exit: make(chan int, 1), diags: map[string][]any{}}
	go func() {
		c.exit <- s.run(bufio.NewReader(inR))
		s.stop()
		outW.Close()
	}()
	go func() {
		r := bufio.NewReader(outR)
		for {
			b, err := readMessage(r)
			if err != nil {
				close(c.msgs)
				return
			}
			var m map[string]any
			if err := json.Unmarshal(b, &m); err != nil {
				panic(err)
			}
			c.msgs <- m
		}
	}()
	params := map[string]any{"processId": nil, "rootUri": nil, "capabilities": map[string]any{}}
	if len(encodings) != 0 {
		params["capabilities"] = map[string]any{"general": map[string]any{"positionEncodings": encodings}}
	}
	r := c.request("initialize", params)
	c.notify("initialized", map[string]any{})
	t.Cleanup(func() { inW.Close() })
	return c, r
}

func (c *client) send(m map[string]any) {
	m["jsonrpc"] = "2.0"
	b, err := json.Marshal(m)
	if err != nil {
		c.t.Fatal(err)
	}
	fmt.Fprintf(c.in, "Content-Length: %d\r\n\r\n%s", len(b), b)
}

func (c *client) notify(method string, params any) {
	c.send(map[string]any{"method": method, "params": params})
}

// request sends a request and returns its response, recording the diagnostics
// published meanwhile.
func (c *client) request(method string, params any) map[string]any {
	c.t.Helper()
	c.id++
	c.send(map[string]any{"id": c.id, "method": method, "params": params})
	for {
		m := c.next()
		if id, ok := m["id"].(float64); ok && int(id) == c.id {
			return m
		}
	}
}

func (c *client) next() map[string]any {
	c.t.Helper()
	select {
	case m, ok := <-c.msgs:
		if !ok {
			c.t.Fatal("the server closed its output")
		}
		if m["method"] == "textDocument/publishDiagnostics" {
			p := m["params"].(map[string]any)
			c.diags[p["uri"].(string)] = p["diagnostics"].([]any)
		}
		return m
	case <-time.After(60 * time.Second):
		c.t.Fatal("no message from the server in 60 s")
	}
	return nil
}

// diagnostics waits for diagnostics of uri to be published, and returns them.
func (c *client) diagnostics(uri string) []any {
	c.t.Helper()
	for {
		m := c.next()
		if m["method"] == "textDocument/publishDiagnostics" && m["params"].(map[string]any)["uri"] == uri {
			return c.diags[uri]
		}
	}
}

func (c *client) open(uri, text string) {
	c.notify("textDocument/didOpen", map[string]any{"textDocument": map[string]any{"uri": uri, "languageId": "ogo", "version": 1, "text": text}})
}

func (c *client) change(uri, text string, version int) {
	c.notify("textDocument/didChange", map[string]any{
		"textDocument":   map[string]any{"uri": uri, "version": version},
		"contentChanges": []any{map[string]any{"text": text}},
	})
}

func rangeOf(d any) (sl, sc, el, ec int) {
	r := d.(map[string]any)["range"].(map[string]any)
	s, e := r["start"].(map[string]any), r["end"].(map[string]any)
	return int(s["line"].(float64)), int(s["character"].(float64)), int(e["line"].(float64)), int(e["character"].(float64))
}

func msgOf(d any) string { return d.(map[string]any)["message"].(string) }

func TestInitialize(t *testing.T) {
	_, r := newClient(t)
	caps := r["result"].(map[string]any)["capabilities"].(map[string]any)
	if caps["positionEncoding"] != "utf-16" {
		t.Errorf("positionEncoding %v, want utf-16 for a client offering none", caps["positionEncoding"])
	}
	if sync := caps["textDocumentSync"].(map[string]any); sync["change"] != 1.0 || sync["openClose"] != true {
		t.Errorf("textDocumentSync %v, want full sync with open and close", sync)
	}
	if caps["documentFormattingProvider"] != true {
		t.Errorf("documentFormattingProvider %v, want true", caps["documentFormattingProvider"])
	}
	for k := range caps {
		switch k {
		case "positionEncoding", "textDocumentSync", "documentFormattingProvider":
		default:
			t.Errorf("capability %q declared, which the server does not implement", k)
		}
	}
	_, r = newClient(t, "utf-8", "utf-16")
	if enc := r["result"].(map[string]any)["capabilities"].(map[string]any)["positionEncoding"]; enc != "utf-8" {
		t.Errorf("positionEncoding %v, want utf-8 for a client offering it", enc)
	}
}

// TestDiagnostics is the acceptance of the handoff: an undefined name is one
// diagnostic over the name, and fixing it publishes none.
func TestDiagnostics(t *testing.T) {
	c, _ := newClient(t)
	dir := t.TempDir()
	uri := pathToURI(filepath.Join(dir, "main.ogo"))
	c.open(uri, "func main() {\n\tprintln(undefinedName)\n}\n")
	ds := c.diagnostics(uri)
	if len(ds) != 1 {
		t.Fatalf("got %v, want one diagnostic", ds)
	}
	if sl, sc, el, ec := rangeOf(ds[0]); sl != 1 || sc != 9 || el != 1 || ec != 22 {
		t.Errorf("range %d:%d-%d:%d, want 1:9-1:22, the name", sl, sc, el, ec)
	}
	if m := msgOf(ds[0]); m != "undefined: undefinedName" {
		t.Errorf("message %q", m)
	}
	if d := ds[0].(map[string]any); d["severity"] != 1.0 || d["source"] != "ogo" {
		t.Errorf("severity %v source %v, want 1 and ogo", d["severity"], d["source"])
	}

	c.change(uri, "func main() {\n\tprintln(1)\n}\n", 2)
	if ds := c.diagnostics(uri); len(ds) != 0 {
		t.Errorf("after the fix: got %v, want none", ds)
	}

	// A refusal the emitter makes is a diagnostic as the checker's are.
	c.change(uri, "func f() []byte {\n\tvar b [4]byte\n\treturn b[:]\n}\n\nfunc main() {\n\tprintln(len(f()))\n}\n", 3)
	ds = c.diagnostics(uri)
	if len(ds) != 1 || !strings.HasPrefix(msgOf(ds[0]), "cannot return a slice backed by local b") {
		t.Fatalf("got %v, want the lifetime refusal", ds)
	}
	if sl, sc, el, ec := rangeOf(ds[0]); sl != 2 || sc != 8 || el != 2 || ec != 9 {
		t.Errorf("range %d:%d-%d:%d, want 2:8-2:9", sl, sc, el, ec)
	}
}

// TestDiagnosticsAcrossPackages: an error in a library a program imports is
// published for the library's file, from the disk, and goes when the library is
// fixed in the editor.
func TestDiagnosticsAcrossPackages(t *testing.T) {
	c, _ := newClient(t)
	dir := t.TempDir()
	lib := filepath.Join(dir, "lib", "lib.ogo")
	if err := os.MkdirAll(filepath.Dir(lib), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lib, []byte("func F() int {\n\treturn missing\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mainURI, libURI := pathToURI(filepath.Join(dir, "main.ogo")), pathToURI(lib)
	c.open(mainURI, "import \"lib\"\n\nfunc main() {\n\tprintln(lib.F())\n}\n")
	var ds []any
	for {
		m := c.next()
		if m["method"] == "textDocument/publishDiagnostics" && c.diags[libURI] != nil && c.diags[mainURI] != nil {
			ds = c.diags[libURI]
			break
		}
	}
	if len(ds) != 1 || msgOf(ds[0]) != "undefined: missing" {
		t.Fatalf("library: got %v, want its undefined name", ds)
	}
	if sl, sc, _, ec := rangeOf(ds[0]); sl != 1 || sc != 8 || ec != 15 {
		t.Errorf("library range %d:%d-%d, want 1:8-15", sl, sc, ec)
	}
	if len(c.diags[mainURI]) != 0 {
		t.Errorf("main: got %v, want none", c.diags[mainURI])
	}

	c.open(libURI, "func F() int {\n\treturn 1\n}\n")
	deadline := time.Now().Add(60 * time.Second)
	for len(c.diags[libURI]) != 0 && time.Now().Before(deadline) {
		c.next()
	}
	if len(c.diags[libURI]) != 0 {
		t.Errorf("library fixed: still %v", c.diags[libURI])
	}
}

// TestTestFiles: a package's _test.ogo files are checked as ogo test checks them.
func TestTestFiles(t *testing.T) {
	c, _ := newClient(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "add.ogo"), []byte("func Add(a, b int) int {\n\treturn a + b\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	uri := pathToURI(filepath.Join(dir, "add_test.ogo"))
	c.open(uri, "import \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(1, 2) != three {\n\t\tt.Error(\"wrong\")\n\t}\n}\n")
	ds := c.diagnostics(uri)
	if len(ds) != 1 || msgOf(ds[0]) != "undefined: three" {
		t.Fatalf("got %v, want the test's undefined name", ds)
	}
}

// TestUTF8Positions: a client offered bytes is given bytes, and one offered
// nothing UTF-16 code units, past a line's non-ASCII text.
func TestUTF8Positions(t *testing.T) {
	const src = "func main() {\n\tprintln(\"é𝄞\", undefinedName)\n}\n"
	at := strings.Index(strings.Split(src, "\n")[1], "undefinedName")
	for _, c := range []struct {
		enc  []string
		want int
	}{
		{nil, at - 3}, // é is one unit, 𝄞 two, of five bytes
		{[]string{"utf-8"}, at},
	} {
		cl, _ := newClient(t, c.enc...)
		uri := pathToURI(filepath.Join(t.TempDir(), "main.ogo"))
		cl.open(uri, src)
		ds := cl.diagnostics(uri)
		if len(ds) != 1 {
			t.Fatalf("%v: got %v", c.enc, ds)
		}
		if _, sc, _, ec := rangeOf(ds[0]); sc != c.want || ec != c.want+len("undefinedName") {
			t.Errorf("%v: range %d-%d, want %d-%d", c.enc, sc, ec, c.want, c.want+len("undefinedName"))
		}
	}
}

func TestFormatting(t *testing.T) {
	c, _ := newClient(t)
	dir := t.TempDir()
	uri := pathToURI(filepath.Join(dir, "mis.ogo"))
	const mis = "func main(){println( \"a\" )\n}"
	c.open(uri, mis)
	r := c.request("textDocument/formatting", map[string]any{"textDocument": map[string]any{"uri": uri}, "options": map[string]any{"tabSize": 8, "insertSpaces": false}})
	edits := r["result"].([]any)
	if len(edits) != 1 {
		t.Fatalf("got %v, want one edit", r)
	}
	var want bytes.Buffer
	if err := octogo.FormatFile("mis.ogo", []byte(mis), &want); err != nil {
		t.Fatal(err)
	}
	e := edits[0].(map[string]any)
	if e["newText"] != want.String() {
		t.Errorf("newText %q, want ogo fmt's %q", e["newText"], want.String())
	}
	if sl, sc, el, ec := rangeOf(e); sl != 0 || sc != 0 || el != 1 || ec != 1 {
		t.Errorf("range %d:%d-%d:%d, want the whole document, 0:0-1:1", sl, sc, el, ec)
	}

	c.change(uri, want.String(), 2)
	if r := c.request("textDocument/formatting", map[string]any{"textDocument": map[string]any{"uri": uri}}); len(r["result"].([]any)) != 0 {
		t.Errorf("formatted already: got %v, want no edit", r["result"])
	}
	c.change(uri, "func main() {\n\tx :=\n}\n", 3)
	if r := c.request("textDocument/formatting", map[string]any{"textDocument": map[string]any{"uri": uri}}); len(r["result"].([]any)) != 0 {
		t.Errorf("not parsing: got %v, want no edit", r["result"])
	}
}

func TestLifecycle(t *testing.T) {
	c, _ := newClient(t)
	if r := c.request("textDocument/hover", map[string]any{}); r["error"].(map[string]any)["code"] != float64(codeMethodNotFound) {
		t.Errorf("an unsupported request: got %v, want MethodNotFound", r)
	}
	if r := c.request("shutdown", nil); r["error"] != nil || r["result"] != nil {
		t.Errorf("shutdown: got %v, want a null result", r)
	}
	c.notify("exit", nil)
	select {
	case code := <-c.exit:
		if code != 0 {
			t.Errorf("exit after shutdown: %d, want 0", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the server did not exit")
	}

	c, _ = newClient(t)
	c.notify("exit", nil)
	if code := <-c.exit; code != 1 {
		t.Errorf("exit without shutdown: %d, want 1", code)
	}
}

// TestCheckTime measures what an editor waits for after the debounce: the check
// of a package of a thousand lines, the handoff's measure being 100 ms.
func TestCheckTime(t *testing.T) {
	var b strings.Builder
	for i := 0; b.Len() == 0 || strings.Count(b.String(), "\n") < 1000; i++ {
		fmt.Fprintf(&b, "func f%d(a, b int) int {\n\tif a > b {\n\t\treturn a - b\n\t}\n\treturn b - a + %d\n}\n\n", i, i)
	}
	b.WriteString("func main() {\n\tprintln(f0(1, 2))\n}\n")
	dir := t.TempDir()
	fn := filepath.Join(dir, "main.ogo")
	if err := os.WriteFile(fn, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	s := newServer(io.Discard, io.Discard, 0)
	defer s.stop()
	s.check(dir) // warm
	t0 := time.Now()
	_, found := s.check(dir)
	t.Logf("%d lines checked in %v", strings.Count(b.String(), "\n"), time.Since(t0))
	if len(found) != 0 {
		t.Errorf("got %v, want no diagnostics", found)
	}
}
