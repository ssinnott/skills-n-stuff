package review

import (
	"strings"
	"testing"
)

func TestFormatCommentCarriesPrefixAndText(t *testing.T) {
	got := FormatComment("  paste from difit's Copy All Prompt  \n")
	if !strings.HasPrefix(got, CommentPrefix) {
		t.Errorf("FormatComment() = %q, want it to start with the prefix", got)
	}
	if !strings.Contains(got, "paste from difit's Copy All Prompt") {
		t.Errorf("FormatComment() lost the pasted text: %q", got)
	}
	if strings.Contains(got, "  \n") {
		t.Error("FormatComment() should trim the pasted text")
	}
}
