package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// header matches a TOML table header on its own line. A task body line such as
// `[ -n "${CI_SLOT:-}" ] || exec …` must not be read as one.
var header = regexp.MustCompile(`^\[[A-Za-z0-9_.:"\-]+\]$`)

// taskBlocks returns the body of every [tasks.<name>] block in mise.toml,
// keyed by the task name. Comments above a header belong to the block before
// it, so a caller that greps a block must ignore its '#' lines.
func taskBlocks(t *testing.T) map[string]string {
	t.Helper()
	content, err := os.ReadFile("mise.toml")
	if err != nil {
		t.Fatalf("read mise.toml: %v", err)
	}

	blocks := map[string]string{}
	name := ""
	var body []string
	flush := func() {
		if name != "" {
			blocks[name] = strings.Join(body, "\n")
		}
	}
	for _, line := range strings.Split(string(content), "\n") {
		if header.MatchString(line) {
			flush()
			name, body = "", nil
			if strings.HasPrefix(line, "[tasks.") {
				name = strings.Trim(strings.TrimSuffix(strings.TrimPrefix(line, "[tasks."), "]"), `"`)
			}
			continue
		}
		if name != "" {
			body = append(body, line)
		}
	}
	flush()
	return blocks
}

// firstRunLine is the first non-empty line of the task's run body, in either
// the triple-quoted or the inline form.
func firstRunLine(t *testing.T, block string) string {
	t.Helper()
	lines := strings.Split(block, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		var body []string
		switch {
		case trimmed == "run = '''" || trimmed == `run = """`:
			body = lines[i+1:]
		case strings.HasPrefix(trimmed, `run = "`):
			inline := strings.TrimSuffix(strings.TrimPrefix(trimmed, `run = "`), `"`)
			return inline
		default:
			continue
		}
		for _, candidate := range body {
			if strings.TrimSpace(candidate) != "" {
				return strings.TrimSpace(candidate)
			}
		}
	}
	t.Fatalf("task has no non-empty run line")
	return ""
}

// DLV-002: each level opens with its ci-slot line, because a dependency would
// run before the guard could take the slot.
func TestCILevelsStartWithTheirGuard(t *testing.T) {
	blocks := taskBlocks(t)
	want := map[string]string{
		"ci:fast": ".mise/lib/ci-slot --own-tree",
		"ci":      `[ -n "${CI_SLOT:-}" ] || exec .mise/lib/ci-slot ci`,
		"ci:full": `[ -n "${CI_SLOT:-}" ] || exec .mise/lib/ci-slot ci:full`,
	}
	for name, guard := range want {
		block, ok := blocks[name]
		if !ok {
			t.Errorf("mise.toml has no %s task", name)
			continue
		}
		if got := firstRunLine(t, block); got != guard {
			t.Errorf("%s run starts with %q, want %q", name, got, guard)
		}
	}
}

func TestCILevelsDeclareNoDepends(t *testing.T) {
	blocks := taskBlocks(t)
	for _, name := range []string{"ci:fast", "ci", "ci:full"} {
		block, ok := blocks[name]
		if !ok {
			t.Errorf("mise.toml has no %s task", name)
			continue
		}
		for _, line := range strings.Split(block, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "depends") {
				t.Errorf("%s has a %s line; the level runs its own steps", name, strings.TrimSpace(line))
			}
		}
	}
}

// The pre-commit hook runs the fast level; running a heavier one is the
// finding DLV-002 names. Comments do not count: a commented-out `ci:fast` with
// a live `mise run ci` under it must still fail here.
func TestPreCommitHookRunsTheFastLevel(t *testing.T) {
	content, err := os.ReadFile(".githooks/pre-commit")
	if err != nil {
		t.Fatalf("read pre-commit hook: %v", err)
	}

	found := false
	for _, line := range strings.Split(string(content), "\n") {
		command := strings.TrimSpace(line)
		if command == "" || strings.HasPrefix(command, "#") {
			continue
		}
		if comment := strings.Index(command, " #"); comment >= 0 {
			command = strings.TrimSpace(command[:comment])
		}
		if command == "exec mise run ci:fast" {
			found = true
		}
		// Split on shell separators so `mise run ci; exec …` is read as the
		// heavier call it is, not as the token `ci;`.
		separated := strings.NewReplacer(";", " ", "&", " ", "|", " ").Replace(command)
		fields := strings.Fields(separated)
		for i := 0; i+2 < len(fields); i++ {
			if fields[i] != "mise" || fields[i+1] != "run" {
				continue
			}
			if level := fields[i+2]; level == "ci" || level == "ci:full" {
				t.Errorf("pre-commit hook runs %s; a hook stays on ci:fast", level)
			}
		}
	}
	if !found {
		t.Errorf("pre-commit hook does not run ci:fast:\n%s", string(content))
	}
}
