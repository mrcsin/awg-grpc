// Package awg drives the AmneziaWG vendor tools and parses their output.
package awg

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Runner executes an external command and returns its stdout.
type Runner interface {
	Run(ctx context.Context, cmd Command) ([]byte, error)
}

// Command is one invocation of an external tool, without a shell.
type Command struct {
	Name string
	Args []string
	// ExtraFiles each become a pipe readable by the child at /dev/fd/(3+i).
	// Every content must fit the pipe buffer: it is written before the child starts.
	ExtraFiles [][]byte
}

// ExitError reports a non-zero exit with stderr redacted of every extra file content.
type ExitError struct {
	Code   int
	Stderr string
}

func (e *ExitError) Error() string {
	return fmt.Sprintf("exit status %d: %s", e.Code, strings.TrimSpace(e.Stderr))
}

// waitDelay bounds Wait after the context ends, when a grandchild still holds stdout or stderr.
const waitDelay = 2 * time.Second

// ExecRunner runs commands with os/exec. It is the production Runner.
type ExecRunner struct{}

var _ Runner = ExecRunner{}

// Run starts the command with one pipe per extra file and waits for it. A non-zero exit
// returns *ExitError; an ended context returns the context error.
func (ExecRunner) Run(ctx context.Context, c Command) ([]byte, error) {
	readEnds, err := openPipes(c.ExtraFiles)
	if err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, c.Name, c.Args...)
	cmd.ExtraFiles = readEnds
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.WaitDelay = waitDelay

	err = cmd.Start()
	closeAll(readEnds)
	if err != nil {
		return nil, fmt.Errorf("starting %s: %w", c.Name, err)
	}
	err = cmd.Wait()
	if err != nil && ctx.Err() != nil {
		return nil, fmt.Errorf("running %s: %w", c.Name, ctx.Err())
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return nil, fmt.Errorf("running %s: %w", c.Name, &ExitError{
			Code:   exitErr.ExitCode(),
			Stderr: redact(stderr.String(), c.ExtraFiles),
		})
	}
	if err != nil {
		return nil, fmt.Errorf("running %s: %w", c.Name, err)
	}
	return stdout.Bytes(), nil
}

// openPipes returns the read ends of pipes that already hold each content, write ends closed.
func openPipes(contents [][]byte) ([]*os.File, error) {
	readEnds := make([]*os.File, 0, len(contents))
	for i, content := range contents {
		r, w, err := os.Pipe()
		if err != nil {
			closeAll(readEnds)
			return nil, fmt.Errorf("creating pipe for extra file %d: %w", i, err)
		}
		readEnds = append(readEnds, r)
		_, writeErr := w.Write(content)
		closeErr := w.Close()
		if err := errors.Join(writeErr, closeErr); err != nil {
			closeAll(readEnds)
			return nil, fmt.Errorf("filling pipe for extra file %d: %w", i, err)
		}
	}
	return readEnds, nil
}

func closeAll(files []*os.File) {
	for _, f := range files {
		_ = f.Close()
	}
}

func redact(stderr string, contents [][]byte) string {
	for _, content := range contents {
		secret := strings.TrimSpace(string(content))
		if secret == "" {
			continue
		}
		stderr = strings.ReplaceAll(stderr, secret, redacted)
	}
	return stderr
}
