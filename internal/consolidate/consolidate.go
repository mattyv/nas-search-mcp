// Package consolidate turns journal events into memory candidates. Whether an event
// proposes a memory, who may see it, and how a candidate is routed are decided by the
// verified core through internal/policy; this package only moves data.
package consolidate

import (
	"context"
	"log"
	"time"

	"github.com/mattyv/nas-search-mcp/internal/jev"
	"github.com/mattyv/nas-search-mcp/internal/memoryapi"
	"github.com/mattyv/nas-search-mcp/internal/policy"
)

const (
	defaultBatch    = 20
	maxBatch        = 100
	defaultInterval = 2 * time.Minute
	duplicateScan   = 500
)

type Config struct {
	Roots          []string
	ExternalScopes []string // scopes whose entries may be sent to an external classifier
	Interval       time.Duration
	Batch          int
	Classifier     Classifier // nil means no external classification
}

func (c Config) batch() int {
	switch {
	case c.Batch <= 0:
		return defaultBatch
	case c.Batch > maxBatch:
		return maxBatch
	}
	return c.Batch
}

func (c Config) interval() time.Duration {
	if c.Interval <= 0 {
		return defaultInterval
	}
	return c.Interval
}

// Once processes one batch of unclassified events, oldest first, and returns how many
// were fully processed. A retryable classifier failure ends the round early without
// marking the event, so the next round retries it.
func Once(ctx context.Context, store memoryapi.Store, cfg Config) (int, error) {
	events, err := store.UnclassifiedEvents(ctx, cfg.Roots, cfg.batch())
	if err != nil {
		return 0, err
	}
	done := 0
	for _, e := range events {
		if err := ctx.Err(); err != nil {
			return done, err
		}
		if !policy.ProposesMemory(e.Type) {
			if err := store.MarkClassified(ctx, cfg.Roots, e.ID); err != nil {
				return done, err
			}
			done++
			continue
		}
		m := memoryapi.Memory{
			Statement:   buildStatement(e.Title, e.Body),
			Category:    policy.DefaultMemoryCategory(e.Type),
			Scope:       e.Scope,
			Project:     e.Project,
			Evidence:    []string{e.ID},
			Probability: unscored,
		}
		dup, err := isDuplicate(ctx, store, cfg.Roots, m)
		if err != nil {
			return done, err
		}
		if !dup {
			userAttested := e.Actor == memoryapi.ActorUser
			if policy.ExternalClassificationAllowed(cfg.Classifier != nil, userAttested, e.Scope, cfg.ExternalScopes) {
				cat, pm, model, cerr := cfg.Classifier.Classify(ctx, e)
				switch {
				case cerr == nil:
					if policy.ValidMemoryCategory(cat) {
						m.Category = cat
					}
					m.Probability = clampPerMille(pm)
					m.Model = model
				case jev.IsRetryable(cerr):
					log.Printf("consolidate: classifier unavailable, will retry (processed %d)", done)
					return done, nil
				default:
					log.Printf("consolidate: classifier rejected an entry; leaving it for owner review")
				}
			}
			if _, err := store.ProposeMemory(ctx, cfg.Roots, m, userAttested); err != nil {
				return done, err
			}
		}
		if err := store.MarkClassified(ctx, cfg.Roots, e.ID); err != nil {
			return done, err
		}
		done++
	}
	return done, nil
}

func isDuplicate(ctx context.Context, store memoryapi.Store, roots []string, m memoryapi.Memory) (bool, error) {
	existing, err := store.SearchMemories(ctx, roots, memoryapi.MemoryQuery{
		Project:  m.Project,
		Statuses: []string{"accepted", "candidate", "disputed"},
		Limit:    duplicateScan,
	})
	if err != nil {
		return false, err
	}
	want := normalizeStatement(m.Statement)
	for _, x := range existing {
		if normalizeStatement(x.Statement) == want {
			return true, nil
		}
	}
	return false, nil
}

// Run calls Once every cfg.Interval until ctx ends. It logs counts only.
func Run(ctx context.Context, store memoryapi.Store, cfg Config) error {
	t := time.NewTicker(cfg.interval())
	defer t.Stop()
	for {
		n, err := Once(ctx, store, cfg)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			log.Printf("consolidate: round failed after %d entries", n)
		} else if n > 0 {
			log.Printf("consolidate: processed %d entries", n)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}
