// Package lsp is a language server for BetterGo (.bgo).
package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/types"
	"io"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"unicode/utf16"

	"bettergo/internal/compile"
)

// Serve speaks LSP over r/w until the client asks to exit.
func Serve(r io.Reader, w io.Writer) error {
	s := &server{docs: map[string]*doc{}, in: bufio.NewReader(r), out: w}
	return s.loop()
}

type doc struct {
	text string
	res  *compile.Result
	info *types.Info
}

type server struct {
	mu   sync.Mutex
	docs map[string]*doc
	in   *bufio.Reader
	out  io.Writer
}

func (s *server) loop() error {
	for {
		body, err := readMessage(s.in)
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		var msg struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(body, &msg); err != nil {
			continue
		}
		if msg.Method == "" {
			continue
		}
		if msg.Method == "exit" {
			return nil
		}
		resp, notify := s.handle(msg.Method, msg.ID, msg.Params)
		if resp != nil {
			if err := writeMessage(s.out, resp); err != nil {
				return err
			}
		}
		for _, n := range notify {
			if err := writeMessage(s.out, n); err != nil {
				return err
			}
		}
	}
}

func (s *server) handle(method string, id, params json.RawMessage) (any, []any) {
	switch method {
	case "initialize":
		return rpcResult(id, map[string]any{
			"capabilities": map[string]any{
				"textDocumentSync":         1,
				"hoverProvider":            true,
				"definitionProvider":       true,
				"documentFormattingProvider": true,
				"completionProvider":       map[string]any{"triggerCharacters": []string{"."}},
				"semanticTokensProvider": map[string]any{
					"legend": map[string]any{
						"tokenTypes":     tokenTypes,
						"tokenModifiers": []string{},
					},
					"full": true,
				},
				"diagnosticProvider": map[string]any{
					"interFileDependencies": false,
					"workspaceDiagnostics":  false,
				},
			},
			"serverInfo": map[string]any{"name": "bettergo"},
		}), nil
	case "initialized", "shutdown":
		if method == "shutdown" {
			return rpcResult(id, nil), nil
		}
		return nil, nil
	case "textDocument/didOpen":
		var p struct {
			TextDocument struct {
				URI  string `json:"uri"`
				Text string `json:"text"`
			} `json:"textDocument"`
		}
		_ = json.Unmarshal(params, &p)
		return nil, []any{s.publish(p.TextDocument.URI, p.TextDocument.Text)}
	case "textDocument/didChange":
		var p struct {
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
			ContentChanges []struct {
				Text string `json:"text"`
			} `json:"contentChanges"`
		}
		_ = json.Unmarshal(params, &p)
		if len(p.ContentChanges) == 0 {
			return nil, nil
		}
		text := p.ContentChanges[len(p.ContentChanges)-1].Text
		return nil, []any{s.publish(p.TextDocument.URI, text)}
	case "textDocument/didSave":
		var p struct {
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
		}
		_ = json.Unmarshal(params, &p)
		s.mu.Lock()
		d := s.docs[p.TextDocument.URI]
		s.mu.Unlock()
		if d == nil {
			return nil, nil
		}
		return nil, []any{s.publish(p.TextDocument.URI, d.text)}
	case "textDocument/formatting":
		var p struct {
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
		}
		_ = json.Unmarshal(params, &p)
		s.mu.Lock()
		d := s.docs[p.TextDocument.URI]
		s.mu.Unlock()
		text := ""
		if d != nil {
			text = d.text
		}
		formatted, err := compile.Format([]byte(text))
		if err != nil {
			return rpcResult(id, []any{}), nil
		}
		return rpcResult(id, []any{map[string]any{
			"range": rangeAll(text),
			"newText": string(formatted),
		}}), nil
	case "textDocument/hover":
		return rpcResult(id, s.hover(params)), nil
	case "textDocument/definition":
		return rpcResult(id, s.definition(params)), nil
	case "textDocument/completion":
		return rpcResult(id, keywordCompletion()), nil
	case "textDocument/semanticTokens/full":
		var p struct {
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
		}
		_ = json.Unmarshal(params, &p)
		s.mu.Lock()
		d := s.docs[p.TextDocument.URI]
		s.mu.Unlock()
		text := ""
		if d != nil {
			text = d.text
		}
		return rpcResult(id, map[string]any{"data": semanticTokens(text)}), nil
	case "textDocument/diagnostic":
		var p struct {
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
		}
		_ = json.Unmarshal(params, &p)
		s.mu.Lock()
		d := s.docs[p.TextDocument.URI]
		s.mu.Unlock()
		var diags []any
		if d != nil {
			diags = diagnostics(d)
		}
		return rpcResult(id, map[string]any{"kind": "full", "items": diags}), nil
	default:
		if len(id) == 0 || string(id) == "null" {
			return nil, nil
		}
		return rpcResult(id, nil), nil
	}
}

func (s *server) publish(uri, text string) any {
	path := uriPath(uri)
	res, err := compile.ParseLower(path, []byte(text))
	d := &doc{text: text, res: res}
	if res != nil {
		info, _, _ := compile.Check(res)
		d.info = info
	}
	s.mu.Lock()
	s.docs[uri] = d
	s.mu.Unlock()
	items := diagnostics(d)
	if err != nil {
		items = append(parseDiags(err, text), items...)
	}
	return map[string]any{
		"jsonrpc": "2.0",
		"method":  "textDocument/publishDiagnostics",
		"params": map[string]any{
			"uri":         uri,
			"diagnostics": items,
		},
	}
}

func (s *server) hover(params json.RawMessage) any {
	uri, line, col := textPos(params)
	id, info, d := s.identAt(uri, line, col)
	if id == nil || info == nil {
		return nil
	}
	obj := info.ObjectOf(id)
	if obj == nil {
		return nil
	}
	return map[string]any{
		"contents": map[string]any{"kind": "markdown", "value": "```go\n" + obj.String() + "\n```"},
		"range":    identRange(d.text, id, d.res),
	}
}

func (s *server) definition(params json.RawMessage) any {
	uri, line, col := textPos(params)
	id, info, d := s.identAt(uri, line, col)
	if id == nil || info == nil || d == nil || d.res == nil {
		return nil
	}
	obj := info.ObjectOf(id)
	if obj == nil || !obj.Pos().IsValid() {
		return nil
	}
	p := d.res.Fset.Position(obj.Pos())
	return map[string]any{
		"uri":   uri,
		"range": map[string]any{"start": lspPos(d.text, p.Line, p.Column), "end": lspPos(d.text, p.Line, p.Column+len(obj.Name()))},
	}
}

func (s *server) identAt(uri string, line, col int) (*ast.Ident, *types.Info, *doc) {
	s.mu.Lock()
	d := s.docs[uri]
	s.mu.Unlock()
	if d == nil || d.res == nil || d.res.File == nil {
		return nil, nil, d
	}
	off := byteOffset(d.text, line, col)
	tf := d.res.Fset.File(d.res.File.Pos())
	if tf == nil {
		return nil, nil, d
	}
	pos := tf.Pos(off)
	var best *ast.Ident
	bestSpan := 1 << 30
	ast.Inspect(d.res.File, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok || strings.HasPrefix(id.Name, "__bgo") {
			return true
		}
		if !id.Pos().IsValid() || !id.End().IsValid() {
			return true
		}
		if id.Pos() <= pos && pos < id.End() {
			span := int(id.End() - id.Pos())
			if span < bestSpan {
				best = id
				bestSpan = span
			}
		}
		return true
	})
	return best, d.info, d
}

func textPos(params json.RawMessage) (string, int, int) {
	var p struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
		Position struct {
			Line      int `json:"line"`
			Character int `json:"character"`
		} `json:"position"`
	}
	_ = json.Unmarshal(params, &p)
	return p.TextDocument.URI, p.Position.Line, p.Position.Character
}

func diagnostics(d *doc) []any {
	if d == nil {
		return []any{}
	}
	var items []any
	if d.res != nil {
		for _, e := range d.res.Diags {
			items = append(items, diagItem(d.text, e.Pos.Line, e.Pos.Column, e.Msg, 1))
		}
	}
	if len(items) == 0 {
		return []any{}
	}
	return items
}

func parseDiags(err error, text string) []any {
	msg := err.Error()
	// go/scanner ErrorList: "file:line:col: msg"
	var items []any
	for _, line := range strings.Split(msg, "\n") {
		parts := strings.SplitN(line, ": ", 2)
		if len(parts) != 2 {
			continue
		}
		head := strings.Split(parts[0], ":")
		if len(head) < 3 {
			continue
		}
		ln, _ := strconv.Atoi(head[len(head)-2])
		col, _ := strconv.Atoi(head[len(head)-1])
		items = append(items, diagItem(text, ln, col, parts[1], 1))
	}
	if len(items) == 0 {
		items = append(items, diagItem(text, 1, 1, msg, 1))
	}
	return items
}

func diagItem(text string, line, col int, msg string, sev int) map[string]any {
	if line < 1 {
		line = 1
	}
	if col < 1 {
		col = 1
	}
	start := lspPos(text, line, col)
	end := start
	end["character"] = start["character"].(int) + 1
	return map[string]any{
		"range":    map[string]any{"start": start, "end": end},
		"severity": sev,
		"source":   "bettergo",
		"message":  msg,
	}
}

func lspPos(text string, line, col int) map[string]any {
	// line and col are 1-based byte columns from token.Position.
	lines := strings.Split(text, "\n")
	if line-1 >= len(lines) {
		return map[string]any{"line": 0, "character": 0}
	}
	srcLine := lines[line-1]
	byteCol := col - 1
	if byteCol < 0 {
		byteCol = 0
	}
	if byteCol > len(srcLine) {
		byteCol = len(srcLine)
	}
	return map[string]any{"line": line - 1, "character": utf16Len(srcLine[:byteCol])}
}

func identRange(text string, id *ast.Ident, res *compile.Result) map[string]any {
	if res == nil {
		return rangeAll(text)
	}
	p := res.Fset.Position(id.Pos())
	start := lspPos(text, p.Line, p.Column)
	end := lspPos(text, p.Line, p.Column+len(id.Name))
	return map[string]any{"start": start, "end": end}
}

func rangeAll(text string) map[string]any {
	n := strings.Count(text, "\n")
	return map[string]any{
		"start": map[string]any{"line": 0, "character": 0},
		"end":   map[string]any{"line": n, "character": 0},
	}
}

func byteOffset(text string, line, char int) int {
	lines := strings.Split(text, "\n")
	if line < 0 {
		line = 0
	}
	off := 0
	for i := 0; i < line && i < len(lines); i++ {
		off += len(lines[i]) + 1
	}
	if line >= len(lines) {
		return len(text)
	}
	src := lines[line]
	// char is UTF-16.
	u := utf16.Encode([]rune(src))
	if char > len(u) {
		char = len(u)
	}
	return off + len(string(utf16.Decode(u[:char])))
}

func utf16Len(s string) int {
	return len(utf16.Encode([]rune(s)))
}

func uriPath(uri string) string {
	u, err := url.Parse(uri)
	if err != nil || u.Path == "" {
		return strings.TrimPrefix(uri, "file://")
	}
	return u.Path
}

func keywordCompletion() map[string]any {
	words := []string{"if", "else", "func", "return", "var", "const", "type", "package", "import", "for", "switch", "select", "go", "defer", "struct", "interface", "map", "chan"}
	var items []any
	for _, w := range words {
		items = append(items, map[string]any{"label": w, "kind": 14})
	}
	return map[string]any{"isIncomplete": false, "items": items}
}

func rpcResult(id json.RawMessage, result any) any {
	if len(id) == 0 {
		id = []byte("null")
	}
	return map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(id), "result": result}
}

func readMessage(r *bufio.Reader) ([]byte, error) {
	var length int
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if strings.HasPrefix(strings.ToLower(line), "content-length:") {
			fmt.Sscanf(strings.TrimSpace(line[len("content-length:"):]), "%d", &length)
		}
	}
	if length < 0 {
		return nil, fmt.Errorf("bad content length")
	}
	buf := make([]byte, length)
	_, err := io.ReadFull(r, buf)
	return buf, err
}

func writeMessage(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Content-Length: %d\r\n\r\n", len(b)); err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}
