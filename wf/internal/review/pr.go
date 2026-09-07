package review

import (
	"regexp"
	"strings"
)

// githubPRRe recognizes a github.com pull request URL. Only github.com is
// special-cased.
var githubPRRe = regexp.MustCompile(`^https?://github\.com/([\w.-]+)/([\w.-]+)/pull/(\d+)/?$`)

// PRHandle derives a short handle from a PR url for `wf review --pr`,
// which has no task ShortID: "owner/repo#123" for GitHub, else the
// trimmed url.
func PRHandle(url string) string {
	trimmed := strings.TrimSpace(url)
	if m := githubPRRe.FindStringSubmatch(trimmed); m != nil {
		return m[1] + "/" + m[2] + "#" + m[3]
	}
	return trimmed
}
