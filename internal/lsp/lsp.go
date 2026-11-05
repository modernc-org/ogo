// Copyright 2026 The OctoGo Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package lsp is `ogo lsp`: a Language Server Protocol 3.17 server over stdio, for
// an editor to show what `ogo build` would say of a package as it is edited, and to
// format a file as `ogo fmt` does.
//
// What it offers is what it does: the documents synced whole on every change, their
// diagnostics published, and formatting. A package is checked as `ogo build` checks
// it before the C backend -- the checker and then the emitter, where the lifetime
// and ABI refusals are made -- with the editor's buffers read over the files on
// disk, and its _test.ogo files checked as `ogo test` checks them. The backend's own
// refusals, a program that does not fit the cog or the hub, are a build's.
package lsp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing/fstest"
	"time"

	"modernc.org/ogo/internal/build"
	"modernc.org/ogo/internal/diag"
	"modernc.org/ogo/internal/octogo"
)

// Debounce is how long a package's files stay unchanged before it is checked.
const Debounce = 100 * time.Millisecond

// JSON-RPC and LSP error codes.
const (
	codeParseError           = -32700
	codeInvalidRequest       = -32600
	codeMethodNotFound       = -32601
	codeInvalidParams        = -32602
	codeServerNotInitialized = -32002
)

// Serve runs the server, reading the client's messages from in and writing to out,
// until the client says exit or in ends. It returns the process's exit status: 0
// for an exit after a shutdown, 1 otherwise, as the protocol asks. What goes wrong
// inside the server is written to log.
func Serve(in io.Reader, out, log io.Writer) int {
	s := newServer(out, log, Debounce)
	defer s.stop()
	return s.run(bufio.NewReader(in))
}

type server struct {
	out      io.Writer
	log      io.Writer
	debounce time.Duration
	wmu      sync.Mutex // one message written at a time

	initialized bool
	shutdown    bool
	utf8        bool // positions count bytes, not UTF-16 code units

	mu      sync.Mutex
	docs    map[string]*document               // open documents by absolute path
	timers  map[string]*time.Timer             // a package directory's debounce
	queue   []string                           // package directories to check, in order
	queued  map[string]bool                    //
	last    map[string]string                  // a package directory's last edited file
	results map[string]map[string][]diagnostic // by package directory, then file
	uris    map[string]string                  // the client's URI of a file it named
	wake    chan struct{}
	done    chan struct{}
	idle    sync.WaitGroup // a check under way
}

type document struct {
	uri     string
	text    []byte
	version int
}

func newServer(out, log io.Writer, debounce time.Duration) *server {
	s := &server{
		out:      out,
		log:      log,
		debounce: debounce,
		docs:     map[string]*document{},
		timers:   map[string]*time.Timer{},
		queued:   map[string]bool{},
		last:     map[string]string{},
		results:  map[string]map[string][]diagnostic{},
		uris:     map[string]string{},
		wake:     make(chan struct{}, 1),
		done:     make(chan struct{}),
	}
	go s.worker()
	return s
}

func (s *server) stop() {
	s.mu.Lock()
	for _, t := range s.timers {
		t.Stop()
	}
	s.mu.Unlock()
	close(s.done)
}

// message is any of the protocol's messages: a request has an id and a method, a
// notification a method only, a response an id only.
type message struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
}

func (s *server) run(r *bufio.Reader) int {
	for {
		b, err := readMessage(r)
		if err != nil {
			if err != io.EOF {
				fmt.Fprintf(s.log, "ogo lsp: %v\n", err)
			}
			return 1
		}
		var m message
		if err := json.Unmarshal(b, &m); err != nil {
			s.reply(json.RawMessage("null"), nil, &rpcError{codeParseError, err.Error()})
			continue
		}
		if m.Method == "" {
			continue // a response to a request of ours; there are none
		}
		if m.Method == "exit" {
			if s.shutdown {
				return 0
			}
			return 1
		}
		s.dispatch(m)
	}
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (s *server) dispatch(m message) {
	request := len(m.ID) != 0
	fail := func(code int, msg string) {
		if request {
			s.reply(m.ID, nil, &rpcError{code, msg})
		}
	}
	switch {
	case m.Method == "initialize":
		if s.initialized {
			fail(codeInvalidRequest, "initialize sent twice")
			return
		}
		s.initialize(m)
		return
	case !s.initialized:
		fail(codeServerNotInitialized, "the server is not initialized")
		return
	case s.shutdown:
		fail(codeInvalidRequest, "the server is shut down")
		return
	}
	switch m.Method {
	case "initialized":
	case "shutdown":
		s.shutdown = true
		s.reply(m.ID, nil, nil)
	case "textDocument/didOpen":
		var p struct {
			TextDocument struct {
				URI     string `json:"uri"`
				Version int    `json:"version"`
				Text    string `json:"text"`
			} `json:"textDocument"`
		}
		if json.Unmarshal(m.Params, &p) == nil {
			s.edited(p.TextDocument.URI, []byte(p.TextDocument.Text), p.TextDocument.Version, true)
		}
	case "textDocument/didChange":
		var p struct {
			TextDocument struct {
				URI     string `json:"uri"`
				Version int    `json:"version"`
			} `json:"textDocument"`
			ContentChanges []struct {
				Range *json.RawMessage `json:"range"`
				Text  string           `json:"text"`
			} `json:"contentChanges"`
		}
		if json.Unmarshal(m.Params, &p) == nil && len(p.ContentChanges) != 0 {
			// The sync is Full: each change is the whole document, and the last is
			// what the document is now.
			c := p.ContentChanges[len(p.ContentChanges)-1]
			if c.Range == nil {
				s.edited(p.TextDocument.URI, []byte(c.Text), p.TextDocument.Version, false)
			}
		}
	case "textDocument/didSave":
		var p struct {
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
		}
		if json.Unmarshal(m.Params, &p) == nil {
			if fn, ok := uriToPath(p.TextDocument.URI); ok {
				s.schedule(fn)
			}
		}
	case "textDocument/didClose":
		var p struct {
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
		}
		if json.Unmarshal(m.Params, &p) == nil {
			if fn, ok := uriToPath(p.TextDocument.URI); ok {
				s.mu.Lock()
				delete(s.docs, fn)
				s.mu.Unlock()
				s.schedule(fn)
			}
		}
	case "textDocument/formatting":
		var p struct {
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
		}
		if err := json.Unmarshal(m.Params, &p); err != nil {
			fail(codeInvalidParams, err.Error())
			return
		}
		s.reply(m.ID, s.format(p.TextDocument.URI), nil)
	default:
		fail(codeMethodNotFound, "method not supported: "+m.Method)
	}
}

func (s *server) initialize(m message) {
	var p struct {
		Capabilities struct {
			General struct {
				PositionEncodings []string `json:"positionEncodings"`
			} `json:"general"`
		} `json:"capabilities"`
	}
	if len(m.Params) != 0 {
		if err := json.Unmarshal(m.Params, &p); err != nil {
			s.reply(m.ID, nil, &rpcError{codeInvalidParams, err.Error()})
			return
		}
	}
	// A position counts UTF-16 code units unless the client offers bytes, which is
	// what the compiler counts.
	enc := "utf-16"
	if slices.Contains(p.Capabilities.General.PositionEncodings, "utf-8") {
		enc = "utf-8"
		s.utf8 = true
	}
	s.initialized = true
	s.reply(m.ID, map[string]any{
		"capabilities": map[string]any{
			"positionEncoding": enc,
			"textDocumentSync": map[string]any{
				"openClose": true,
				"change":    1, // Full
				"save":      map[string]any{"includeText": false},
			},
			"documentFormattingProvider": true,
		},
		"serverInfo": map[string]any{"name": "ogo"},
	}, nil)
}

// edited records what an editor holds of a file and schedules its package.
func (s *server) edited(uri string, text []byte, version int, open bool) {
	fn, ok := uriToPath(uri)
	if !ok {
		return
	}
	s.mu.Lock()
	if d := s.docs[fn]; d != nil || open {
		s.docs[fn] = &document{uri: uri, text: text, version: version}
	}
	s.uris[fn] = uri
	s.mu.Unlock()
	s.schedule(fn)
}

// schedule checks the package of the file fn once its files have stayed as they
// are for the debounce, and with it every package a change to fn may change the
// answer for: one whose last check reported on fn, and one the editor has open
// whose root fn is under, which may import it. Without an ogo.mod a package's
// directory is its root, and a library it imports is below it.
func (s *server) schedule(fn string) {
	if !strings.HasSuffix(fn, ".ogo") {
		return
	}
	dir := filepath.Dir(fn)
	s.mu.Lock()
	var reported, open []string
	for _, d := range sortedKeys(s.results) {
		if _, ok := s.results[d][fn]; ok && d != dir {
			reported = append(reported, d)
		}
	}
	for _, f := range sortedKeys(s.docs) {
		if d := filepath.Dir(f); d != dir && !slices.Contains(reported, d) && !slices.Contains(open, d) {
			open = append(open, d)
		}
	}
	s.last[dir] = fn
	s.debounceLocked(dir)
	for _, d := range reported {
		s.debounceLocked(d)
	}
	s.mu.Unlock()
	for _, d := range open {
		if root, err := build.BuildRoot(d); err == nil && within(root, dir) {
			s.mu.Lock()
			s.debounceLocked(d)
			s.mu.Unlock()
		}
	}
}

// within reports whether dir is root or below it.
func within(root, dir string) bool {
	rel, err := filepath.Rel(root, dir)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// debounceLocked queues the package in dir for a check once the debounce passes
// with no other call for it. s.mu is held.
func (s *server) debounceLocked(dir string) {
	if t := s.timers[dir]; t != nil {
		t.Stop()
	}
	s.timers[dir] = time.AfterFunc(s.debounce, func() {
		s.mu.Lock()
		if !s.queued[dir] {
			s.queued[dir] = true
			s.queue = append(s.queue, dir)
		}
		s.mu.Unlock()
		select {
		case s.wake <- struct{}{}:
		default:
		}
	})
}

// worker checks the packages scheduled, one at a time: a check of a large program
// takes the memory of a build.
func (s *server) worker() {
	for {
		select {
		case <-s.done:
			return
		case <-s.wake:
		}
		for {
			s.mu.Lock()
			if len(s.queue) == 0 {
				s.mu.Unlock()
				break
			}
			dir := s.queue[0]
			s.queue = s.queue[1:]
			delete(s.queued, dir)
			s.idle.Add(1)
			s.mu.Unlock()
			s.checkAndPublish(dir)
			s.idle.Done()
		}
	}
}

// diagnostic is one LSP Diagnostic.
type diagnostic struct {
	Range    lspRange `json:"range"`
	Severity int      `json:"severity"`
	Source   string   `json:"source"`
	Message  string   `json:"message"`
}

type lspRange struct {
	Start lspPosition `json:"start"`
	End   lspPosition `json:"end"`
}

type lspPosition struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// checkAndPublish checks the package in dir and publishes the diagnostics of every
// file it has, and of every other file the check or the one before it reported on.
func (s *server) checkAndPublish(dir string) {
	files, found := s.check(dir)
	s.mu.Lock()
	old := s.results[dir]
	s.results[dir] = found
	set := map[string]bool{}
	for _, fn := range files {
		set[fn] = true
	}
	for fn := range found {
		set[fn] = true
	}
	for fn := range old {
		set[fn] = true
	}
	var names []string
	for fn := range set {
		names = append(names, fn)
	}
	sort.Strings(names)
	type publish struct {
		uri   string
		ver   *int
		diags []diagnostic
	}
	var pubs []publish
	for _, fn := range names {
		// A file another package's check reported on keeps what that check said.
		diags := []diagnostic{}
		seen := map[diagnostic]bool{}
		for _, k := range sortedKeys(s.results) {
			for _, d := range s.results[k][fn] {
				if !seen[d] {
					seen[d] = true
					diags = append(diags, d)
				}
			}
		}
		p := publish{uri: s.uriOf(fn), diags: diags}
		if d := s.docs[fn]; d != nil {
			v := d.version
			p.ver = &v
		}
		pubs = append(pubs, p)
	}
	s.mu.Unlock()
	select {
	case <-s.done:
		return
	default:
	}
	for _, p := range pubs {
		params := map[string]any{"uri": p.uri, "diagnostics": p.diags}
		if p.ver != nil {
			params["version"] = *p.ver
		}
		s.notify("textDocument/publishDiagnostics", params)
	}
}

func sortedKeys[V any](m map[string]V) []string {
	r := make([]string, 0, len(m))
	for k := range m {
		r = append(r, k)
	}
	sort.Strings(r)
	return r
}

// uriOf is the URI the client named the file fn by, or the file's own.
func (s *server) uriOf(fn string) string {
	if u, ok := s.uris[fn]; ok {
		return u
	}
	return pathToURI(fn)
}

// check checks the package in dir as `ogo build` and `ogo test` check it, with the
// open documents read over the files on disk. It returns the package's files and
// the diagnostics by file, both by absolute path.
func (s *server) check(dir string) (files []string, found map[string][]diagnostic) {
	found = map[string][]diagnostic{}
	s.mu.Lock()
	last := s.last[dir]
	s.mu.Unlock()
	var ds []diag.Diagnostic
	var root string
	func() {
		defer func() {
			if e := recover(); e != nil {
				ds = append(ds, diag.Diagnostic{Message: fmt.Sprintf("internal compiler error: %v", e), Phase: "check"})
			}
		}()
		var err error
		if root, err = build.BuildRoot(dir); err != nil {
			ds = diag.FromError(err, "check")
			return
		}
		fsys, rel, modulePath, err := build.ModuleContext(dir)
		if err != nil {
			ds = diag.FromError(err, "check")
			return
		}
		ofs := overlayFS{fsys, s.overlay(root)}
		var srcs, tests []string
		des, _ := fs.ReadDir(ofs, rel)
		for _, de := range des {
			switch nm := de.Name(); {
			case de.IsDir() || !strings.HasSuffix(nm, ".ogo"):
			case strings.HasSuffix(nm, "_test.ogo"):
				tests = append(tests, nm)
			default:
				srcs = append(srcs, nm)
			}
		}
		for _, nm := range append(slices.Clip(srcs), tests...) {
			files = append(files, filepath.Join(dir, nm))
		}
		if len(srcs) != 0 {
			pkg, err := octogo.BuildModule(-1, modulePath, rel, srcs, ofs)
			if err == nil {
				err = octogo.EmitC(pkg, io.Discard, octogo.Checked(), octogo.FuncValueClasses(), octogo.Inline())
			}
			ds = append(ds, diag.FromError(err, "check")...)
		}
		if len(tests) != 0 {
			_, err := octogo.BuildModule(-1, modulePath, rel, append(slices.Clip(srcs), tests...), ofs)
			ds = append(ds, diag.FromError(err, "check")...)
		}
	}()
	seen := map[diag.Diagnostic]bool{}
	for _, d := range ds {
		if seen[d] {
			continue
		}
		seen[d] = true
		fn := last // an error with no position is shown at the file last edited
		if d.File != "" && root != "" {
			fn = filepath.Join(root, filepath.FromSlash(d.File))
		}
		if fn == "" {
			continue
		}
		var line string
		if d.Line > 0 {
			src := s.source(fn)
			d.SetEnd(src)
			line, _ = diag.Line(src, d.Line)
		}
		found[fn] = append(found[fn], diagnostic{
			Range: lspRange{
				Start: s.position(line, d.Line, d.Col),
				End:   s.position(line, d.EndLine, d.EndCol),
			},
			Severity: 1,
			Source:   "ogo",
			Message:  d.Message,
		})
	}
	return files, found
}

// position is the LSP position of a 1-based line and byte column in a line of text.
func (s *server) position(text string, line, col int) lspPosition {
	if line <= 0 || col <= 0 {
		return lspPosition{}
	}
	c := col - 1
	if !s.utf8 {
		c = diag.UTF16(text, c)
	}
	return lspPosition{Line: line - 1, Character: c}
}

// source is the text of the file fn as the editor holds it, or as it is on disk.
func (s *server) source(fn string) []byte {
	s.mu.Lock()
	d := s.docs[fn]
	s.mu.Unlock()
	if d != nil {
		return d.text
	}
	b, _ := os.ReadFile(fn)
	return b
}

// overlay is the open documents under root, by their path from it.
func (s *server) overlay(root string) fstest.MapFS {
	m := fstest.MapFS{}
	s.mu.Lock()
	defer s.mu.Unlock()
	for fn, d := range s.docs {
		if !within(root, fn) {
			continue
		}
		rel, _ := filepath.Rel(root, fn)
		m[path.Clean(filepath.ToSlash(rel))] = &fstest.MapFile{Data: d.text, Mode: 0o644, ModTime: time.Now()}
	}
	return m
}

// overlayFS reads from extra first and from FS otherwise: the editor's buffers over
// the files on disk.
type overlayFS struct {
	fs.FS
	extra fs.FS
}

func (o overlayFS) Open(name string) (fs.File, error) {
	if f, err := o.extra.Open(name); err == nil {
		if fi, err := f.Stat(); err == nil && !fi.IsDir() {
			return f, nil
		}
		f.Close()
	}
	return o.FS.Open(name)
}

func (o overlayFS) ReadFile(name string) ([]byte, error) {
	if b, err := fs.ReadFile(o.extra, name); err == nil {
		return b, nil
	}
	return fs.ReadFile(o.FS, name)
}

// Stat finds a directory that is only the editor's, a package of new files.
func (o overlayFS) Stat(name string) (fs.FileInfo, error) {
	if fi, err := fs.Stat(o.FS, name); err == nil {
		return fi, nil
	}
	return fs.Stat(o.extra, name)
}

// ReadDir lists a directory of both, extra's entry winning where both have one.
func (o overlayFS) ReadDir(name string) ([]fs.DirEntry, error) {
	base, err := fs.ReadDir(o.FS, name)
	extra, err2 := fs.ReadDir(o.extra, name)
	if err != nil && err2 != nil {
		return nil, err
	}
	seen := map[string]bool{}
	out := append([]fs.DirEntry(nil), extra...)
	for _, e := range extra {
		seen[e.Name()] = true
	}
	for _, e := range base {
		if !seen[e.Name()] {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out, nil
}

// format is the document formatted as `ogo fmt` formats it, as one edit replacing
// the whole of it, or no edit where it is formatted already or does not parse: the
// diagnostics say why then.
func (s *server) format(uri string) []textEdit {
	fn, ok := uriToPath(uri)
	if !ok {
		return []textEdit{}
	}
	src := s.source(fn)
	var buf bytes.Buffer
	if err := octogo.FormatFile(fn, src, &buf); err != nil || bytes.Equal(buf.Bytes(), src) {
		return []textEdit{}
	}
	return []textEdit{{Range: lspRange{End: s.end(src)}, NewText: buf.String()}}
}

type textEdit struct {
	Range   lspRange `json:"range"`
	NewText string   `json:"newText"`
}

// end is the position just past the last character of src.
func (s *server) end(src []byte) lspPosition {
	n := bytes.Count(src, []byte("\n"))
	last := string(src[bytes.LastIndexByte(src, '\n')+1:])
	c := len(last)
	if !s.utf8 {
		c = diag.UTF16(last, len(last))
	}
	return lspPosition{Line: n, Character: c}
}

func (s *server) reply(id json.RawMessage, result any, e *rpcError) {
	m := map[string]any{"jsonrpc": "2.0", "id": id}
	if e != nil {
		m["error"] = e
	} else {
		m["result"] = result
	}
	s.write(m)
}

func (s *server) notify(method string, params any) {
	s.write(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

func (s *server) write(m any) {
	b, err := json.Marshal(m)
	if err != nil {
		fmt.Fprintf(s.log, "ogo lsp: %v\n", err)
		return
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	fmt.Fprintf(s.out, "Content-Length: %d\r\n\r\n", len(b))
	s.out.Write(b)
}

// readMessage reads one message's content: headers to an empty line, then as many
// bytes as Content-Length says.
func readMessage(r *bufio.Reader) ([]byte, error) {
	n := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			if err == io.EOF && line == "" && n < 0 {
				return nil, io.EOF
			}
			return nil, fmt.Errorf("reading a header: %v", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		k, v, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(k), "Content-Length") {
			if n, err = strconv.Atoi(strings.TrimSpace(v)); err != nil || n < 0 {
				return nil, fmt.Errorf("bad Content-Length %q", v)
			}
		}
	}
	if n < 0 {
		return nil, fmt.Errorf("a message without Content-Length")
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return nil, fmt.Errorf("reading a message: %v", err)
	}
	return b, nil
}

// uriToPath is the absolute path a file URI names.
func uriToPath(uri string) (string, bool) {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return "", false
	}
	p := u.Path
	if runtime.GOOS == "windows" {
		p = strings.TrimPrefix(p, "/") // file:///C:/x is C:/x
		if u.Host != "" {
			p = "//" + u.Host + "/" + p
		}
	}
	return filepath.Clean(filepath.FromSlash(p)), true
}

// pathToURI is the file URI of an absolute path.
func pathToURI(fn string) string {
	p := filepath.ToSlash(fn)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p // a drive letter
	}
	return (&url.URL{Scheme: "file", Path: p}).String()
}
