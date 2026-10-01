// Command release tags a commit of main with the next semantic version and
// creates its GitHub release (the release job of .github/workflows/ci.yml),
// or, with -plan-pr, reports the version that merging a pull request would
// release (.github/workflows/release-plan.yml).
//
//	release -commit $GITHUB_SHA                         # after CI passed on main
//	release -commit HEAD -plan-pr 12 -apidiff apidiff   # in a pull request's merge commit
//
// The release level of each commit is the release:major, release:minor or
// release:patch label of the pull request GitHub merged it from (patch
// without a label, or for a commit pushed directly). A release covers every
// commit since the previous release tag and takes the highest level among
// them, so a release that never ran (a cancelled job, a failed CI run) is
// folded into the next one without losing a breaking change.
//
// The plan also compares the exported API of the merge commit's first
// parent (the base branch) and the merge commit with apidiff
// (golang.org/x/exp/cmd/apidiff), and fails when it reports incompatible
// changes but the pull request's level is below minor in v0, or below
// major from v1 on, so that a forgotten label cannot release a breaking
// change as a patch.
//
// The tags are the only state. Running release again for the same commit, or
// for a commit an existing release already contains, changes nothing, and a
// run that stopped between creating the tag and the release finishes the
// release. Tags are created with the Git refs API, which fails if the tag
// exists, so two runs can never give one version to two commits.
//
// It runs git in the current repository and gh with GH_TOKEN, and reads
// GITHUB_REPOSITORY. With GITHUB_OUTPUT set, a run that leaves the commit
// released writes the outputs tag and module.
package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

func main() {
	log.SetFlags(0)
	log.SetPrefix("release: ")
	commit := flag.String("commit", "HEAD", "commit to release, or the pull request's merge commit with -plan-pr")
	planPR := flag.Int("plan-pr", 0, "report the release for merging this pull request, without creating anything")
	apidiff := flag.String("apidiff", "", "apidiff executable, required with -plan-pr")
	flag.Parse()

	repo := os.Getenv("GITHUB_REPOSITORY")
	if repo == "" {
		log.Fatal("GITHUB_REPOSITORY is not set")
	}
	if *planPR > 0 && *apidiff == "" {
		log.Fatal("-plan-pr needs -apidiff")
	}
	r := &releaser{repo: repo, base: "github.com/" + repo}
	sha, err := git("rev-parse", "--verify", *commit+"^{commit}")
	if err != nil {
		log.Fatal(err)
	}
	if *planPR > 0 {
		err = r.plan(sha, *planPR, *apidiff)
	} else {
		err = r.release(sha)
	}
	if err != nil {
		log.Fatal(err)
	}
}

type releaser struct {
	repo string // owner/name
	base string // module path without a /vN suffix
}

type tag struct {
	v      Version
	commit string
}

// version returns the version of t, or nil when there is no tag yet.
func (t *tag) version() *Version {
	if t == nil {
		return nil
	}
	return &t.v
}

// release tags sha with the next version and creates its GitHub release.
func (r *releaser) release(sha string) error {
	if _, err := git("fetch", "--quiet", "--tags", "origin"); err != nil {
		return err
	}
	tags, err := releaseTags()
	if err != nil {
		return err
	}
	// Already tagged (a re-run, or a run that stopped before the release).
	for _, t := range slices.Backward(tags) {
		if t.commit == sha {
			log.Printf("%s is already tagged %s", sha, t.v)
			return r.ensureRelease(t, previous(tags, t.v))
		}
	}
	var latest *tag
	if len(tags) > 0 {
		latest = &tags[len(tags)-1]
		if ancestor(sha, latest.commit) {
			log.Printf("%s is already part of %s; nothing to release", sha, latest.v)
			return nil
		}
		if !ancestor(latest.commit, sha) {
			return fmt.Errorf("%s (%s) is not an ancestor of %s: refusing to release a diverged history", latest.v, latest.commit, sha)
		}
	}
	level, why, err := r.levelSince(latest, sha)
	if err != nil {
		return err
	}
	next, err := r.next(latest, level, sha)
	if err != nil {
		return err
	}
	log.Printf("releasing %s at %s (%s: %s)", next, sha, level, why)
	t := tag{next, sha}
	if err := r.createTag(t); err != nil {
		return err
	}
	return r.ensureRelease(t, latest)
}

// plan reports what merging pull request pr (whose merge commit is sha)
// would release, and fails where release would fail after the merge or
// where the level is too low for the change to the exported API.
func (r *releaser) plan(sha string, pr int, apidiff string) error {
	tags, err := releaseTags()
	if err != nil {
		return err
	}
	var latest *tag
	if len(tags) > 0 {
		latest = &tags[len(tags)-1]
	}
	// Commits already on main but not yet released, then this pull request.
	mainTip, err := git("rev-parse", sha+"^1")
	if err != nil {
		return err
	}
	level, why, err := r.levelSince(latest, mainTip)
	if err != nil {
		return err
	}
	labels, err := r.prLabels(pr)
	if err != nil {
		return err
	}
	prLevel, err := LevelFromLabels(labels)
	if err != nil {
		return fmt.Errorf("pull request #%d: %w", pr, err)
	}
	change, report, err := apiChange(apidiff, mainTip, sha)
	if err != nil {
		return err
	}
	if floor := MinLevel(latest.version(), change); prLevel < floor {
		return fmt.Errorf("pull request #%d makes incompatible changes to the exported API (apidiff above), which need the label release:%s", pr, floor)
	}
	if prLevel >= level {
		level, why = prLevel, fmt.Sprintf("#%d", pr)
	}
	next, err := r.next(latest, level, sha)
	if err != nil {
		return err
	}
	if latest == nil {
		why = "the first release"
	} else {
		why = fmt.Sprintf("%s, from %s", level, why)
	}
	msg := fmt.Sprintf("Merging #%d releases **%s** (%s).", pr, next, why)
	log.Print(msg)
	api := fmt.Sprintf("Exported API since %.7s: %s.", mainTip, change)
	if report != "" {
		api += "\n\n```\n" + report + "\n```"
	}
	return summary(msg + "\n\n" + api + "\n\nSet the level with one label: `release:major`, `release:minor` or `release:patch` (the default).\n")
}

// levelSince returns the highest release level among the first-parent
// commits after latest up to sha, and the pull request that set it.
func (r *releaser) levelSince(latest *tag, sha string) (Level, string, error) {
	rng := sha
	if latest != nil {
		rng = latest.commit + ".." + sha
	}
	out, err := git("rev-list", "--first-parent", rng)
	if err != nil {
		return 0, "", err
	}
	level, why := Patch, "no release label"
	for c := range strings.FieldsSeq(out) {
		pr, labels, err := r.mergedFrom(c)
		if err != nil {
			return 0, "", err
		}
		if pr == 0 {
			continue // pushed directly: patch
		}
		l, err := LevelFromLabels(labels)
		if err != nil {
			return 0, "", fmt.Errorf("pull request #%d (merged as %s): %w; fix its labels and re-run", pr, c, err)
		}
		if l > level {
			level, why = l, fmt.Sprintf("#%d", pr)
		}
	}
	return level, why, nil
}

// mergedFrom returns the pull request that commit c of main was merged from
// (0 when it was pushed directly) and that pull request's current labels.
// Only a pull request whose merge commit is c counts: the API also lists
// open pull requests that contain c.
func (r *releaser) mergedFrom(c string) (int, []string, error) {
	out, err := gh("api", fmt.Sprintf("repos/%s/commits/%s/pulls", r.repo, c), "--jq",
		fmt.Sprintf(`.[] | select(.merge_commit_sha == "%s") | "\(.number) \([.labels[].name] | join(","))"`, c))
	if err != nil || out == "" {
		return 0, nil, err
	}
	num, labels, _ := strings.Cut(out, " ")
	n, err := strconv.Atoi(num)
	return n, splitNonEmpty(labels), err
}

func (r *releaser) prLabels(pr int) ([]string, error) {
	// Read now, not from the event: a re-run reuses the original event.
	out, err := gh("api", fmt.Sprintf("repos/%s/pulls/%d", r.repo, pr), "--jq", `[.labels[].name] | join(",")`)
	if err != nil {
		return nil, err
	}
	return splitNonEmpty(out), nil
}

// next computes the next version and checks it against go.mod at sha.
func (r *releaser) next(latest *tag, level Level, sha string) (Version, error) {
	path, err := moduleAt(sha)
	if err != nil {
		return Version{}, err
	}
	return Next(latest.version(), level, path, r.base)
}

// moduleAt returns the module path declared by go.mod at commit c.
func moduleAt(c string) (string, error) {
	gomod, err := git("show", c+":go.mod")
	if err != nil {
		return "", err
	}
	return modulePath(gomod)
}

// apiChange compares the exported API of the module at the commits base
// and head with apidiff, and returns the change and apidiff's report.
func apiChange(apidiff, base, head string) (APIChange, string, error) {
	tmp, err := os.MkdirTemp("", "release-apidiff-")
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	api := filepath.Join(tmp, "base.api")
	if _, err := apidiffAt(apidiff, base, filepath.Join(tmp, "base"), "-m", "-w", api); err != nil {
		return 0, "", err
	}
	report, err := apidiffAt(apidiff, head, filepath.Join(tmp, "head"), "-m", api)
	if err != nil {
		return 0, "", err
	}
	if report != "" {
		log.Printf("apidiff %.7s..%.7s:\n%s", base, head, report)
	}
	change, err := ParseAPIDiff(report)
	return change, report, err
}

// apidiffAt extracts the tree of commit c into dir, so the working tree
// does not matter, and runs apidiff there with args and the module path
// of c.
func apidiffAt(apidiff, c, dir string, args ...string) (string, error) {
	path, err := moduleAt(c)
	if err != nil {
		return "", err
	}
	if err := os.Mkdir(dir, 0o755); err != nil {
		return "", err
	}
	if _, err := git("archive", "-o", dir+".tar", c); err != nil {
		return "", err
	}
	if _, err := run("", "tar", "-xf", dir+".tar", "-C", dir); err != nil {
		return "", err
	}
	return run(dir, apidiff, append(args, path)...)
}

// createTag creates the tag through the refs API, which refuses an existing
// ref: the atomic step that makes a version belong to one commit.
func (r *releaser) createTag(t tag) error {
	_, err := gh("api", "-X", "POST", fmt.Sprintf("repos/%s/git/refs", r.repo),
		"-f", "ref=refs/tags/"+t.v.String(), "-f", "sha="+t.commit)
	if err == nil {
		return nil
	}
	// It may exist from a concurrent or interrupted run: fine only if it
	// points at the same commit.
	got, lerr := git("ls-remote", "origin", "refs/tags/"+t.v.String())
	if lerr == nil && strings.HasPrefix(got, t.commit) {
		return nil
	}
	return fmt.Errorf("creating tag %s at %s: %w (remote has %q)", t.v, t.commit, err, got)
}

// ensureRelease creates the GitHub release of an existing tag unless it
// exists. Generated notes list the pull requests since prev.
func (r *releaser) ensureRelease(t tag, prev *tag) error {
	if _, err := gh("release", "view", t.v.String(), "--json", "tagName"); err == nil {
		log.Printf("release %s exists", t.v)
	} else {
		args := []string{"release", "create", t.v.String(), "--verify-tag", "--title", t.v.String(),
			"--notes", fmt.Sprintf("```\ngo get %s@%s\n```", ModuleFor(r.base, t.v), t.v), "--generate-notes"}
		if prev != nil {
			args = append(args, "--notes-start-tag", prev.v.String())
		}
		if _, err := gh(args...); err != nil {
			return err
		}
		log.Printf("created release %s", t.v)
	}
	if err := summary(fmt.Sprintf("Released **%s** at %s.\n", t.v, t.commit)); err != nil {
		return err
	}
	return output(map[string]string{"tag": t.v.String(), "module": ModuleFor(r.base, t.v)})
}

// releaseTags returns the release tags in increasing version order, with
// the commits they point at (annotated tags peeled).
func releaseTags() ([]tag, error) {
	out, err := git("for-each-ref", "--format=%(refname:strip=2) %(objectname) %(*objectname)", "refs/tags")
	if err != nil {
		return nil, err
	}
	var tags []tag
	for line := range strings.SplitSeq(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		v, ok := ParseVersion(f[0])
		if !ok {
			continue
		}
		c := f[1]
		if len(f) == 3 {
			c = f[2]
		}
		tags = append(tags, tag{v, c})
	}
	slices.SortFunc(tags, func(a, b tag) int {
		switch {
		case a.v.Less(b.v):
			return -1
		case b.v.Less(a.v):
			return 1
		}
		return 0
	})
	return tags, nil
}

// previous returns the highest tag below v, or nil.
func previous(tags []tag, v Version) *tag {
	for i := len(tags) - 1; i >= 0; i-- {
		if tags[i].v.Less(v) {
			return &tags[i]
		}
	}
	return nil
}

func ancestor(a, b string) bool {
	return exec.Command("git", "merge-base", "--is-ancestor", a, b).Run() == nil
}

func git(args ...string) (string, error) { return run("", "git", args...) }
func gh(args ...string) (string, error)  { return run("", "gh", args...) }

// run runs a command in dir (empty: the current directory) and returns its
// trimmed standard output.
func run(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}

func splitNonEmpty(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}

// output appends step outputs to $GITHUB_OUTPUT when it is set.
func output(kv map[string]string) error {
	var b strings.Builder
	for _, k := range slices.Sorted(maps.Keys(kv)) {
		b.WriteString(k + "=" + kv[k] + "\n")
	}
	return appendEnvFile("GITHUB_OUTPUT", b.String())
}

func summary(md string) error { return appendEnvFile("GITHUB_STEP_SUMMARY", md) }

func appendEnvFile(env, s string) error {
	path := os.Getenv(env)
	if path == "" {
		return nil
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	_, err = f.WriteString(s)
	return errors.Join(err, f.Close())
}
