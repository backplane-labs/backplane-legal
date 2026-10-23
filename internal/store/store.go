// Package store provides PostgreSQL persistence and a durable legal event outbox.
package store

import (
	"context"
	"embed"
	"errors"
	"fmt"

	"github.com/backplane-labs/backplane/pkg/backplane/deps"
	"github.com/backplane-labs/backplane/pkg/backplane/infra/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/gopherex/pgtx/pkg/tx"

	"github.com/backplane-labs/backplane-legal/internal/domain"
	"github.com/backplane-labs/backplane-legal/internal/store/db"
)

const Schema = "legal"

//go:embed migrations/*.sql
var migrations embed.FS

// Store holds the mutable SDK connection and context-aware typed queries.
type Store struct {
	*postgres.DB
	Q *db.Queries
}

// New registers the SDK PostgreSQL provider with legal's generated migrations.
func New(cfg postgres.Config) deps.Provider[*Store] {
	return provider{inner: postgres.New(cfg, postgres.Schema(Schema), postgres.Migrations(migrations))}
}

type provider struct{ inner deps.Provider[*postgres.DB] }

func (provider) Name() string { return "store" }

func (p provider) Provide(ctx context.Context, scope deps.Scope) (*Store, error) {
	pg, err := p.inner.Provide(ctx, scope)
	if err != nil {
		return nil, fmt.Errorf("provide legal store: %w", err)
	}

	return &Store{DB: pg, Q: db.New(pg.DB)}, nil
}

func (p provider) Probe(ctx context.Context, store *Store) error {
	return p.inner.Probe(ctx, store.DB) //nolint:wrapcheck // SDK driver error retains its own context.
}

func (p provider) Close(ctx context.Context, store *Store) error {
	return p.inner.Close(ctx, store.DB) //nolint:wrapcheck // SDK driver error retains its own context.
}

func (s *Store) InTx(ctx context.Context, fn func(context.Context) error) error {
	return tx.DoReadCommitted(ctx, s.Trm, fn) //nolint:wrapcheck // Preserve application and transaction error identities.
}

func (s *Store) Lock(ctx context.Context) error {
	if err := s.Q.EnsureHead(ctx); err != nil {
		return translate(err)
	}

	_, err := s.Q.LockHead(ctx)

	return translate(err)
}

func translate(err error) error {
	if err == nil {
		return nil
	}

	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: %w", domain.ErrNotFound, err)
	}

	var pgerr *pgconn.PgError
	if errors.As(err, &pgerr) {
		switch pgerr.Code {
		case "23505", "23514":
			return fmt.Errorf("%w: %w", domain.ErrConflict, err)
		case "23503":
			return fmt.Errorf("%w: %w", domain.ErrNotFound, err)
		}
	}

	return fmt.Errorf("legal store: %w", err)
}
