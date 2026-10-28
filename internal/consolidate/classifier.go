package consolidate

import (
	"context"

	"github.com/mattyv/nas-search-mcp/internal/jev"
	"github.com/mattyv/nas-search-mcp/internal/memoryapi"
	"github.com/mattyv/nas-search-mcp/internal/policy"
)

// Classifier scores how likely an event states something the owner decided or confirmed
// and suggests a memory category. perMille is 0..1000.
type Classifier interface {
	Classify(ctx context.Context, e memoryapi.Event) (category string, perMille int, model string, err error)
}

type jevClassifier struct{ c *jev.Client }

// NewJevClassifier classifies with TypeSafe's Jev. It sends only the event's type, title,
// body and project; sources and IDs stay local.
func NewJevClassifier(c *jev.Client) Classifier { return &jevClassifier{c: c} }

func (j *jevClassifier) Classify(ctx context.Context, e memoryapi.Event) (string, int, string, error) {
	state := map[string]string{"type": e.Type, "title": e.Title, "body": e.Body, "project": e.Project}
	questions := map[string]jev.Question{
		"decided": {
			Type:         "noul",
			Instructions: "Does this entry record something the owner decided, confirmed, or stated as fact about themselves or their work, rather than a suggestion, idea, or assistant speculation?",
			Criteria: map[string]string{
				"true":  "The owner decided, confirmed, or stated this as fact.",
				"false": "It is a suggestion, idea, or assistant speculation.",
			},
		},
		"category": {
			Type:         "choice",
			Instructions: "Which kind of lasting memory does this entry best describe?",
			Criteria: map[string]string{
				"fact":       "A stable fact about the owner, their environment, or their work.",
				"preference": "A way the owner likes things done.",
				"decision":   "A choice the owner made between options.",
				"constraint": "A limit or requirement that must be respected.",
				"lesson":     "Something learned from experience or a mistake.",
			},
		},
	}
	answers, model, err := j.c.DecideWithModel(ctx, state, questions)
	if err != nil {
		return "", 0, "", err
	}
	category := answers["category"].Choice
	if !policy.ValidMemoryCategory(category) {
		category = policy.DefaultMemoryCategory(e.Type)
	}
	return category, perMille(answers["decided"].Noul), model, nil
}
