package cli

import (
	"io"
	"strings"
	"testing"
)

// fakePrompt records prompt calls and returns canned values.
type fakePrompt struct {
	calls []struct {
		label  string
		hidden bool
	}
	vals []string
}

func (f *fakePrompt) prompt(label string, hidden bool) (string, error) {
	f.calls = append(f.calls, struct {
		label  string
		hidden bool
	}{label, hidden})
	i := len(f.calls) - 1
	if i < len(f.vals) {
		return f.vals[i], nil
	}

	return "", nil
}

func TestResolveCredentialsFlags(t *testing.T) {
	p := &fakePrompt{}
	u, pw, err := resolveCredentials("alice", "s3cret", nil, p.prompt)
	if err != nil {
		t.Fatal(err)
	}
	if u != "alice" || pw != "s3cret" {
		t.Fatalf("got %q/%q", u, pw)
	}
	if len(p.calls) != 0 {
		t.Fatalf("prompted %d times, want 0", len(p.calls))
	}
}

func TestResolveCredentialsPositional(t *testing.T) {
	p := &fakePrompt{}
	u, pw, err := resolveCredentials("", "", []string{"alice", "s3cret"}, p.prompt)
	if err != nil {
		t.Fatal(err)
	}
	if u != "alice" || pw != "s3cret" {
		t.Fatalf("got %q/%q", u, pw)
	}
	if len(p.calls) != 0 {
		t.Fatalf("prompted %d times, want 0", len(p.calls))
	}
}

func TestResolveCredentialsFlagsWinOverArgs(t *testing.T) {
	p := &fakePrompt{}
	u, pw, err := resolveCredentials("alice", "", []string{"bob", "s3cret"}, p.prompt)
	if err != nil {
		t.Fatal(err)
	}
	// username from flag, password from second positional arg.
	if u != "alice" || pw != "s3cret" {
		t.Fatalf("got %q/%q", u, pw)
	}
	if len(p.calls) != 0 {
		t.Fatalf("prompted %d times, want 0", len(p.calls))
	}
}

func TestResolveCredentialsPromptsMissing(t *testing.T) {
	p := &fakePrompt{vals: []string{"s3cret"}}
	u, pw, err := resolveCredentials("alice", "", nil, p.prompt)
	if err != nil {
		t.Fatal(err)
	}
	if u != "alice" || pw != "s3cret" {
		t.Fatalf("got %q/%q", u, pw)
	}
	// Only the password was missing; the password prompt must be hidden.
	if len(p.calls) != 1 {
		t.Fatalf("prompted %d times, want 1", len(p.calls))
	}
	if !p.calls[0].hidden || p.calls[0].label != "Password" {
		t.Fatalf("prompt = %+v, want hidden Password prompt", p.calls[0])
	}
}

func TestResolveCredentialsRejectsEmpty(t *testing.T) {
	p := &fakePrompt{}
	_, _, err := resolveCredentials("", "", nil, p.prompt)
	if err == nil || !strings.Contains(err.Error(), "username and password required") {
		t.Fatalf("err = %v, want username and password required", err)
	}
}

// TestPrompterPipedInput verifies piped input is read in order for both a
// visible and a hidden field, without the second prompt losing the first
// prompt's read-ahead.
func TestPrompterPipedInput(t *testing.T) {
	p := newPrompter(strings.NewReader("alice\ns3cret\n"), io.Discard)
	u, err := p("Username", false)
	if err != nil {
		t.Fatal(err)
	}
	pw, err := p("Password", true)
	if err != nil {
		t.Fatal(err)
	}
	if u != "alice" || pw != "s3cret" {
		t.Fatalf("got %q/%q", u, pw)
	}
}
