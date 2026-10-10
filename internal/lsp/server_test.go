package lsp_test

import (
	"bytes"
	"strconv"
	"strings"
	"testing"

	"bettergo/internal/lsp"
)

func TestInitializeAndDiagnostic(t *testing.T) {
	var in bytes.Buffer
	writeMsg(&in, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"processId":null,"rootUri":null,"capabilities":{}}}`)
	writeMsg(&in, `{"jsonrpc":"2.0","method":"initialized","params":{}}`)
	writeMsg(&in, `{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"file:///tmp/p.bgo","languageId":"bettergo","version":1,"text":"package p\nfunc f() int {\n\treturn 40 if true else 90\n}\n"}}}`)
	writeMsg(&in, `{"jsonrpc":"2.0","method":"exit"}`)

	var out bytes.Buffer
	if err := lsp.Serve(&in, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, `"name":"bettergo"`) {
		t.Fatalf("missing server info: %s", got)
	}
	if !strings.Contains(got, "publishDiagnostics") {
		t.Fatalf("missing diagnostics: %s", got)
	}
}

func writeMsg(b *bytes.Buffer, msg string) {
	b.WriteString("Content-Length: ")
	b.WriteString(strconv.Itoa(len(msg)))
	b.WriteString("\r\n\r\n")
	b.WriteString(msg)
}
