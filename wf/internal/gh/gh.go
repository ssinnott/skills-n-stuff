// Package gh answers one question — did this pull request merge? — by
// shelling out to the `gh` CLI.
//
// Completing a task when every PR on it has landed needs someone to ask
// GitHub whether they did. `gh` wins that job on the things that are
// expensive to get right and easy to get wrong: authentication (SSO, tokens,
// enterprise hosts), rate-limit backoff, and the URL-to-API mapping for a
// link a human pasted. Reimplementing those means owning an auth story wf has
// no business owning, and the standard-library-only rule would make even the
// HTTP client hand-rolled. The cost is the same one difit, kata and pi
// already impose: a binary that must be on PATH and authenticated, whose
// absence is a runtime failure rather than a build one — hence GH_BIN,
// exactly as KATA_BIN, PI_BIN and DIFIT_BIN work.
//
// The rule that shapes every function here is that **a failed lookup degrades
// to "state unknown", never to a wrong answer**. A missing binary, an
// unauthenticated one, a network failure, a 404, a URL that will not parse:
// none of them may return a state. A binding whose state cannot be refreshed
// keeps the state it had, because closing a task on a guess is exactly the
// evidence-free close the whole design refuses.
//
// Every assumption about gh's command line and its JSON output lives in this
// one file, behind the Spawner seam, for the same reason internal/review
// isolates difit: none of it is verified against a published reference here,
// so one live run against a real `gh` should be able to correct it by editing
// argv and status in one place rather than hunting through callers.
//
// Assumed, and unverified:
//
//   - `gh pr view <number> --repo <host>/<owner>/<repo> --json number,state,title,url`
//     prints one JSON object on stdout and exits 0.
//   - The repository is named explicitly rather than inferred from the
//     working directory, so the answer does not depend on where wf ran.
//     gh documents --repo as taking [HOST/]OWNER/REPO, which is also what
//     makes an enterprise host work without GH_HOST.
//   - `state` is one of "OPEN", "CLOSED", "MERGED" — a merged PR reports
//     MERGED, not CLOSED, which is the whole distinction wf needs.
//   - Failure prints a diagnostic on stderr and exits non-zero.
package gh

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/ssinnott/skills-n-stuff/wf/internal/wf"
)

// Spawner runs one `gh` invocation and returns what it produced. A real
// Spawner shells out; tests fake it the way internal/review fakes difit, so
// gh need not be installed — or authenticated — to test this seam.
type Spawner interface {
	Spawn(ctx context.Context, args []string) (stdout, stderr string, err error)
}

// ExecSpawner runs the configured gh binary as a subprocess.
type ExecSpawner struct {
	// Bin overrides the binary. Empty means "gh" on PATH; GH_BIN is what
	// fills it in for the launchd or systemd case where PATH is not what a
	// shell would give you.
	Bin string
}

// Spawn runs gh and captures its output. Unlike difit's spawner the error is
// returned rather than swallowed: gh's exit code is a real signal, and it is
// the only diagnostic there is when the binary is missing entirely.
func (s ExecSpawner) Spawn(ctx context.Context, args []string) (string, string, error) {
	bin := s.Bin
	if bin == "" {
		bin = "gh"
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	// Deliberately no cmd.Dir: every lookup names its repository with
	// --repo, so an answer never depends on which checkout wf was invoked
	// from — including from a worktree that has since been disposed.
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

// PRRef is a pull request identified well enough to ask gh about it.
type PRRef struct {
	Host   string
	Owner  string
	Repo   string
	Number int
}

// RepoArg is the value gh's --repo flag takes. The host is always included:
// gh documents [HOST/]OWNER/REPO, and carrying the host through is what makes
// an enterprise URL work without anyone setting GH_HOST.
func (r PRRef) RepoArg() string { return r.Host + "/" + r.Owner + "/" + r.Repo }

// String renders the ref the way a human writes it.
func (r PRRef) String() string {
	return fmt.Sprintf("%s/%s#%d", r.Owner, r.Repo, r.Number)
}

// prURLRe recognizes a pull request URL on any host, not just github.com:
// gh talks to enterprise hosts too, and the host is a value it takes rather
// than a fork in the code. The shape itself — /owner/repo/pull/N — is
// GitHub's, which is honest, because gh is a GitHub client.
var prURLRe = regexp.MustCompile(`^https?://([\w.-]+)/([\w.-]+)/([\w.-]+)/pull/(\d+)/?$`)

// ParsePRURL turns a recorded PR url into something gh can be asked about.
//
// A URL that does not parse is an error and never a lookup: guessing at the
// repository would ask GitHub about the wrong pull request, and a wrong
// answer here is the one failure mode this package exists to prevent. The
// caller's response is to leave the binding's state alone.
func ParsePRURL(raw string) (PRRef, error) {
	trimmed := strings.TrimSpace(raw)
	m := prURLRe.FindStringSubmatch(trimmed)
	if m == nil {
		return PRRef{}, fmt.Errorf("unrecognized pull request url %q", raw)
	}
	n, err := strconv.Atoi(m[4])
	if err != nil || n <= 0 {
		return PRRef{}, fmt.Errorf("unrecognized pull request number in %q", raw)
	}
	return PRRef{Host: m[1], Owner: m[2], Repo: m[3], Number: n}, nil
}

// viewFields is what `gh pr view --json` is asked for. Kept to the four
// fields wf actually reads: every name here is an assumption about gh's
// output schema, and an unread field is an assumption taken on for nothing.
const viewFields = "number,state,title,url"

// viewArgs builds the argv for one lookup. Split out from Status so a test
// can assert the command line without a process, which is the only way this
// stays checkable while gh is not installed.
func viewArgs(ref PRRef) []string {
	return []string{
		"pr", "view", strconv.Itoa(ref.Number),
		"--repo", ref.RepoArg(),
		"--json", viewFields,
	}
}

// view is the subset of gh's JSON output wf decodes.
type view struct {
	Number int    `json:"number"`
	State  string `json:"state"`
	Title  string `json:"title"`
	URL    string `json:"url"`
}

// Status is what a successful lookup establishes about one pull request.
type Status struct {
	Ref PRRef
	// State is always one of merged, closed or live. A response wf cannot
	// map to one of those is an error, not a Status carrying
	// BindingUnknown: "we asked and learned nothing" and "we never asked"
	// are the same outcome for the caller, and only one of them should be
	// able to reach a binding.
	State wf.BindingState
	Title string
	URL   string
}

// Client asks gh about pull requests.
type Client struct{ Spawner Spawner }

// New builds a client over the real binary. bin is the GH_BIN override, or
// empty for gh on PATH.
func New(bin string) Client { return Client{Spawner: ExecSpawner{Bin: bin}} }

// Status looks up one pull request by url.
//
// Every failure path returns an error and no state. That is the whole
// contract: a caller applies State only when err is nil, so a missing gh, an
// unauthenticated one, a network failure, a deleted PR and a garbled response
// all land in the same place — the binding keeps whatever it already had.
func (c Client) Status(ctx context.Context, url string) (Status, error) {
	if c.Spawner == nil {
		return Status{}, fmt.Errorf("gh: no spawner configured")
	}
	ref, err := ParsePRURL(url)
	if err != nil {
		return Status{}, err
	}

	stdout, stderr, runErr := c.Spawner.Spawn(ctx, viewArgs(ref))
	status, parseErr := ParseStatus(ref, stdout)
	if parseErr == nil {
		// A parseable answer is the success signal even if gh exited
		// non-zero, for difit's reason: the output is the thing wf can
		// actually verify. In practice gh exits 0 here.
		return status, nil
	}
	// gh's own diagnostic beats wf's guess at what went wrong, and the exit
	// error is all there is when the binary itself is missing.
	if detail := diagnostic(stdout, stderr, runErr); detail != "" {
		return Status{}, fmt.Errorf("gh pr view %s: %s", ref, detail)
	}
	return Status{}, fmt.Errorf("gh pr view %s: %w", ref, parseErr)
}

// ParseStatus decodes gh's JSON object. Exported so the one shape assumption
// this package makes about gh's output is testable on its own, without a
// process and without the binary.
//
// Scanning for a JSON object rather than decoding stdout whole is deliberate:
// a wrapper — a shell function, a version-update notice — can print its own
// text around the line that matters, and a leading banner should not read as
// a failed lookup.
func ParseStatus(ref PRRef, stdout string) (Status, error) {
	raw := strings.TrimSpace(stdout)
	if raw == "" {
		return Status{}, fmt.Errorf("gh produced no output")
	}
	if i := strings.IndexByte(raw, '{'); i > 0 {
		raw = raw[i:]
	}

	var v view
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return Status{}, fmt.Errorf("gh output is not the expected JSON object: %w", err)
	}
	state, ok := bindingState(v.State)
	if !ok {
		// An unrecognized state is a schema change or a wrapper's noise,
		// and either way wf knows nothing new. Reporting it as an error
		// rather than silently keeping the old state is what makes a
		// schema drift visible instead of quiet.
		return Status{}, fmt.Errorf("gh reported unknown pull request state %q", v.State)
	}
	return Status{Ref: ref, State: state, Title: v.Title, URL: v.URL}, nil
}

// bindingState maps gh's vocabulary onto the ledger's. MERGED and CLOSED are
// different words for a reason wf cares about — one landed, one did not — and
// collapsing them would let a closed-unmerged PR complete a task.
func bindingState(raw string) (wf.BindingState, bool) {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "MERGED":
		return wf.BindingMerged, true
	case "CLOSED":
		return wf.BindingClosed, true
	case "OPEN":
		return wf.BindingLive, true
	}
	return wf.BindingUnknown, false
}

// diagnostic picks the most useful text out of a failed run: gh's stderr
// first, then anything it managed to print, then the process error, which is
// the only signal when the binary does not exist at all.
func diagnostic(stdout, stderr string, runErr error) string {
	if s := strings.TrimSpace(stderr); s != "" {
		return firstLine(s)
	}
	if s := strings.TrimSpace(stdout); s != "" {
		return firstLine(s)
	}
	if runErr != nil {
		return runErr.Error()
	}
	return ""
}

// firstLine keeps a multi-line diagnostic from turning one report row into a
// paragraph. gh's own errors lead with the useful sentence.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}
