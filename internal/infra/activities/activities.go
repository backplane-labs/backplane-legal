// Package activities exposes legal capabilities to Backplane wiring.
package activities

import (
	"context"
	"errors"
	"maps"
	"time"

	"github.com/backplane-labs/backplane/pkg/backplane/activity"
	"github.com/backplane-labs/backplane/pkg/backplane/deps"
	"github.com/backplane-labs/backplane/pkg/backplane/meta"

	"github.com/backplane-labs/backplane-legal/internal/domain"
)

// Cases is the application port consumed by activities.
type Cases interface {
	Pending(ctx context.Context, subject, audience, lang string) ([]domain.View, error)
	Accept(ctx context.Context, in domain.AcceptInput) ([]domain.Record, error)
	Withdraw(ctx context.Context, in domain.WithdrawInput) (domain.Record, error)
	GetDocument(ctx context.Context, slug string, version int64, lang string) (domain.View, error)
	ListDocuments(ctx context.Context, audience, lang string) ([]domain.View, error)
	Acceptances(ctx context.Context, filter domain.Filter) ([]domain.Record, error)
	Verify(ctx context.Context, from, to int64) (domain.Verification, error)
}

// PendingInput selects a subject's current required and optional choices.
type PendingInput struct {
	Subject  string `json:"subject"  schemapb:"description=Opaque product subject"`
	Audience string `json:"audience" schemapb:"description=all or registration"`
	Lang     string `json:"lang"     schemapb:"description=Requested BCP 47 language"`
}

// DocumentsOutput is a stable wrapper for document results.
type (
	DocumentsOutput struct {
		Documents []domain.View `json:"documents"`
	}
	// RecordsOutput contains the exact committed evidence entries.
	RecordsOutput struct {
		Records []domain.Record `json:"records"`
	}
	// GetInput selects an effective publication, zero means current.
	GetInput struct {
		Slug    string `json:"slug"`
		Version int64  `json:"version,omitempty"`
		Lang    string `json:"lang,omitempty"`
	}
)

// ListInput selects current documents by audience.
type ListInput struct {
	Audience string `json:"audience,omitempty"`
	Lang     string `json:"lang,omitempty"`
}

// SubjectInput selects an opaque subject's complete history.
type (
	SubjectInput struct {
		Subject string `json:"subject" schemapb:"description=Opaque product subject"`
	}
	// VerifyInput optionally bounds the journal verification by sequence.
	VerifyInput struct {
		From int64 `json:"from,omitempty"`
		To   int64 `json:"to,omitempty"`
	}
)

// Activities is a component whose dependencies are supplied at composition time.
type Activities struct {
	deps.Component
	cases Cases
}

// New registers the legal activity contract.
func New(parent deps.Scope, cases Cases) *Activities {
	a := &Activities{Component: deps.NewComponent(parent, "legal"), cases: cases}

	const timeout = 30 * time.Second
	activity.Handle(a, "Pending", a.Pending,
		activity.StartToClose(timeout), activity.Describe("returns current documents awaiting a subject's choice"),
	)
	activity.Handle(a, "Accept", a.Accept,
		activity.StartToClose(timeout), activity.Describe("appends idempotent legal acceptance evidence"),
	)
	activity.Handle(a, "Withdraw", a.Withdraw,
		activity.StartToClose(timeout), activity.Describe("appends withdrawal of an optional consent"),
	)
	activity.Handle(a, "GetDocument", a.GetDocument,
		activity.StartToClose(timeout), activity.Describe("reads an effective published legal text"),
	)
	activity.Handle(a, "ListDocuments", a.ListDocuments,
		activity.StartToClose(timeout), activity.Describe("lists current legal documents"),
	)
	activity.Handle(a, "Acceptances", a.Acceptances,
		activity.StartToClose(timeout), activity.Describe("reads a subject's acceptance and withdrawal history"),
	)
	activity.Handle(a, "Verify", a.Verify,
		activity.StartToClose(timeout), activity.Describe("verifies evidence hashes and journal continuity"),
	)

	return a
}

// Pending is legal.Pending.
func (a *Activities) Pending(ctx context.Context, in PendingInput) (DocumentsOutput, error) {
	out, err := a.cases.Pending(ctx, in.Subject, in.Audience, in.Lang)
	return DocumentsOutput{Documents: out}, classify(err)
}

// Accept is legal.Accept; explicit context overrides propagated call metadata.
func (a *Activities) Accept(ctx context.Context, in domain.AcceptInput) (RecordsOutput, error) {
	in.Context = metadata(ctx, in.Context)
	out, err := a.cases.Accept(ctx, in)

	return RecordsOutput{Records: out}, classify(err)
}

// Withdraw is legal.Withdraw.
func (a *Activities) Withdraw(ctx context.Context, in domain.WithdrawInput) (domain.Record, error) {
	in.Context = metadata(ctx, in.Context)
	out, err := a.cases.Withdraw(ctx, in)

	return out, classify(err)
}

// GetDocument is legal.GetDocument.
func (a *Activities) GetDocument(ctx context.Context, in GetInput) (domain.View, error) {
	out, err := a.cases.GetDocument(ctx, in.Slug, in.Version, in.Lang)
	return out, classify(err)
}

// ListDocuments is legal.ListDocuments.
func (a *Activities) ListDocuments(ctx context.Context, in ListInput) (DocumentsOutput, error) {
	out, err := a.cases.ListDocuments(ctx, in.Audience, in.Lang)
	return DocumentsOutput{Documents: out}, classify(err)
}

// Acceptances is legal.Acceptances.
func (a *Activities) Acceptances(ctx context.Context, in SubjectInput) (RecordsOutput, error) {
	if in.Subject == "" {
		return RecordsOutput{}, classify(domain.ErrInvalid)
	}

	out, err := a.cases.Acceptances(ctx, domain.Filter{Subject: in.Subject})

	return RecordsOutput{Records: out}, classify(err)
}

// Verify is legal.Verify.
func (a *Activities) Verify(ctx context.Context, in VerifyInput) (domain.Verification, error) {
	out, err := a.cases.Verify(ctx, in.From, in.To)
	return out, classify(err)
}

func classify(err error) error {
	if errors.Is(err, domain.ErrInvalid) || errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrConflict) {
		return activity.NonRetryable(err) //nolint:wrapcheck // Preserve the SDK non-retryable error marker.
	}

	return err
}

func metadata(ctx context.Context, explicit map[string]string) map[string]string {
	out := meta.From(ctx)
	if out == nil {
		out = make(map[string]string, len(explicit))
	}

	maps.Copy(out, explicit)

	return out
}
