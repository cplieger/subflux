// Package searchtest is test support for the search engine: a scripted
// timing-sync aligner the engine tests pass as search.Deps.Aligner.
package searchtest

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"sync"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/syncsched"
	"github.com/cplieger/subflux/internal/timing"
)

// Result is one scripted Align answer.
type Result struct {
	Err     error
	Aligned syncsched.Aligned
}

// ErrScriptEnded is Align's answer once every scripted Result is spent.
var ErrScriptEnded = errors.New("searchtest: no scripted Align answer left")

// Aligner answers each Align call with the Result scripted for a member
// its payload holds, else the next scripted Result in order, and records
// every call; it is safe for concurrent use.
type Aligner struct {
	members []member
	results []Result
	calls   []call
	mu      sync.Mutex
}

// call is one Align call as the aligner received it.
type call struct {
	payload  []byte
	decision timing.Decision
}

type member struct {
	text   []byte
	result Result
}

// NewAligner scripts the answers, first call first.
func NewAligner(results ...Result) *Aligner { return &Aligner{results: results} }

// Member answers r to every payload holding text, whatever the order: the
// first such rule set wins.
func (a *Aligner) Member(text string, r Result) *Aligner { //nolint:gocritic // hugeParam: a script is written inline, by value
	a.mu.Lock()
	defer a.mu.Unlock()
	a.members = append(a.members, member{text: []byte(text), result: r})
	return a
}

// Align records the call and returns its member's Result, else the next
// scripted one, else ErrScriptEnded.
func (a *Aligner) Align(_ context.Context, _ statedb.InstanceID, payload []byte, _ language.Tag, d timing.Decision) (syncsched.Aligned, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls = append(a.calls, call{payload: slices.Clone(payload), decision: d})
	for i := range a.members {
		if m := &a.members[i]; bytes.Contains(payload, m.text) {
			return m.result.Aligned, m.result.Err
		}
	}
	if len(a.results) == 0 {
		return syncsched.Aligned{}, ErrScriptEnded
	}
	r := a.results[0]
	a.results = a.results[1:]
	return r.Aligned, r.Err
}

// Payloads are the payloads Align was asked to align, in order.
func (a *Aligner) Payloads() [][]byte {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([][]byte, len(a.calls))
	for i := range a.calls {
		out[i] = slices.Clone(a.calls[i].payload)
	}
	return out
}

// Decisions are the plans Align was asked to align with, in order.
func (a *Aligner) Decisions() []timing.Decision {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]timing.Decision, len(a.calls))
	for i := range a.calls {
		out[i] = a.calls[i].decision
	}
	return out
}
