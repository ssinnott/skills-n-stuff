package review

import "testing"

func TestPRHandle(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{
			name: "github URL",
			url:  "https://github.com/acme/widgets/pull/482",
			want: "acme/widgets#482",
		},
		{
			name: "github URL with a trailing slash",
			url:  "https://github.com/acme/widgets/pull/482/",
			want: "acme/widgets#482",
		},
		{
			name: "non-github URL falls back to the raw URL",
			url:  "https://gitlab.com/acme/widgets/-/merge_requests/12",
			want: "https://gitlab.com/acme/widgets/-/merge_requests/12",
		},
		{
			name: "garbage falls back to the trimmed input",
			url:  "  not a url at all  ",
			want: "not a url at all",
		},
		{
			name: "github repo URL with no pull path falls back",
			url:  "https://github.com/acme/widgets",
			want: "https://github.com/acme/widgets",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PRHandle(tt.url); got != tt.want {
				t.Errorf("PRHandle(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}

func TestPRHandleNeverEmptyForNonEmptyInput(t *testing.T) {
	// Result.Ref, and the JSON contract's "ref" field, must never be
	// empty — a caller only ever calls PRHandle with a non-empty --pr
	// value, and the handle must carry through as something, even if it's
	// just the raw url unrecognized.
	for _, url := range []string{"x", "https://github.com/a/b/pull/1", "://not even a url"} {
		if got := PRHandle(url); got == "" {
			t.Errorf("PRHandle(%q) = \"\", want a non-empty handle", url)
		}
	}
}
