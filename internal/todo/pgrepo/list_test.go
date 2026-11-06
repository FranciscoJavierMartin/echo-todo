package pgrepo_test

import (
	"context"
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"echo-todo-2/internal/platform/postgres"
	"echo-todo-2/internal/platform/postgres/pgtest"
	"echo-todo-2/internal/todo"
	"echo-todo-2/internal/todo/pgrepo"
)

func seed(t *testing.T, repo *pgrepo.Repo, owner uuid.UUID, n int, rng *rand.Rand) map[uuid.UUID]todo.Todo {
	t.Helper()
	base := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	out := make(map[uuid.UUID]todo.Todo, n)
	words := []string{"alpha", "Bravo", "charlie", "delta", "écho", "foxtrot"}
	for i := range n {
		td := &todo.Todo{
			OwnerID:  owner,
			Title:    fmt.Sprintf("%s %03d", words[rng.IntN(len(words))], rng.IntN(50)),
			Notes:    "seeded",
			Status:   todo.StatusOpen,
			Priority: 1 + rng.IntN(5),
		}
		if rng.IntN(4) != 0 { // ~25% without due date
			d := base.Add(time.Duration(rng.IntN(20)) * 24 * time.Hour) // many ties on purpose
			td.DueAt = &d
		}
		if i%7 == 0 {
			td.Status = todo.StatusDone
		}
		require.NoError(t, repo.Create(context.Background(), td))
		out[td.ID] = *td
	}
	return out
}

// Pages through every row with several sorts while inserting new rows between pages.
// Every original row must appear exactly once, in todo.Compare order.
func TestList_KeysetIsStableAcrossInserts(t *testing.T) {
	pool := pgtest.New(t)
	repo := pgrepo.New(pool)
	svc := todo.NewService(repo, postgres.NewTxManager(pool), repo)
	owner := newUser(t, pool)
	rng := rand.New(rand.NewPCG(1, 2))
	originals := seed(t, repo, owner, 500, rng)

	for _, sortSpec := range []string{"", "-priority,due_at", "due_at", "-due_at,title", "title", "priority,-created_at"} {
		t.Run("sort="+sortSpec, func(t *testing.T) {
			keys, err := todo.ParseSort(sortSpec)
			require.NoError(t, err)
			seen := map[uuid.UUID]int{}
			var ordered []todo.Todo
			cursor := ""
			for pageNo := 0; ; pageNo++ {
				require.Less(t, pageNo, 100, "pagination does not terminate")
				page, err := svc.List(context.Background(), todo.ListQuery{
					Filter: todo.Filter{OwnerID: owner, Limit: 37}, SortSpec: sortSpec, Cursor: cursor,
				})
				require.NoError(t, err)
				for _, td := range page.Items {
					seen[td.ID]++
					ordered = append(ordered, td)
				}
				// Concurrent writers: new rows land somewhere in the order.
				extra := &todo.Todo{OwnerID: owner, Title: fmt.Sprintf("inserted %d", pageNo), Status: todo.StatusOpen, Priority: 1 + rng.IntN(5)}
				require.NoError(t, repo.Create(context.Background(), extra))
				if page.NextCursor == "" {
					break
				}
				cursor = page.NextCursor
			}
			for id := range originals {
				require.Equal(t, 1, seen[id], "row %s seen %d times", id, seen[id])
			}
			for id, n := range seen {
				require.Equal(t, 1, n, "duplicate %s", id)
			}
			require.True(t, slices.IsSortedFunc(ordered, func(a, b todo.Todo) int { return todo.Compare(keys, &a, &b) }),
				"SQL order must match todo.Compare")
		})
	}
}

func TestList_FiltersSearchAndCount(t *testing.T) {
	pool := pgtest.New(t)
	repo := pgrepo.New(pool)
	ctx := context.Background()
	owner := newUser(t, pool)
	day := func(d int) *time.Time { v := time.Date(2030, 1, d, 0, 0, 0, 0, time.UTC); return &v }
	for _, td := range []*todo.Todo{
		{Title: "Renew passport", Notes: "bring photos", Priority: 1, DueAt: day(5)},
		{Title: "Buy milk", Notes: "and running shoes", Priority: 2, DueAt: day(10)},
		{Title: "Plan running route", Priority: 3},
		{Title: "File taxes", Priority: 1, DueAt: day(20)},
	} {
		td.OwnerID, td.Status = owner, todo.StatusOpen
		require.NoError(t, repo.Create(ctx, td))
	}
	list := func(f todo.Filter) ([]string, int) {
		f.OwnerID, f.Limit, f.WithCount = owner, 50, true
		items, total, err := repo.List(ctx, f)
		require.NoError(t, err)
		var titles []string
		for _, i := range items {
			titles = append(titles, i.Title)
		}
		slices.Sort(titles)
		return titles, total
	}

	titles, total := list(todo.Filter{Priorities: []int{1}})
	require.Equal(t, []string{"File taxes", "Renew passport"}, titles)
	require.Equal(t, 2, total)

	titles, _ = list(todo.Filter{DueBefore: day(10)})
	require.Equal(t, []string{"Renew passport"}, titles)
	titles, _ = list(todo.Filter{DueAfter: day(10)})
	require.Equal(t, []string{"Buy milk", "File taxes"}, titles)

	// Stemming: "runs" matches "running" in title (A) and notes (B).
	titles, _ = list(todo.Filter{Query: "runs"})
	require.Equal(t, []string{"Buy milk", "Plan running route"}, titles)
	titles, _ = list(todo.Filter{Query: `running -milk`})
	require.Equal(t, []string{"Plan running route"}, titles)
	titles, _ = list(todo.Filter{Query: `"bring photos" or taxes`})
	require.Equal(t, []string{"File taxes", "Renew passport"}, titles)
	titles, total = list(todo.Filter{Query: "nothing-matches-this"})
	require.Empty(t, titles)
	require.Zero(t, total)
}
