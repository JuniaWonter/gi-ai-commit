package diff

import (
	"testing"
)

func TestParseNumStat(t *testing.T) {
	input := "10\t5\tpkg/file.go\n-\t-\tbinary.bin\n3\t2\tpkg/other.go"
	result := parseNumStat(input)

	if result["pkg/file.go"] != 15 {
		t.Errorf("expected 15, got %d", result["pkg/file.go"])
	}
	if result["binary.bin"] != 0 {
		t.Errorf("expected 0 for binary, got %d", result["binary.bin"])
	}
	if result["pkg/other.go"] != 5 {
		t.Errorf("expected 5, got %d", result["pkg/other.go"])
	}
}

func TestTruncateText(t *testing.T) {
	s := "hello world"
	if truncateText(s, 20) != s {
		t.Error("should not truncate short text")
	}
	result := truncateText(s, 8)
	if len(result) != 8 {
		t.Errorf("expected length 8, got %d", len(result))
	}
}

func TestExtractDiffPath(t *testing.T) {
	section := "diff --git a/pkg/file.go b/pkg/file.go\nindex 1234567..abcdef"
	path := extractDiffPath(section)
	if path != "pkg/file.go" {
		t.Errorf("expected pkg/file.go, got %s", path)
	}
}

func TestSortSections(t *testing.T) {
	sections := []diffSection{
		{Path: "c.go", Score: 5},
		{Path: "a.go", Score: 10},
		{Path: "b.go", Score: 5},
	}
	sortSections(sections)

	if sections[0].Path != "a.go" || sections[0].Score != 10 {
		t.Errorf("first should be a.go with score 10")
	}
	if sections[1].Path != "b.go" {
		t.Errorf("second should be b.go (same score, alphabetical)")
	}
	if sections[2].Path != "c.go" {
		t.Errorf("third should be c.go")
	}
}
