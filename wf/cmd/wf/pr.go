package main

// wf pr: what GitHub says about the pull requests a task opened.
//
// The logic lives in internal/gh and internal/wf; this file parses argv and
// renders, the same division cmdReview keeps with internal/review.
//
// `wf pr refresh` reports and never closes. Closing is Apply's, it is gated
// on typed evidence a run produced, and a refresh holds none of that — see
// the comment on wf.Completion for the whole argument. What this prints
// instead is the answer to "which tasks are waiting on nothing", which is
// the useful half and the honest one.

import (
	"context"
	"errors"
	"fmt"

	"github.com/ssinnott/skills-n-stuff/wf/internal/gh"
	"github.com/ssinnott/skills-n-stuff/wf/internal/store"
	"github.com/ssinnott/skills-n-stuff/wf/internal/wf"
)

// jsonPR is one refreshed pull request. Additive-only, like jsonReview: a
// client codes against these names.
type jsonPR struct {
	URL   string `json:"url"`
	State string `json:"state,omitempty"`
	Title string `json:"title,omitempty"`
	Was   string `json:"was,omitempty"`
	// Error is why this one could not be refreshed. Its presence is the
	// signal that State is the *old* state, not a fresh one.
	Error string `json:"error,omitempty"`
}

type jsonPRTask struct {
	Task   string   `json:"task"`
	Handle string   `json:"handle,omitempty"`
	PRs    []jsonPR `json:"prs"`
	// Completable is true only when every pull request merged. An
	// unrefreshable PR keeps it false.
	Completable bool   `json:"completable"`
	Blocker     string `json:"blocker,omitempty"`
}

func (a *app) cmdPR(ctx context.Context, args []string) (int, error) {
	positional := positionals(args)
	if len(positional) == 0 || positional[0] != "refresh" {
		return 1, errors.New("wf pr refresh [<ref>]")
	}
	ref := ""
	if len(positional) > 1 {
		ref = positional[1]
	}

	st := store.New(store.Root(a.cfg.Path))
	ids, err := prTargets(st, ref)
	if err != nil {
		return 1, err
	}

	client := gh.New(a.cfg.GhBin)
	out := make([]jsonPRTask, 0, len(ids))
	failed := false
	for _, id := range ids {
		rec, results, err := gh.RefreshRecord(ctx, client, st, id)
		if err != nil {
			return 1, err
		}
		if len(results) == 0 && ref == "" {
			// Sweeping the whole ledger, most records have no PR at all;
			// listing them would bury the ones that do.
			continue
		}
		row := prRow(rec, results)
		for _, r := range results {
			if r.Err != nil {
				failed = true
			}
		}
		out = append(out, row)
	}

	if hasFlag(args, "--json") {
		return 0, emit("tasks", out)
	}
	printPRTasks(out)
	// A lookup wf could not make is worth an exit code: a cron that greps
	// for "merged" should be able to tell "nothing merged" from "nobody
	// asked GitHub", which is the whole point of degrading to unknown.
	if failed {
		return 2, nil
	}
	return 0, nil
}

// prTargets resolves what to refresh: one task, or every record in the
// ledger. Resolution goes through the store rather than the queue, because a
// PR binding lives in the ledger and a refresh must work with no tracker
// running.
func prTargets(st store.Store, ref string) ([]string, error) {
	if ref != "" {
		rec, err := st.Resolve(ref)
		if err != nil {
			return nil, err
		}
		return []string{rec.ID}, nil
	}
	recs, err := st.List()
	if err != nil {
		var skip *store.SkipError
		if !errors.As(err, &skip) {
			return nil, err
		}
		// One unreadable file must not cost the rest — `wf gc` is what
		// names it.
	}
	ids := make([]string, 0, len(recs))
	for _, rec := range recs {
		if len(rec.Bindings.ByKind(wf.KindPR)) > 0 {
			ids = append(ids, rec.ID)
		}
	}
	return ids, nil
}

func prRow(rec wf.Record, results []gh.Result) jsonPRTask {
	row := jsonPRTask{Task: rec.ID, Handle: rec.Handle}
	byRef := map[string]gh.Result{}
	for _, r := range results {
		byRef[r.Ref] = r
	}
	for _, b := range rec.Bindings.ByKind(wf.KindPR) {
		pr := jsonPR{URL: b.Ref, State: b.StateLabel(), Title: b.Label}
		if r, ok := byRef[b.Ref]; ok {
			if r.Err != nil {
				pr.Error = r.Err.Error()
			} else if r.Changed() {
				pr.Was = string(r.From)
			}
		}
		row.PRs = append(row.PRs, pr)
	}
	completion := rec.PRCompletion()
	row.Completable = completion.Complete()
	row.Blocker = completion.Blocker()
	return row
}

func printPRTasks(rows []jsonPRTask) {
	if len(rows) == 0 {
		fmt.Println("no pull requests recorded")
		return
	}
	for _, row := range rows {
		name := row.Handle
		if name == "" {
			name = row.Task
		}
		fmt.Println(name)
		for _, pr := range row.PRs {
			state := pr.State
			if state == "" {
				state = "unknown"
			}
			if pr.Was != "" {
				state = fmt.Sprintf("%s (was %s)", state, pr.Was)
			}
			fmt.Printf("   %-8s %s\n", state, pr.URL)
			if pr.Error != "" {
				// The state above is the one wf already had; saying so
				// beside the reason is what keeps it from reading as a
				// fresh answer.
				fmt.Printf("            unrefreshed: %s\n", pr.Error)
			}
		}
		if row.Completable {
			fmt.Println("   every pull request merged — completable")
			continue
		}
		if row.Blocker != "" {
			fmt.Printf("   not completable: %s\n", row.Blocker)
		}
	}
}
