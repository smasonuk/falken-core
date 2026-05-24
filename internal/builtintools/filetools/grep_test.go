package filetools

import (
	"strings"
	"testing"

	"github.com/smasonuk/falken-core/internal/runtimefiles"
)

func TestGrepContentShowsPaginationFooter(t *testing.T) {
	content := grepContent(runtimefiles.GrepResult{
		CommonResult:     runtimefiles.CommonResult{Success: true},
		OutputMode:       "content",
		Matches:          []runtimefiles.GrepMatch{{Path: "a.go", Line: 1, Text: "match"}},
		TotalMatchesSeen: 20,
		Returned:         1,
		Truncated:        true,
		NextOffset:       11,
	})
	for _, want := range []string{"--- pagination ---", "Total matches seen: 20", "Returned: 1", "Next offset: 11"} {
		if !strings.Contains(content, want) {
			t.Fatalf("content = %q, missing %q", content, want)
		}
	}
}
