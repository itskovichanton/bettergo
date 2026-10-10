package main

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"bettergo/internal/compile"
	"bettergo/internal/lsp"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "gen":
		os.Exit(cmdGen(os.Args[2:]))
	case "build":
		os.Exit(cmdGo("build", os.Args[2:]))
	case "run":
		os.Exit(cmdGo("run", os.Args[2:]))
	case "fmt":
		os.Exit(cmdFmt(os.Args[2:]))
	case "vet":
		os.Exit(cmdVet(os.Args[2:]))
	case "lsp":
		if err := lsp.Serve(os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `bettergo — tools for the BetterGo language

usage:
  bettergo gen [--check] [path...]
  bettergo build [go build args...]
  bettergo run [go run args...]
  bettergo fmt [path...]
  bettergo vet [path...]
  bettergo lsp
`)
}

func cmdGen(args []string) int {
	check := false
	var paths []string
	for _, a := range args {
		if a == "--check" {
			check = true
			continue
		}
		paths = append(paths, a)
	}
	if len(paths) == 0 {
		paths = []string{"."}
	}
	files, err := collect(paths)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	status := 0
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			status = 1
			continue
		}
		res, err := compile.ParseLower(f, src)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			status = 1
			continue
		}
		for _, d := range res.Diags {
			fmt.Fprintln(os.Stderr, d.Error())
			status = 1
		}
		out := strings.TrimSuffix(f, filepath.Ext(f)) + ".go"
		if check {
			have, err := os.ReadFile(out)
			if err != nil || string(have) != string(res.Go) {
				fmt.Fprintf(os.Stderr, "%s: generated Go is stale\n", f)
				status = 1
			}
			continue
		}
		if err := os.WriteFile(out, res.Go, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			status = 1
		}
	}
	return status
}

func cmdFmt(args []string) int {
	if len(args) == 0 {
		args = []string{"."}
	}
	files, err := collect(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	status := 0
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			status = 1
			continue
		}
		out, err := compile.Format(src)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", f, err)
			status = 1
			continue
		}
		if string(out) != string(src) {
			if err := os.WriteFile(f, out, 0o644); err != nil {
				fmt.Fprintln(os.Stderr, err)
				status = 1
			}
		}
	}
	return status
}

func cmdVet(args []string) int {
	if st := cmdGen(args); st != 0 {
		return st
	}
	goArgs := append([]string{"vet"}, args...)
	if len(args) == 0 {
		goArgs = []string{"vet", "./..."}
	}
	return run("go", goArgs...)
}

func cmdGo(sub string, args []string) int {
	if st := cmdGen(nil); st != 0 {
		return st
	}
	return run("go", append([]string{sub}, args...)...)
}

func run(name string, args ...string) int {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode()
		}
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func collect(paths []string) ([]string, error) {
	var out []string
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		if !fi.IsDir() {
			if filepath.Ext(p) == ".bgo" {
				out = append(out, p)
			}
			continue
		}
		err = filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				base := d.Name()
				if base == ".git" || base == "ide" || base == "vendor" {
					return filepath.SkipDir
				}
				return nil
			}
			if filepath.Ext(path) == ".bgo" {
				out = append(out, path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
