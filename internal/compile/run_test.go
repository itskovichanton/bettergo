package compile_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"bettergo/internal/compile"
)

func TestPrograms(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "assign",
			src: `package main
func main() {
	a := 11
	sum := 0
	sum = 40 if a > 10 else 90
	println(sum)
}
`,
			want: "40\n",
		},
		{
			name: "short-circuit-then",
			src: `package main
func boom() int { panic("boom") }
func main() {
	sum := 40 if true else boom()
	println(sum)
}
`,
			want: "40\n",
		},
		{
			name: "short-circuit-else",
			src: `package main
func boom() int { panic("boom") }
func main() {
	sum := boom() if false else 90
	println(sum)
}
`,
			want: "90\n",
		},
		{
			name: "precedence",
			src: `package main
func main() {
	println(1+2 if false else 10)
	println(1+2 if true else 10)
}
`,
			want: "10\n3\n",
		},
		{
			name: "nested",
			src: `package main
func main() {
	println(1 if false else 2 if false else 3)
	label := "big" if false else "mid" if true else "small"
	println(label)
}
`,
			want: "3\nmid\n",
		},
		{
			name: "float-context",
			src: `package main
func main() {
	sum := 40
	var scaled float64 = 1 if sum > 10 else 2
	println(scaled)
}
`,
			want: "+1.000000e+000\n",
		},
		{
			name: "return",
			src: `package main
func pick(a int) int {
	return 40 if a > 10 else 90
}
func main() {
	println(pick(3))
	println(pick(20))
}
`,
			want: "90\n40\n",
		},
		{
			name: "if-statement",
			src: `package main
func main() {
	x := 1
	if x > 0 {
		println(2)
	} else {
		println(3)
	}
}
`,
			want: "2\n",
		},
		{
			name: "newline-ternary",
			src: `package main
func main() {
	sum := 40 if
		true else
		90
	println(sum)
}
`,
			want: "40\n",
		},
		{
			name: "newline-before-if-stmt",
			src: `package main
func main() {
	x := 1
	if x > 0 {
		println(7)
	}
}
`,
			want: "7\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module t\n\ngo 1.25\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "main.bgo")
			if err := os.WriteFile(path, []byte(tt.src), 0o644); err != nil {
				t.Fatal(err)
			}
			res, err := compile.Generate(path)
			if err != nil {
				t.Fatalf("generate: %v\n%s", err, string(resGo(res)))
			}
			cmd := exec.Command("go", "run", ".")
			cmd.Dir = dir
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("go run: %v\n%s\n--- generated ---\n%s", err, out, osRead(filepath.Join(dir, "main.go")))
			}
			if string(out) != tt.want {
				t.Fatalf("output %q, want %q\n--- generated ---\n%s", out, tt.want, osRead(filepath.Join(dir, "main.go")))
			}
		})
	}
}

func TestConstRejected(t *testing.T) {
	src := []byte("package p\nconst x = 1 if true else 2\n")
	res, err := compile.ParseLower("p.bgo", src)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Diags) == 0 || !strings.Contains(res.Diags[0].Error(), "not a constant") {
		t.Fatalf("got %+v", res.Diags)
	}
}

func TestFormatIdempotent(t *testing.T) {
	src := []byte("package p\nfunc f(a int) int {\nreturn 40 if a > 10 else 90\n}\n")
	once, err := compile.Format(src)
	if err != nil {
		t.Fatal(err)
	}
	twice, err := compile.Format(once)
	if err != nil {
		t.Fatal(err)
	}
	if string(once) != string(twice) {
		t.Fatalf("not idempotent\nfirst:\n%s\nsecond:\n%s", once, twice)
	}
	if !strings.Contains(string(once), "40 if a > 10 else 90") {
		t.Fatalf("lost ternary:\n%s", once)
	}
}

func resGo(res *compile.Result) string {
	if res == nil {
		return ""
	}
	return string(res.Go)
}

func osRead(path string) string {
	b, _ := os.ReadFile(path)
	return string(b)
}
