package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestReadConfigFileDoesNotCreateAMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dev.env")

	got, err := readConfigFile(path)
	if err != nil {
		t.Fatalf("readConfigFile() error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("readConfigFile() = %#v, want empty config", got)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("reading a missing stage file created it (stat err = %v)", err)
	}
}

func TestRenderStageReportsKeyChangesWithoutValues(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "_template.env"), "A={{ .A }}\nB={{ df .B \"new\" }}\nC={{ df .C \"c\" }}\n")
	writeTestFile(t, filepath.Join(dir, "dev.env"), "A=secret-a\nB=\nOLD=gone\n")

	var out bytes.Buffer
	if err := renderStage(dir, "dev", false, &out); err != nil {
		t.Fatalf("renderStage() error = %v", err)
	}
	want := filepath.Join(dir, "dev.env") + ": +1 -1 ~1\n  + C\n  - OLD\n  ~ B\n"
	if out.String() != want {
		t.Fatalf("report = %q, want %q", out.String(), want)
	}
	if strings.Contains(out.String(), "secret-a") {
		t.Fatal("report leaked a value")
	}
}

func TestDryRunWritesNothingAndSaysSo(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "_template.env"), "A={{ df .A \"x\" }}\n")
	stage := filepath.Join(dir, "prod.env")
	writeTestFile(t, stage, "A=\nOLD=1\n")

	var out bytes.Buffer
	if err := renderStage(dir, "prod", true, &out); err != nil {
		t.Fatalf("renderStage() error = %v", err)
	}
	if !strings.HasPrefix(out.String(), stage+": +0 -1 ~1 (dry run)\n") || !strings.Contains(out.String(), "  - OLD\n") {
		t.Fatalf("dry-run report = %q", out.String())
	}
	if got, _ := os.ReadFile(stage); string(got) != "A=\nOLD=1\n" {
		t.Fatalf("dry run changed the stage file: %q", got)
	}
}

func TestDryRunOnAMissingStageCreatesNothing(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "_template.env"), "A={{ df .A \"x\" }}\n")

	if err := renderStage(dir, "prod", true, &bytes.Buffer{}); err != nil {
		t.Fatalf("renderStage() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "prod.env")); !os.IsNotExist(err) {
		t.Fatalf("dry run created the stage file (stat err = %v)", err)
	}
}

func TestANewStageFileIsPrivate(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "_template.env"), "A={{ df .A \"x\" }}\n")

	if err := renderStage(dir, "prod", false, &bytes.Buffer{}); err != nil {
		t.Fatalf("renderStage() error = %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, "prod.env"))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Fatalf("new stage file mode = %o, want 600 (it holds secrets)", got)
	}
}

func TestTheWriteLeavesNoTempFileBehind(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "_template.env"), "A={{ df .A \"x\" }}\n")
	writeTestFile(t, filepath.Join(dir, "dev.env"), "A=1\n")

	if err := renderStage(dir, "dev", false, &bytes.Buffer{}); err != nil {
		t.Fatalf("renderStage() error = %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if !reflect.DeepEqual(names, []string{"_template.env", "dev.env"}) {
		t.Fatalf("files after render = %v", names)
	}
}

func TestTemplateProblemsFlagSecretLikeKeysWithValues(t *testing.T) {
	tmpl := strings.Join([]string{
		"DB_PASS={{ .DB_PASS }}", // bare: fine
		"API_KEY=",               // empty: fine
		"JWT_SECRET={{ df .JWT_SECRET \"dev\" }}", // a default: flagged
		"STRIPE_TOKEN=sk_live_x",                  // a literal: flagged
		"MONKEY=banana",                           // not secret-like (no _KEY suffix)
		"SSH_KEY=abc",                             // flagged
		"# PRIVATE_URL=ignored comment",           // a comment is not a key line
		"PORT={{ df .PORT \"8000\" }}",            // not secret-like
	}, "\n")
	got := templateProblems(tmpl)
	want := []string{"JWT_SECRET", "STRIPE_TOKEN", "SSH_KEY"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("templateProblems() = %v, want %v", got, want)
	}
}

func TestMissingKeysCountsCommentedDeclarationsAndUsesByteOrder(t *testing.T) {
	tmpl := "DB_HOST={{ .DB_HOST }}\n# SENTRY_DSN=\nA_B=1\n"
	keys := []string{"DB_HOST", "SENTRY_DSN", "AB_C", "A_B", "Z", "AB_C", ""}
	got := missingKeys(tmpl, keys)
	want := []string{"AB_C", "Z"} // deduplicated, blank lines skipped, byte order
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("missingKeys() = %v, want %v", got, want)
	}
}

func TestRunExitCodes(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "_template.env"), "DB_HOST={{ .DB_HOST }}\nAPI_TOKEN=literal\n")
	keys := filepath.Join(t.TempDir(), "keys")
	writeTestFile(t, keys, "DB_HOST\nMISSING_ONE\n")

	tests := []struct {
		name string
		args []string
		code int
		out  string
	}{
		{"no arguments prints usage and succeeds", []string{}, 0, ""},
		{"check-template finds a secret", []string{"--check-template", dir}, 1, "API_TOKEN: secret-like key must be bare (API_TOKEN={{ .API_TOKEN }}) in the template\n"},
		{"missing keys", []string{"--missing", keys, dir}, 1, "MISSING_ONE\n"},
		{"render needs stages", []string{dir}, 2, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := run(tt.args, strings.NewReader(""), &out, &errOut)
			if code != tt.code || (tt.out != "" && out.String() != tt.out) {
				t.Fatalf("run(%v) = %d, out %q, err %q; want %d, %q", tt.args, code, out.String(), errOut.String(), tt.code, tt.out)
			}
		})
	}
}

func TestMissingKeysReadFromStdin(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "_template.env"), "A=\n")
	var out bytes.Buffer
	code := run([]string{"--missing", "-", dir}, strings.NewReader("A\nB\n"), &out, &bytes.Buffer{})
	if code != 1 || out.String() != "B\n" {
		t.Fatalf("run(--missing -) = %d, %q", code, out.String())
	}
}

func TestVersionFlagPrintsTheBuildVersion(t *testing.T) {
	old := version
	version = "v9.9.9"
	defer func() { version = old }()

	var out bytes.Buffer
	if code := run([]string{"--version"}, strings.NewReader(""), &out, &bytes.Buffer{}); code != 0 {
		t.Fatalf("run(--version) = %d, want 0", code)
	}
	if out.String() != "fenv v9.9.9\n" {
		t.Fatalf("--version printed %q", out.String())
	}
}

func TestAnUnstampedBuildSaysDev(t *testing.T) {
	if version != "dev" {
		t.Fatalf("default version = %q, want dev (release builds stamp it with -ldflags)", version)
	}
}

func TestStageLinesWithExportKeepTheirValue(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "_template.env"), "A={{ .A }}\nB={{ df .B \"d\" }}\n")
	writeTestFile(t, filepath.Join(dir, "prod.env"), "export A=kept\nexport  B = also\n")

	var out bytes.Buffer
	if err := renderStage(dir, "prod", true, &out); err != nil {
		t.Fatalf("renderStage() error = %v", err)
	}
	// `export KEY=` is the same key as `KEY=`: nothing is removed or changed.
	if want := filepath.Join(dir, "prod.env") + ": +0 -0 ~0 (dry run)\n"; out.String() != want {
		t.Fatalf("report = %q, want %q", out.String(), want)
	}
	if err := renderStage(dir, "prod", false, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "prod.env")); string(got) != "A=kept\nB=also\n" {
		t.Fatalf("rendered = %q", got)
	}
}

func TestLinesThatAreNotKeyValuePairsAreSkipped(t *testing.T) {
	got, _ := parseEnv("export\nmy-key=1\n9X=2\nOK=3\n")
	if !reflect.DeepEqual(got, map[string]string{"OK": "3"}) {
		t.Fatalf("parseEnv() = %#v", got)
	}
}
