package main

import (
	"errors"
	"testing"

	"github.com/ssinnott/skills-n-stuff/wf/internal/gh"
	"github.com/ssinnott/skills-n-stuff/wf/internal/wf"
)

func TestPRRowReportsCompletableOnlyWhenEverythingMerged(t *testing.T) {
	const (
		a = "https://github.com/acme/w/pull/1"
		b = "https://github.com/acme/w/pull/2"
	)
	rec := wf.Record{ID: "t1", Handle: "neck", Bindings: wf.Bindings{
		{Kind: wf.KindPR, Ref: a, State: wf.BindingMerged},
		{Kind: wf.KindPR, Ref: b, State: wf.BindingMerged},
	}}
	row := prRow(rec, []gh.Result{
		{Ref: a, From: wf.BindingLive, To: wf.BindingMerged},
		{Ref: b, From: wf.BindingMerged, To: wf.BindingMerged},
	})

	if !row.Completable {
		t.Error("Completable = false with every pull request merged")
	}
	if row.Blocker != "" {
		t.Errorf("Blocker = %q, want none", row.Blocker)
	}
	if row.PRs[0].Was != string(wf.BindingLive) {
		t.Errorf("Was = %q, want the state it moved from", row.PRs[0].Was)
	}
	if row.PRs[1].Was != "" {
		t.Errorf("Was = %q for an unchanged pull request, want empty", row.PRs[1].Was)
	}
}

func TestPRRowShowsTheOldStateWhenALookupFailed(t *testing.T) {
	// The row a human reads has to say "this is what wf already had, and
	// here is why it could not check" — never a state that looks fresh.
	const url = "https://github.com/acme/w/pull/1"
	rec := wf.Record{ID: "t1", Bindings: wf.Bindings{
		{Kind: wf.KindPR, Ref: url, State: wf.BindingLive},
	}}
	row := prRow(rec, []gh.Result{{Ref: url, From: wf.BindingLive, To: wf.BindingLive,
		Err: errors.New("gh: Not Found (HTTP 404)")}})

	if row.Completable {
		t.Error("Completable = true on a task wf could not check")
	}
	if row.PRs[0].State != "open" {
		t.Errorf("State = %q, want the PR word for the state it had", row.PRs[0].State)
	}
	if row.PRs[0].Error == "" {
		t.Error("Error is empty; the row must say the state was not refreshed")
	}
}

func TestPRRowNamesUnknownAsABlocker(t *testing.T) {
	rec := wf.Record{ID: "t1", Bindings: wf.Bindings{
		{Kind: wf.KindPR, Ref: "u", State: wf.BindingMerged},
		{Kind: wf.KindPR, Ref: "v"},
	}}
	row := prRow(rec, nil)
	if row.Completable {
		t.Error("Completable = true with an unverified pull request")
	}
	if row.Blocker != "pull request state unknown" {
		t.Errorf("Blocker = %q", row.Blocker)
	}
}
