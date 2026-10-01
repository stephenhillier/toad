package buddy

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Compile fixtures in separate modules: compile-failure cases must fail because
// of their handler signatures, not because a dependency or toolchain is missing.
func TestBuilderCompilation(t *testing.T) {
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	sum, err := os.ReadFile("go.sum")
	if err != nil {
		t.Fatal(err)
	}
	fixtures, err := filepath.Glob("testdata/compile/*.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(fixtures) == 0 {
		t.Fatal("Missing compile fixtures")
	}
	for _, fixture := range fixtures {
		t.Run(strings.TrimSuffix(filepath.Base(fixture), ".go"), func(t *testing.T) {
			dir := t.TempDir()
			source, err := os.ReadFile(fixture)
			if err != nil {
				t.Fatal(err)
			}
			module := fmt.Sprintf("module buddy_compile_fixture\n\ngo 1.27\n\nrequire github.com/stephenhillier/buddy v0.0.0\nreplace github.com/stephenhillier/buddy => %q\n", filepath.ToSlash(root))
			for name, data := range map[string][]byte{"go.mod": []byte(module), "go.sum": sum, "fixture.go": source} {
				if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command("go", "build", "-mod=mod", ".")
			cmd.Dir = dir
			// Dependencies were already resolved when building this test. Keep fixtures
			// offline, isolated from a parent workspace and inherited build flags.
			cmd.Env = append(os.Environ(), "GOWORK=off", "GOTOOLCHAIN=local", "GOPROXY=off", "GOFLAGS=")
			output, err := cmd.CombinedOutput()
			invalid := strings.HasPrefix(filepath.Base(fixture), "invalid_")
			if !invalid && err != nil {
				t.Fatalf("Valid handler contracts failed to compile: %v\n%s", err, output)
			}
			if invalid {
				if err == nil {
					t.Fatal("Mismatched handler compiled successfully")
				}
				if !strings.Contains(string(output), "./fixture.go:") || !(strings.Contains(string(output), "cannot use") || strings.Contains(string(output), "does not match") || strings.Contains(string(output), "cannot infer")) {
					t.Fatalf("Expected a handler type error, got %v\n%s", err, output)
				}
			}
		})
	}
}
