// Package unitonly holds the lint that keeps this module's tests unit
// tests: in process, with no run, supervisor, fake agent or second CLI.
package unitonly

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// allowed are the commands a test may start, with the first argument
// each must take ("" for any): git builds and reads fixtures, go lists a
// package's imports.
var allowed = map[string]string{"git": "", "go": "list"}

// TestUnitOnly fails on a test file that starts any other process or
// sleeps a second or more. End-to-end tests cost minutes in every
// session's checks and leak what they start; see AGENTS.md.
func TestUnitOnly(t *testing.T) {
	fset := token.NewFileSet()
	err := filepath.WalkDir("..", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == "testdata" {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		names := imports(f)
		ast.Inspect(f, func(n ast.Node) bool {
			if why := starts(n, names); why != "" {
				t.Errorf("%s: %s", fset.Position(n.Pos()), why)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// imports maps each name a file imports a package under to its path.
func imports(f *ast.File) map[string]string {
	m := map[string]string{}
	for _, is := range f.Imports {
		path, _ := strconv.Unquote(is.Path.Value)
		name := path[strings.LastIndex(path, "/")+1:]
		if is.Name != nil {
			name = is.Name.Name
		}
		m[name] = path
	}
	return m
}

func starts(n ast.Node, names map[string]string) string {
	switch n := n.(type) {
	case *ast.CompositeLit:
		if sel(n.Type, names) == "os/exec.Cmd" {
			return "builds an exec.Cmd: a unit test starts no process"
		}
	case *ast.CallExpr:
		if id, ok := n.Fun.(*ast.Ident); ok && id.Name == "new" && len(n.Args) == 1 && sel(n.Args[0], names) == "os/exec.Cmd" {
			return "builds an exec.Cmd: a unit test starts no process"
		}
		switch name := sel(n.Fun, names); name {
		case "os/exec.Command", "os/exec.CommandContext":
			arg := 0
			if name == "os/exec.CommandContext" {
				arg = 1
			}
			if len(n.Args) > arg {
				if want, ok := allowed[literal(n.Args[arg])]; ok && (want == "" || len(n.Args) > arg+1 && literal(n.Args[arg+1]) == want) {
					return ""
				}
			}
			return name + " starts a process other than git or go list: a unit test runs in process"
		case "os.StartProcess", "syscall.ForkExec", "syscall.Exec", "golang.org/x/sys/unix.Exec":
			return name + " starts a process: a unit test runs in process"
		case "time.Sleep":
			if d, ok := constDuration(n.Args[0], names); ok && d >= time.Second {
				return "time.Sleep of " + d.String() + ": a unit test waits on no clock a second or more"
			}
		}
	}
	return ""
}

func literal(e ast.Expr) string {
	if lit, ok := e.(*ast.BasicLit); ok && lit.Kind == token.STRING {
		s, _ := strconv.Unquote(lit.Value)
		return s
	}
	return ""
}

// sel names a selector by its package's import path: "os/exec.Command".
func sel(e ast.Expr, names map[string]string) string {
	if s, ok := e.(*ast.SelectorExpr); ok {
		if x, ok := s.X.(*ast.Ident); ok {
			if path, ok := names[x.Name]; ok {
				return path + "." + s.Sel.Name
			}
		}
	}
	return ""
}

// constDuration reads n * time.Unit or time.Unit, the forms tests write.
func constDuration(e ast.Expr, names map[string]string) (time.Duration, bool) {
	units := map[string]time.Duration{"time.Nanosecond": time.Nanosecond, "time.Microsecond": time.Microsecond,
		"time.Millisecond": time.Millisecond, "time.Second": time.Second, "time.Minute": time.Minute, "time.Hour": time.Hour}
	switch e := e.(type) {
	case *ast.SelectorExpr:
		d, ok := units[sel(e, names)]
		return d, ok
	case *ast.ParenExpr:
		return constDuration(e.X, names)
	case *ast.BinaryExpr:
		if e.Op != token.MUL {
			return 0, false
		}
		l, lok := constInt(e.X)
		r, rok := constDuration(e.Y, names)
		if !lok || !rok {
			l2, lok2 := constDuration(e.X, names)
			r2, rok2 := constInt(e.Y)
			if !lok2 || !rok2 {
				return 0, false
			}
			return l2 * time.Duration(r2), true
		}
		return time.Duration(l) * r, true
	}
	return 0, false
}

func constInt(e ast.Expr) (int64, bool) {
	if lit, ok := e.(*ast.BasicLit); ok && lit.Kind == token.INT {
		v, err := strconv.ParseInt(lit.Value, 0, 64)
		return v, err == nil
	}
	return 0, false
}
