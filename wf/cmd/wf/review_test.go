package main

import "testing"

func TestReviewArgsMutualExclusion(t *testing.T) {
	tests := []struct {
		name    string
		ref     string
		pr      string
		wantErr bool
	}{
		{
			name: "ref alone",
			ref:  "abc4",
		},
		{
			name: "--pr alone",
			pr:   "https://github.com/acme/widgets/pull/1",
		},
		{
			name:    "both ref and --pr is a usage error",
			ref:     "abc4",
			pr:      "https://github.com/acme/widgets/pull/1",
			wantErr: true,
		},
		{
			name: "neither is fine at this layer — cmdReview itself rejects it",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref, pr, err := reviewArgs(tt.ref, tt.pr)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("reviewArgs(%q, %q) = nil error, want one", tt.ref, tt.pr)
				}
				return
			}
			if err != nil {
				t.Fatalf("reviewArgs(%q, %q) error = %v", tt.ref, tt.pr, err)
			}
			if ref != tt.ref {
				t.Errorf("ref = %q, want %q", ref, tt.ref)
			}
			if pr != tt.pr {
				t.Errorf("pr = %q, want %q", pr, tt.pr)
			}
		})
	}
}
