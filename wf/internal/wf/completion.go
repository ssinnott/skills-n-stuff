package wf

// Is this task finished, as far as its pull requests are concerned?
//
// This is the one capability pi-tasks had that wf lost: close a task when
// everything it opened has landed. What it deliberately is *not* is a second
// way to close one.
//
// Closing lives in Apply, and it is gated on typed evidence a run reported —
// a PR, a commit, a document, a test — plus a close message substantial
// enough for the tracker to accept. A refresh command holds none of that: it
// has no outcomes, no transcript, and no run. It could only close a task by
// manufacturing the evidence the gate exists to demand, which would defeat
// the only thing that makes a closed task trustworthy, and it would put a
// second closing path beside the one that already enforces the rule.
//
// So this reports. "Completable" means every pull request bound to this task
// has merged and a human (or Apply, on a run that reports DONE) can now
// close it; wf owns the transition and takes it in one place. The report is
// the useful half anyway — the question a sweep answers is "which tasks are
// waiting on nothing," and that is a query.
//
// The arithmetic is deliberately unforgiving in two directions. A PR whose
// state nobody has verified never counts toward completion — an unchecked
// binding is not a merged one, and treating it as one is the evidence-free
// close in a different costume. And a task with no pull requests at all is
// never completable *by this rule*: plenty of work finishes without one, and
// saying nothing is right where inventing an answer is not.

// Completion is what a task's pull requests say about whether it is done.
type Completion struct {
	// Total is how many pull request bindings the task carries.
	Total int
	// Merged, Closed and Open are the verified states. Unknown counts the
	// bindings nobody has successfully refreshed — the reason a task can
	// have no open PRs and still not be completable.
	Merged  int
	Closed  int
	Open    int
	Unknown int
}

// Complete reports whether every pull request on the task merged.
func (c Completion) Complete() bool { return c.Total > 0 && c.Merged == c.Total }

// Blocker names, in one phrase, what stands between the task and completion.
// Empty when nothing does — either because it is complete or because pull
// requests are not what this task is waiting on.
func (c Completion) Blocker() string {
	switch {
	case c.Total == 0, c.Complete():
		return ""
	case c.Unknown > 0 && c.Open == 0 && c.Closed == 0:
		return "pull request state unknown"
	case c.Open > 0:
		return "pull requests still open"
	case c.Closed > 0:
		return "a pull request was closed without merging"
	default:
		return "pull request state unknown"
	}
}

// PRCompletion counts the states of a task's pull request bindings.
func (r Record) PRCompletion() Completion {
	var c Completion
	for _, b := range r.Bindings.ByKind(KindPR) {
		c.Total++
		switch b.State {
		case BindingMerged:
			c.Merged++
		case BindingClosed:
			c.Closed++
		case BindingLive:
			c.Open++
		default:
			// Superseded and disposed are not states a PR should reach,
			// and the zero value is "never checked". All of them are the
			// same thing here: wf does not know this one landed.
			c.Unknown++
		}
	}
	return c
}
