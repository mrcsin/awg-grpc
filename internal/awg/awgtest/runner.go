// Package awgtest holds test doubles and helpers for tests of code that uses package awg.
package awgtest

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/mrcsin/awg-grpc/internal/awg"
)

// Runner is a fake awg.Runner. It answers each call from results queued with Respond, matched
// on name and args, and records every call. It is safe for concurrent use.
type Runner struct {
	mu        sync.Mutex
	responses []response
	calls     []awg.Command
}

type response struct {
	name    string
	args    []string
	results []result
}

type result struct {
	stdout []byte
	err    error
}

var _ awg.Runner = (*Runner)(nil)

// NewRunner returns a fake runner with no queued results.
func NewRunner() *Runner {
	return &Runner{}
}

// Respond queues a result for calls with exactly this name and args. Results for one command
// are returned in the order they were queued; the last one repeats for every later call.
func (r *Runner) Respond(name string, args []string, stdout []byte, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	res := result{stdout: slices.Clone(stdout), err: err}
	for i := range r.responses {
		if r.responses[i].matches(name, args) {
			r.responses[i].results = append(r.responses[i].results, res)
			return
		}
	}
	r.responses = append(r.responses, response{name: name, args: slices.Clone(args), results: []result{res}})
}

// Run records the call and returns the next queued result, or an error when none matches.
func (r *Runner) Run(_ context.Context, cmd awg.Command) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, cloneCommand(cmd))
	for i := range r.responses {
		resp := &r.responses[i]
		if !resp.matches(cmd.Name, cmd.Args) {
			continue
		}
		res := resp.results[0]
		if len(resp.results) > 1 {
			resp.results = resp.results[1:]
		}
		return slices.Clone(res.stdout), res.err
	}
	return nil, fmt.Errorf("awgtest: no response for %s %q", cmd.Name, cmd.Args)
}

// Calls returns a copy of every recorded call in order, extra file contents included.
func (r *Runner) Calls() []awg.Command {
	r.mu.Lock()
	defer r.mu.Unlock()
	calls := make([]awg.Command, len(r.calls))
	for i, c := range r.calls {
		calls[i] = cloneCommand(c)
	}
	return calls
}

func (resp *response) matches(name string, args []string) bool {
	return resp.name == name && slices.Equal(resp.args, args)
}

func cloneCommand(c awg.Command) awg.Command {
	clone := awg.Command{Name: c.Name, Args: slices.Clone(c.Args)}
	if c.ExtraFiles != nil {
		clone.ExtraFiles = make([][]byte, len(c.ExtraFiles))
		for i, f := range c.ExtraFiles {
			clone.ExtraFiles[i] = slices.Clone(f)
		}
	}
	return clone
}
