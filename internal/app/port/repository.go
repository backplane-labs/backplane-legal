// Package port defines the transactional storage contract used by legal scenarios.
package port

import (
	"context"

	"github.com/backplane-labs/backplane-legal/internal/domain"
)

// Repository serializes mutations with a single durable chain-head lock.
type Repository interface {
	Transactions
	Documents
	Journal
}

// Transactions owns the boundary and serializes state-dependent mutations.
type Transactions interface {
	InTx(ctx context.Context, fn func(context.Context) error) error
	Lock(ctx context.Context) error
}

// Documents persists document metadata and draft/published versions.
type Documents interface {
	LockDocument(ctx context.Context, slug string) error
	CreateDocument(ctx context.Context, doc domain.Document) error
	Documents(ctx context.Context, audience string) ([]domain.Document, error)
	Document(ctx context.Context, slug string) (domain.Document, error)
	Versions(ctx context.Context, slug string) ([]domain.Version, error)
	SaveVersion(ctx context.Context, version domain.Version) error
}

// Journal appends evidence and delivery records in the caller's transaction.
type Journal interface {
	Records(ctx context.Context, filter domain.Filter) ([]domain.Record, error)
	Head(ctx context.Context) (int64, string, error)
	Append(ctx context.Context, record domain.Record) error
	Enqueue(ctx context.Context, name string, payload any) error
}
