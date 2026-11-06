// Command covercheck enforces per-tree statement coverage on a merged profile:
//
//	go test -coverpkg=./internal/... -coverprofile=coverage.out ./...
//	go run ./tools/covercheck -profile coverage.out internal/todo=80 internal/auth=90
//
// A tree includes its subpackages; test-helper packages (*test) and generated
// sqlc code are excluded. Blocks seen by several test binaries are merged.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
)

const module = "echo-todo-2/"

type block struct {
	stmts   int
	covered bool
}

func main() {
	profile := flag.String("profile", "coverage.out", "merged cover profile")
	flag.Parse()

	blocks, err := read(*profile)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	failed := false
	for _, arg := range flag.Args() {
		tree, minStr, ok := strings.Cut(arg, "=")
		if !ok {
			fmt.Fprintf(os.Stderr, "bad threshold %q (want path=percent)\n", arg)
			os.Exit(2)
		}
		minPct, err := strconv.ParseFloat(minStr, 64)
		if err != nil {
			fmt.Fprintf(os.Stderr, "bad percent in %q\n", arg)
			os.Exit(2)
		}
		total, covered := 0, 0
		for key, b := range blocks {
			file := key[:strings.Index(key, ":")]
			if !strings.HasPrefix(file, module+tree+"/") || excluded(file) {
				continue
			}
			total += b.stmts
			if b.covered {
				covered += b.stmts
			}
		}
		pct := 0.0
		if total > 0 {
			pct = 100 * float64(covered) / float64(total)
		}
		status := "ok"
		if pct < minPct {
			status, failed = "FAIL", true
		}
		fmt.Printf("%-4s %-20s %6.1f%% (min %.0f%%, %d/%d statements)\n", status, tree, pct, minPct, covered, total)
	}
	if failed {
		os.Exit(1)
	}
}

func excluded(file string) bool {
	dir := file[:strings.LastIndex(file, "/")]
	return strings.HasSuffix(dir, "test") || strings.HasSuffix(dir, "/db")
}

func read(path string) (map[string]block, error) {
	f, err := os.Open(path) // #nosec G304 -- developer-supplied path
	if err != nil {
		return nil, err
	}
	defer f.Close()
	blocks := map[string]block{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "mode:") || line == "" {
			continue
		}
		// file:start,end numStmts count
		fields := strings.Fields(line)
		if len(fields) != 3 {
			continue
		}
		stmts, _ := strconv.Atoi(fields[1])
		count, _ := strconv.Atoi(fields[2])
		b := blocks[fields[0]]
		b.stmts = stmts
		b.covered = b.covered || count > 0
		blocks[fields[0]] = b
	}
	return blocks, sc.Err()
}
