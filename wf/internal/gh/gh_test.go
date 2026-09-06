package gh

import (
	"context"
	"errors"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/ssinnott/skills-n-stuff/wf/internal/wf"
)

// fakeSpawner stands in for the gh binary, which is not installed here and
// would not be authenticated if it were — the same way internal/review fakes
// difit.
type fakeSpawner struct {
	stdout, stderr string
	err            error
	gotArgs        []string
	calls          int
	// byNumber overrides the canned answer per PR number, for the mixed
	// case where one lookup succeeds and another fails.
	byNumber map[string]fakeReply
}

type fakeReply struct {
	stdout, stderr string
	err            error
}

func (f *fakeSpawner) Spawn(_ context.Context, args []string) (string, string, error) {
	f.gotArgs = args
	f.calls++
	if f.byNumber != nil && len(args) > 2 {
		if reply, ok := f.byNumber[args[2]]; ok {
			return reply.stdout, reply.stderr, reply.err
		}
	}
	return f.stdout, f.stderr, f.err
}

func TestParsePRURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		want    PRRef
		wantErr bool
	}{
		{
			name: "github",
			url:  "https://github.com/acme/widgets/pull/412",
			want: PRRef{Host: "github.com", Owner: "acme", Repo: "widgets", Number: 412},
		},
		{
			name: "trailing slash",
			url:  "https://github.com/acme/widgets/pull/412/",
			want: PRRef{Host: "github.com", Owner: "acme", Repo: "widgets", Number: 412},
		},
		{
			name: "enterprise host carries through, since gh takes HOST/OWNER/REPO",
			url:  "https://github.corp.example/team/app/pull/7",
			want: PRRef{Host: "github.corp.example", Owner: "team", Repo: "app", Number: 7},
		},
		{name: "an issue is not a pull request", url: "https://github.com/acme/widgets/issues/412", wantErr: true},
		{name: "no number", url: "https://github.com/acme/widgets/pull/", wantErr: true},
		{name: "not a url at all", url: "widgets#412", wantErr: true},
		{name: "empty", url: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParsePRURL(tt.url)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParsePRURL(%q) = %+v, want an error", tt.url, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParsePRURL(%q) error = %v", tt.url, err)
			}
			if got != tt.want {
				t.Errorf("ParsePRURL(%q) = %+v, want %+v", tt.url, got, tt.want)
			}
		})
	}
}

func TestViewArgsNamesTheRepositoryExplicitly(t *testing.T) {
	// The whole assumption about gh's command line, in one assertion: a live
	// run that disagrees with this is the one place to change.
	ref := PRRef{Host: "github.com", Owner: "acme", Repo: "widgets", Number: 412}
	want := []string{"pr", "view", "412", "--repo", "github.com/acme/widgets", "--json", "number,state,title,url"}
	if got := viewArgs(ref); !reflect.DeepEqual(got, want) {
		t.Errorf("viewArgs() = %v, want %v", got, want)
	}
}

func TestStatusMapsGitHubStates(t *testing.T) {
	tests := []struct {
		state string
		want  wf.BindingState
	}{
		{"MERGED", wf.BindingMerged},
		{"CLOSED", wf.BindingClosed},
		{"OPEN", wf.BindingLive},
		// gh is documented as shouting; tolerate a case change rather than
		// reading it as an unknown state.
		{"merged", wf.BindingMerged},
	}
	for _, tt := range tests {
		t.Run(tt.state, func(t *testing.T) {
			s := &fakeSpawner{stdout: `{"number":412,"state":"` + tt.state + `","title":"Add the parser","url":"https://github.com/acme/widgets/pull/412"}`}
			got, err := Client{Spawner: s}.Status(context.Background(), "https://github.com/acme/widgets/pull/412")
			if err != nil {
				t.Fatalf("Status() error = %v", err)
			}
			if got.State != tt.want {
				t.Errorf("State = %q, want %q", got.State, tt.want)
			}
			if got.Title != "Add the parser" {
				t.Errorf("Title = %q, want the PR title", got.Title)
			}
		})
	}
}

func TestStatusIgnoresLeadingBannerText(t *testing.T) {
	// A wrapper or an update notice printing before the JSON must not read
	// as a failed lookup.
	s := &fakeSpawner{stdout: "A new release of gh is available\n" +
		`{"number":1,"state":"MERGED","title":"t","url":"u"}`}
	got, err := Client{Spawner: s}.Status(context.Background(), "https://github.com/acme/widgets/pull/1")
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if got.State != wf.BindingMerged {
		t.Errorf("State = %q, want merged", got.State)
	}
}

// Every failure mode below has to produce an error and no state. That is the
// load-bearing property: a caller applies State only when err is nil, so
// "wf could not tell" can never reach a binding as "merged" or "closed".
func TestStatusFailureModesYieldNoState(t *testing.T) {
	tests := []struct {
		name    string
		spawner *fakeSpawner
		url     string
	}{
		{
			name:    "missing binary",
			spawner: &fakeSpawner{err: exec.ErrNotFound},
			url:     "https://github.com/acme/widgets/pull/1",
		},
		{
			name:    "unauthenticated gh",
			spawner: &fakeSpawner{stderr: "gh: To use GitHub CLI in a GitHub Actions workflow, set the GH_TOKEN environment variable", err: errors.New("exit status 4")},
			url:     "https://github.com/acme/widgets/pull/1",
		},
		{
			name:    "network failure",
			spawner: &fakeSpawner{stderr: "dial tcp: lookup api.github.com: no such host", err: errors.New("exit status 1")},
			url:     "https://github.com/acme/widgets/pull/1",
		},
		{
			name:    "404 on a deleted pull request",
			spawner: &fakeSpawner{stderr: "GraphQL: Could not resolve to a PullRequest with the number of 9999.", err: errors.New("exit status 1")},
			url:     "https://github.com/acme/widgets/pull/9999",
		},
		{
			name:    "garbage on stdout",
			spawner: &fakeSpawner{stdout: "not json at all"},
			url:     "https://github.com/acme/widgets/pull/1",
		},
		{
			name:    "truncated json",
			spawner: &fakeSpawner{stdout: `{"number":1,"state":"MER`},
			url:     "https://github.com/acme/widgets/pull/1",
		},
		{
			name:    "a state nobody has heard of",
			spawner: &fakeSpawner{stdout: `{"number":1,"state":"DRAFTED"}`},
			url:     "https://github.com/acme/widgets/pull/1",
		},
		{
			name:    "empty output and a clean exit",
			spawner: &fakeSpawner{},
			url:     "https://github.com/acme/widgets/pull/1",
		},
		{
			name:    "unparseable url never reaches gh",
			spawner: &fakeSpawner{stdout: `{"number":1,"state":"MERGED"}`},
			url:     "not-a-url",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Client{Spawner: tt.spawner}.Status(context.Background(), tt.url)
			if err == nil {
				t.Fatalf("Status() = %+v, nil error; want an error so the binding keeps its state", got)
			}
			if got.State != wf.BindingUnknown {
				t.Errorf("State = %q, want no state at all on a failed lookup", got.State)
			}
		})
	}
}

func TestStatusDoesNotSpawnForAnUnparseableURL(t *testing.T) {
	// Guessing at the repository would ask GitHub about the wrong pull
	// request, which is a wrong answer rather than a missing one.
	s := &fakeSpawner{stdout: `{"number":1,"state":"MERGED"}`}
	if _, err := (Client{Spawner: s}).Status(context.Background(), "https://example.com/whatever"); err == nil {
		t.Fatal("Status() = nil error for an unrecognized url")
	}
	if s.calls != 0 {
		t.Errorf("spawned %d time(s), want 0 — an unparseable url is not a lookup", s.calls)
	}
}

func TestStatusSurfacesGhsOwnDiagnostic(t *testing.T) {
	s := &fakeSpawner{stderr: "gh: Not Found (HTTP 404)", err: errors.New("exit status 1")}
	_, err := Client{Spawner: s}.Status(context.Background(), "https://github.com/acme/widgets/pull/1")
	if err == nil {
		t.Fatal("Status() = nil error")
	}
	if !strings.Contains(err.Error(), "Not Found") {
		t.Errorf("error = %v, want gh's own text surfaced", err)
	}
}

func TestNoSpawnerIsAnErrorNotAnAnswer(t *testing.T) {
	if _, err := (Client{}).Status(context.Background(), "https://github.com/acme/widgets/pull/1"); err == nil {
		t.Fatal("Status() with no spawner = nil error")
	}
}
