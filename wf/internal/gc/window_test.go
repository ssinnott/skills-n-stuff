package gc

import (
	"testing"
	"time"
)

func TestParseWindow(t *testing.T) {
	tests := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{in: "", want: 0},
		{in: "30d", want: 30 * 24 * time.Hour},
		{in: "2w", want: 14 * 24 * time.Hour},
		{in: "0.5d", want: 12 * time.Hour},
		{in: "720h", want: 720 * time.Hour},
		{in: "90m", want: 90 * time.Minute},
		{in: " 7d ", want: 7 * 24 * time.Hour},
		{in: "-3d", wantErr: true},
		{in: "-1h", wantErr: true},
		{in: "soon", wantErr: true},
		{in: "3 fortnights", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseWindow(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseWindow(%q) = %v, want an error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseWindow(%q) error = %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("ParseWindow(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}
