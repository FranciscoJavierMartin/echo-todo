package todo_test

import (
	"os/exec"
	"strings"
	"testing"
)

// The domain and HTTP packages must stay storage-agnostic: no pgx, no database/sql,
// not even transitively.
func TestNoStorageImports(t *testing.T) {
	if testing.Short() {
		t.Skip("requires go toolchain")
	}
	forbidden := []string{"github.com/jackc/pgx", "database/sql"}
	for _, pkg := range []string{"echo-todo-2/internal/todo", "echo-todo-2/internal/todo/handler"} {
		out, err := exec.Command("go", "list", "-deps", pkg).CombinedOutput()
		if err != nil {
			if strings.Contains(string(out), "is not in std") || strings.Contains(string(out), "no Go files") || strings.Contains(string(out), "cannot find") {
				continue
			}
			t.Fatalf("go list %s: %v\n%s", pkg, err, out)
		}
		for dep := range strings.SplitSeq(string(out), "\n") {
			for _, f := range forbidden {
				// database/sql/driver is fine: google/uuid implements driver.Valuer.
				if dep == f || strings.HasPrefix(dep, f+"/") && f != "database/sql" {
					t.Errorf("%s depends on %s", pkg, dep)
				}
			}
		}
	}
}
