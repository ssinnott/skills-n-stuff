package review

// PRHandle turns a PR url into the ad-hoc `wf review --pr` ref.
//
// `wf review <ref>` is always a task; `wf review --pr <url>` never is, so
// there is no ShortID to report as Result.Ref. A short human handle
// derived from the URL fills that role instead — jsonReview.Ref is
// required, so this must never come back empty.

import (
	"regexp"
	"strings"
)

// githubPRRe recognizes a github.com pull request URL, tolerating a
// trailing slash. Only github.com is special-cased: it is the overwhelming
// common case, and guessing at the URL shape of every other forge (GitLab
// merge requests, Gitea, Bitbucket) would be exactly the kind of
// unverified format-guessing this codebase avoids elsewhere.
var githubPRRe = regexp.MustCompile(`^https?://github\.com/([\w.-]+)/([\w.-]+)/pull/(\d+)/?$`)

// PRHandle derives a short handle from a PR url: "owner/repo#123" for a
// GitHub URL, or the trimmed URL itself when the shape isn't recognized —
// still a valid, non-empty ref, just a less pretty one.
func PRHandle(url string) string {
	trimmed := strings.TrimSpace(url)
	if m := githubPRRe.FindStringSubmatch(trimmed); m != nil {
		return m[1] + "/" + m[2] + "#" + m[3]
	}
	return trimmed
}
