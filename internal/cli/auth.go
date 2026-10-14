package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// promptFunc reads one line for an auth field. When hidden is true and stdin
// is a terminal, the input is read with echo disabled so the password never
// appears on screen.
type promptFunc func(label string, hidden bool) (string, error)

// newPrompter returns a prompt function bound to a command's input/output. A
// single buffered reader is shared across every prompt, so piped input
// (`printf 'user\npass\n' | clacks login`) is consumed in order rather than
// each prompt dropping the previous one's read-ahead.
func newPrompter(in io.Reader, out io.Writer) promptFunc {
	r := bufio.NewReader(in)
	f, _ := in.(*os.File)
	tty := f != nil && term.IsTerminal(int(f.Fd()))

	return func(label string, hidden bool) (string, error) {
		if tty {
			fmt.Fprint(out, label+": ")
			if hidden {
				b, err := term.ReadPassword(int(f.Fd()))
				fmt.Fprintln(out)

				return strings.TrimSpace(string(b)), err
			}
		}
		// Visible terminal line or piped input: read a plain line. When input
		// is piped there is no prompt, so the label is not printed into stdout.
		line, err := r.ReadString('\n')

		return strings.TrimSpace(line), err
	}
}

// resolveCredentials fills missing username/password from positional args,
// then from interactive prompts. Precedence is flags > positional args >
// prompts, matching `docker login` while still allowing a fully non-interactive
// `clacks login USER PASSWORD`.
func resolveCredentials(flagUser, flagPass string, args []string, prompt promptFunc) (string, string, error) {
	user, pass := flagUser, flagPass
	if user == "" && len(args) >= 1 {
		user = args[0]
	}
	if pass == "" && len(args) >= 2 {
		pass = args[1]
	}
	if user == "" {
		v, err := prompt("Username", false)
		if err != nil {
			return "", "", fmt.Errorf("read username: %w", err)
		}
		user = v
	}
	if pass == "" {
		v, err := prompt("Password", true)
		if err != nil {
			return "", "", fmt.Errorf("read password: %w", err)
		}
		pass = v
	}
	if user == "" || pass == "" {
		return "", "", errors.New("username and password required")
	}

	return user, pass, nil
}
