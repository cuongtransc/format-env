package main

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"text/template"
)

// version is stamped at build time: -ldflags "-X main.version=<git describe>"
// (mise run build / cli:install); a plain `go build` says dev.
var version = "dev"

// Exit codes: 1 = a check found something (secret-like value, missing key),
// 2 = usage or an error that left the stage file untouched.
const (
	exitFound = 1
	exitError = 2
)

var (
	keyLine    = regexp.MustCompile(`^(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=(.*)$`)
	secretName = regexp.MustCompile(`(SECRET|TOKEN|PASSWORD|PASSWD|_PASS$|PRIVATE|(^|_)KEY$|API_KEY|MNEMONIC|SEED)`)
	bareAction = regexp.MustCompile(`^\{\{\s*\.([A-Za-z_][A-Za-z0-9_]*)\s*\}\}$`)
	// A template line that declares KEY, commented or not: `KEY=` or `# KEY=`.
	declared = regexp.MustCompile(`^(?:#\s*)?([A-Za-z_][A-Za-z0-9_]*)=`)
)

func defaultFunc(value any, fallback string) string {
	if stringValue, ok := value.(string); ok && stringValue != "" {
		return stringValue
	}
	return fallback
}

// readConfigFile reads the key-value pairs from a .env file; a missing file reads as empty.
func readConfigFile(filePath string) (map[string]string, error) {
	content, err := os.ReadFile(filePath)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	config, _ := parseEnv(string(content))
	return config, nil
}

// parseEnv returns the KEY=value pairs of an env file and the keys in file order.
// Comments, blank lines and lines that are not KEY=value are skipped; an `export `
// prefix is the same key; the last value of a key wins.
func parseEnv(text string) (map[string]string, []string) {
	config := make(map[string]string)
	var order []string
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if len(line) == 0 || strings.HasPrefix(line, "#") {
			continue
		}
		m := keyLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		key := m[1]
		if _, seen := config[key]; !seen {
			order = append(order, key)
		}
		config[key] = strings.TrimSpace(m[2])
	}
	return config, order
}

// formatEnv renders <envDir>/_template.env into <envDir>/<stage>.env.
func formatEnv(envDir string, stage string) error {
	return renderStage(envDir, stage, false, io.Discard)
}

// renderStage renders one stage and reports the keys it adds, removes and changes
// (names only, never values). With dryRun nothing is written.
func renderStage(envDir, stage string, dryRun bool, out io.Writer) error {
	configPath := filepath.Join(envDir, stage+".env")
	templatePath := filepath.Join(envDir, "_template.env")

	templateContent, err := os.ReadFile(templatePath)
	if err != nil {
		return fmt.Errorf("failed to read template file: %w", err)
	}

	oldContent, err := os.ReadFile(configPath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		if errors.Is(err, fs.ErrPermission) {
			return fmt.Errorf("cannot read %s (%s); chown it to the user that renders it", configPath, ownerNote(configPath))
		}
		return fmt.Errorf("failed to read config file: %w", err)
	}
	oldConfig, oldOrder := parseEnv(string(oldContent))

	tmpl, err := template.New("env").Option("missingkey=zero").Funcs(template.FuncMap{
		"df": defaultFunc,
	}).Parse(string(templateContent))
	if err != nil {
		return fmt.Errorf("failed to parse template file: %w; %s left untouched", err, configPath)
	}

	var output bytes.Buffer
	if err := tmpl.Execute(&output, oldConfig); err != nil {
		return fmt.Errorf("failed to execute template: %w; %s left untouched", err, configPath)
	}

	newConfig, newOrder := parseEnv(output.String())
	var added, removed, changed []string
	for _, k := range newOrder {
		old, ok := oldConfig[k]
		switch {
		case !ok:
			added = append(added, k)
		case old != newConfig[k]:
			changed = append(changed, k)
		}
	}
	for _, k := range oldOrder {
		if _, ok := newConfig[k]; !ok {
			removed = append(removed, k)
		}
	}
	note := ""
	if dryRun {
		note = " (dry run)"
	}
	fmt.Fprintf(out, "%s: +%d -%d ~%d%s\n", configPath, len(added), len(removed), len(changed), note)
	for _, group := range []struct {
		sign string
		keys []string
	}{{"+", added}, {"-", removed}, {"~", changed}} {
		for _, k := range group.keys {
			fmt.Fprintf(out, "  %s %s\n", group.sign, k)
		}
	}

	if dryRun {
		return nil
	}
	if err := writeAtomic(configPath, output.Bytes()); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}
	return nil
}

// writeAtomic replaces path in one rename. An existing file keeps its mode, owner and
// group (a render run with sudo must not hand the deploy user's file to root); a new
// file is 0600, since stage files hold secrets.
func writeAtomic(path string, data []byte) error {
	info, statErr := os.Stat(path)
	tmp, err := os.CreateTemp(filepath.Dir(path), ".fenv-")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	fail := func(err error) error {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return fail(err)
	}
	mode := fs.FileMode(0600)
	if statErr == nil {
		mode = info.Mode().Perm()
	}
	if err := tmp.Chmod(mode); err != nil {
		return fail(err)
	}
	if statErr == nil {
		if st, ok := info.Sys().(*syscall.Stat_t); ok {
			// A no-op for the owner itself; another user's file needs root, else an error and no change.
			if err := tmp.Chown(int(st.Uid), int(st.Gid)); err != nil {
				return fail(err)
			}
		}
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// ownerNote names the file's owner and the user running fenv, for a permission error.
func ownerNote(path string) string {
	me := "unknown"
	if u, err := user.Current(); err == nil {
		me = u.Username
	}
	owner := "unknown"
	if info, err := os.Stat(path); err == nil {
		if st, ok := info.Sys().(*syscall.Stat_t); ok {
			owner = strconv.Itoa(int(st.Uid))
			if u, err := user.LookupId(owner); err == nil {
				owner = u.Username
			}
		}
	}
	return fmt.Sprintf("owner %s, running as %s", owner, me)
}

// templateProblems lists secret-like keys that carry a literal value or a df default
// in the template instead of a bare {{ .KEY }} or nothing.
func templateProblems(tmpl string) []string {
	var problems []string
	for _, line := range strings.Split(tmpl, "\n") {
		m := keyLine.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil || !secretName.MatchString(m[1]) {
			continue
		}
		rhs := strings.TrimSpace(m[2])
		if rhs != "" && !bareAction.MatchString(rhs) {
			problems = append(problems, m[1])
		}
	}
	return problems
}

// missingKeys returns, in byte order and without duplicates, the keys the template does
// not declare. A commented `# KEY=` line declares a key that stays unset by default.
func missingKeys(tmpl string, keys []string) []string {
	have := map[string]bool{}
	for _, line := range strings.Split(tmpl, "\n") {
		if m := declared.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			have[m[1]] = true
		}
	}
	seen := map[string]bool{}
	var missing []string
	for _, k := range keys {
		k = strings.TrimSpace(k)
		if k == "" || have[k] || seen[k] {
			continue
		}
		seen[k] = true
		missing = append(missing, k)
	}
	sort.Strings(missing)
	return missing
}

const usage = `Usage:
  fenv --version                          print the version
  fenv [--dry-run] <env_dir> <stages>     render <env_dir>/_template.env into <env_dir>/<stage>.env
  fenv --check-template <env_dir>         fail if a secret-like key has a value in the template
  fenv --missing <keys_file|-> <env_dir>  list the keys (one per line) the template does not declare`

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("fenv", flag.ContinueOnError)
	fl.SetOutput(stderr)
	fl.Usage = func() { fmt.Fprintln(stderr, usage) }
	dryRun := fl.Bool("dry-run", false, "report key changes, write nothing")
	checkTemplate := fl.Bool("check-template", false, "only validate the template's secret-like keys")
	missing := fl.String("missing", "", "file of keys to look up in the template (- for stdin)")
	showVersion := fl.Bool("version", false, "print the version")
	if err := fl.Parse(args); err != nil {
		return exitError
	}
	if *showVersion {
		fmt.Fprintf(stdout, "fenv %s\n", version)
		return 0
	}
	if fl.NArg() < 1 {
		// No arguments prints the usage and succeeds, as fenv always did (the
		// pre-commit hook entry runs it bare).
		fl.Usage()
		return 0
	}
	envDir := fl.Arg(0)
	templateContent, err := os.ReadFile(filepath.Join(envDir, "_template.env"))
	if err != nil {
		fmt.Fprintf(stderr, "fenv: %v\n", err)
		return exitError
	}

	switch {
	case *checkTemplate:
		bad := templateProblems(string(templateContent))
		for _, k := range bad {
			fmt.Fprintf(stdout, "%s: secret-like key must be bare (%s={{ .%s }}) in the template\n", k, k, k)
		}
		if len(bad) > 0 {
			return exitFound
		}
		return 0
	case *missing != "":
		var keys []byte
		if *missing == "-" {
			keys, err = io.ReadAll(stdin)
		} else {
			keys, err = os.ReadFile(*missing)
		}
		if err != nil {
			fmt.Fprintf(stderr, "fenv: %v\n", err)
			return exitError
		}
		absent := missingKeys(string(templateContent), strings.Split(string(keys), "\n"))
		for _, k := range absent {
			fmt.Fprintln(stdout, k)
		}
		if len(absent) > 0 {
			return exitFound
		}
		return 0
	}

	if fl.NArg() < 2 || fl.Arg(1) == "" {
		fmt.Fprintln(stderr, "fenv: stages required (e.g. dev,prod)")
		fl.Usage()
		return exitError
	}
	for _, stage := range strings.Split(fl.Arg(1), ",") {
		if stage == "" {
			continue
		}
		if err := renderStage(envDir, stage, *dryRun, stdout); err != nil {
			fmt.Fprintf(stderr, "fenv: %v\n", err)
			return exitError
		}
	}
	return 0
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
