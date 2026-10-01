package main

import (
	"os"
	"slices"
	"testing"
)

func TestReleaseTags(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	mustGit := func(args ...string) string {
		t.Helper()
		out, err := git(args...)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	mustGit("init", "--quiet", "--initial-branch=main")
	mustGit("config", "user.name", "test")
	mustGit("config", "user.email", "test@example.com")
	mustGit("commit", "--quiet", "--allow-empty", "-m", "first")
	first := mustGit("rev-parse", "HEAD")
	mustGit("tag", "v0.1.0")
	mustGit("commit", "--quiet", "--allow-empty", "-m", "second")
	second := mustGit("rev-parse", "HEAD")
	mustGit("tag", "-a", "-m", "annotated", "v0.2.0")
	mustGit("tag", "interop/v0.1.0")

	got, err := releaseTags()
	want := []tag{{Version{0, 1, 0}, first}, {Version{0, 2, 0}, second}}
	if err != nil || !slices.Equal(got, want) {
		t.Errorf("releaseTags() = %v, %v; want %v", got, err, want)
	}
}
