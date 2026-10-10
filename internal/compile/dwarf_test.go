package compile_test

import (
	"debug/dwarf"
	"debug/elf"
	"debug/macho"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"bettergo/internal/compile"
)

func TestDWARFPointsAtBGO(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module t\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := []byte(`package main
func main() {
	sum := 40 if true else 90
	println(sum)
}
`)
	path := filepath.Join(dir, "main.bgo")
	if err := os.WriteFile(path, src, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := compile.Generate(path); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "prog")
	cmd := exec.Command("go", "build", "-o", bin, "-gcflags=all=-N -l", ".")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	data, err := openDWARF(bin)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	var names []string
	r := data.Reader()
	for {
		ent, err := r.Next()
		if err != nil || ent == nil {
			break
		}
		if ent.Tag != dwarf.TagCompileUnit {
			continue
		}
		lr, err := data.LineReader(ent)
		if err != nil || lr == nil {
			continue
		}
		var line dwarf.LineEntry
		for {
			if err := lr.Next(&line); err != nil {
				break
			}
			if line.File == nil {
				continue
			}
			names = append(names, line.File.Name)
			if strings.HasSuffix(line.File.Name, ".bgo") {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("DWARF line table has no .bgo file, saw %v", unique(names))
	}

	if dlv, err := exec.LookPath("dlv"); err == nil {
		script := filepath.Join(dir, "dlv.txt")
		abs, _ := filepath.Abs(path)
		body := "break " + abs + ":3\ncontinue\nstack\nquit\n"
		if err := os.WriteFile(script, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(dlv, "exec", bin, "--allow-non-terminal-interactive=true", "--init", script)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		text := string(out)
		if err != nil && !strings.Contains(text, ".bgo") {
			t.Logf("dlv exited %v\n%s", err, text)
		}
		if !strings.Contains(text, "main.bgo") {
			t.Fatalf("delve stack did not mention main.bgo:\n%s", text)
		}
	}
}

func openDWARF(path string) (*dwarf.Data, error) {
	if f, err := macho.Open(path); err == nil {
		return f.DWARF()
	}
	f, err := elf.Open(path)
	if err != nil {
		return nil, err
	}
	return f.DWARF()
}

func unique(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
