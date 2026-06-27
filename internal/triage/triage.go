// Package triage is the LLM-free routing front-half ported from the shell
// ai-assist-triage: compute the cache keys, apply the cache-disable guard, look
// the request up, and decide HIT (serve the cached entry) vs ESCALATE (the
// authoring step — deferred to stage 4b).
//
// The cheap-model classify step (command/answer/escalate sentinels) is NOT here:
// that needs an LLM and lands in stage 4b. This package is the deterministic,
// unit-tested router that decides whether a cached playbook can be served.
package triage

import (
	"strings"

	"github.com/Townk/ai-playbook/internal/cache"
	"github.com/Townk/ai-playbook/internal/capture"
)

// Outcome enumerates the router's decision.
type Outcome int

const (
	// Escalate means there is no usable cache entry: author a fresh playbook
	// (stage 4b). This is the safe default for every miss / disabled-cache case.
	Escalate Outcome = iota
	// Hit means a cached entry exists and should be served.
	Hit
)

func (o Outcome) String() string {
	switch o {
	case Hit:
		return "hit"
	case Escalate:
		return "escalate"
	default:
		return "unknown"
	}
}

// Decision is the router's result. On a Hit, Path is the cache entry file and
// CtxHash/ReqHash are the computed keys; on Escalate, Path is empty.
type Decision struct {
	Outcome  Outcome
	Path     string // cache entry .md path (Hit only)
	CtxHash  string // computed context hash ("" if cache disabled)
	ReqHash  string // computed request hash ("" if cache disabled)
	Disabled bool   // true when the cache-disable guard fired
	Reason   string // human-readable note (for logging/debug)
}

// Cache is the lookup surface triage needs (satisfied by *cache.Cache). It is an
// interface so Route is testable without touching disk when desired.
type Cache interface {
	Lookup(ctx, req string) (string, bool)
}

// Route is the deterministic LLM-free router. It computes the cache keys from
// req, applies the cache-disable guard, and looks the entry up.
//
// Cache-disable guard (faithful to the shell): a FAILED command (exit != 0)
// whose scrollback we could NOT capture has an unreliable key — it collapses to
// project+command+exit, so two genuinely different errors of the same command
// would collide and replay the wrong playbook. In that case the cache is
// DISABLED for this request: keys are cleared, never looked up, never stored, so
// the request always escalates (authors fresh).
//
// noCache forces a miss (the AI_PLAYBOOK_NO_CACHE bypass for regenerate/testing).
func Route(req capture.Request, c Cache, noCache bool) Decision {
	cr := cache.Request{
		ProjectRoot: req.ProjectRoot,
		CWD:         req.CWD,
		CommandText: req.Command,
		CommandExit: req.Exit,
		Scrollback:  req.Scrollback,
	}

	// Cache-disable guard: failure with empty/whitespace-only scrollback.
	if req.Exit != "" && req.Exit != "0" && strings.TrimSpace(req.Scrollback) == "" {
		return Decision{
			Outcome:  Escalate,
			Disabled: true,
			Reason:   "failure with empty scrollback — unreliable cache key, cache disabled",
		}
	}

	ctx := cache.ContextHash(cr)
	reqHash := cache.RequestHash(req.UserRequest)

	if noCache {
		return Decision{Outcome: Escalate, CtxHash: ctx, ReqHash: reqHash, Reason: "cache bypassed (no-cache)"}
	}

	if c != nil {
		if path, ok := c.Lookup(ctx, reqHash); ok {
			return Decision{Outcome: Hit, Path: path, CtxHash: ctx, ReqHash: reqHash, Reason: "cache hit"}
		}
	}
	return Decision{Outcome: Escalate, CtxHash: ctx, ReqHash: reqHash, Reason: "cache miss"}
}
