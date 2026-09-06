package main

import "testing"

func TestReviewArgsMutualExclusion(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantRef string
		wantPR  string
		wantErr bool
	}{
		{
			name:    "ref alone",
			args:    []string{"abc4"},
			wantRef: "abc4",
		},
		{
			name:   "--pr alone",
			args:   []string{"--pr", "https://github.com/acme/widgets/pull/1"},
			wantPR: "https://github.com/acme/widgets/pull/1",
		},
		{
			name:    "both ref and --pr is a usage error",
			args:    []string{"abc4", "--pr", "https://github.com/acme/widgets/pull/1"},
			wantErr: true,
		},
		{
			name: "neither is fine at this layer — cmdReview itself rejects it",
			args: []string{"--json"},
		},
		{
			name:    "--pr alongside unrelated flags",
			args:    []string{"--pr", "https://github.com/acme/widgets/pull/1", "--json", "--repo", "/r"},
			wantPR:  "https://github.com/acme/widgets/pull/1",
			wantRef: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref, pr, err := reviewArgs(tt.args)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("reviewArgs(%v) = nil error, want one", tt.args)
				}
				return
			}
			if err != nil {
				t.Fatalf("reviewArgs(%v) error = %v", tt.args, err)
			}
			if ref != tt.wantRef {
				t.Errorf("ref = %q, want %q", ref, tt.wantRef)
			}
			if pr != tt.wantPR {
				t.Errorf("pr = %q, want %q", pr, tt.wantPR)
			}
		})
	}
}
