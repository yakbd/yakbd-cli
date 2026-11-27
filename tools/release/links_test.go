package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// pinnedLink is a link to one release or a tag-pinned file of a yakbd repository. Owner rule (2026-11-27): docs and
// the release-notes template link to releases/latest (downloads as releases/latest/download/<name without a
// version>) and to blob/main, because old releases and their tags are deleted. Build scripts may pin tags.
var pinnedLink = regexp.MustCompile(`github\.com/yakbd/[A-Za-z0-9._-]+/(releases/tag/|blob/v[0-9])|/blob/v\$`)

// TestNoPinnedReleaseLinks fails on such a link in a Markdown file of the repository or in the release workflow.
func TestNoPinnedReleaseLinks(t *testing.T) {
	root := "../.."
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "testdata") {
			return filepath.SkipDir
		}
		if d.IsDir() || !(strings.HasSuffix(p, ".md") || strings.HasSuffix(p, filepath.Join(".github", "workflows", "release.yml"))) {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(b), "\n") {
			if pinnedLink.MatchString(line) {
				t.Errorf("%s:%d links to one release or tag: %s", p, i+1, strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
