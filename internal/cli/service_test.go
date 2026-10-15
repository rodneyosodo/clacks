package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderService(t *testing.T) {
	got := renderService("/usr/local/bin/clacks")
	if !strings.Contains(got, "ExecStart=/usr/local/bin/clacks daemon\n") {
		t.Fatalf("ExecStart missing or wrong:\n%s", got)
	}
	if strings.Contains(got, "{{BIN}}") {
		t.Fatalf("placeholder not replaced:\n%s", got)
	}
}

func TestRenderServiceQuotesSpaces(t *testing.T) {
	got := renderService("/usr/local/bin/my clacks")
	if !strings.Contains(got, `ExecStart="/usr/local/bin/my clacks" daemon`) {
		t.Fatalf("path with spaces not quoted:\n%s", got)
	}
}

func TestSystemdEscape(t *testing.T) {
	cases := map[string]string{
		"/usr/bin/clacks": "/usr/bin/clacks",
		"/a b/clacks":     `"/a b/clacks"`,
		`/a"b/clacks`:     `"/a\"b/clacks"`,
	}
	for in, want := range cases {
		if got := systemdEscape(in); got != want {
			t.Errorf("systemdEscape(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWriteUnitAndPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	p, err := writeUnit(renderService("/usr/bin/clacks"))
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "systemd", "user", "clacks.service")
	if p != want {
		t.Fatalf("path = %q, want %q", p, want)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "ExecStart=/usr/bin/clacks daemon") {
		t.Fatalf("unexpected content:\n%s", data)
	}
}
