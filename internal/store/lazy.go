package store

import (
	"context"
	"fmt"

	"github.com/backplane-labs/backplane/pkg/backplane/deps"

	"github.com/backplane-labs/backplane-legal/internal/app/port"
	"github.com/backplane-labs/backplane-legal/internal/domain"
)

// Repository resolves the SDK dependency at call time, after dependency readiness.
// It is immutable; the underlying Store owns the connection and transaction state.
type Repository struct{ dependency deps.Dependency[*Store] }

var _ port.Repository = Repository{}

func NewRepository(dependency deps.Dependency[*Store]) Repository {
	return Repository{dependency: dependency}
}

func (r Repository) InTx(ctx context.Context, fn func(context.Context) error) error {
	resolved, err := r.resolve()
	if err != nil {
		return err
	}

	return resolved.InTx(ctx, fn)
}

func (r Repository) Lock(ctx context.Context) error {
	resolved, err := r.resolve()
	if err != nil {
		return err
	}

	return resolved.Lock(ctx)
}

func (r Repository) CreateDocument(ctx context.Context, doc domain.Document) error {
	resolved, err := r.resolve()
	if err != nil {
		return err
	}

	return resolved.CreateDocument(ctx, doc)
}

func (r Repository) Documents(ctx context.Context, audience string) ([]domain.Document, error) {
	resolved, err := r.resolve()
	if err != nil {
		return nil, err
	}

	return resolved.Documents(ctx, audience)
}

func (r Repository) Document(ctx context.Context, slug string) (domain.Document, error) {
	resolved, err := r.resolve()
	if err != nil {
		return domain.Document{}, err
	}

	return resolved.Document(ctx, slug)
}

func (r Repository) Versions(ctx context.Context, slug string) ([]domain.Version, error) {
	resolved, err := r.resolve()
	if err != nil {
		return nil, err
	}

	return resolved.Versions(ctx, slug)
}

func (r Repository) SaveVersion(ctx context.Context, version domain.Version) error {
	resolved, err := r.resolve()
	if err != nil {
		return err
	}

	return resolved.SaveVersion(ctx, version)
}

func (r Repository) Records(ctx context.Context, filter domain.Filter) ([]domain.Record, error) {
	resolved, err := r.resolve()
	if err != nil {
		return nil, err
	}

	return resolved.Records(ctx, filter)
}

func (r Repository) Head(ctx context.Context) (int64, string, error) {
	resolved, err := r.resolve()
	if err != nil {
		return 0, "", err
	}

	return resolved.Head(ctx)
}

func (r Repository) Append(ctx context.Context, record domain.Record) error {
	resolved, err := r.resolve()
	if err != nil {
		return err
	}

	return resolved.Append(ctx, record)
}

func (r Repository) Enqueue(ctx context.Context, name string, value any) error {
	resolved, err := r.resolve()
	if err != nil {
		return err
	}

	return resolved.Enqueue(ctx, name, value)
}

func (r Repository) PendingEvents(ctx context.Context, limit int32) ([]domain.Event, error) {
	resolved, err := r.resolve()
	if err != nil {
		return nil, err
	}

	return resolved.PendingEvents(ctx, limit)
}

func (r Repository) AckEvent(ctx context.Context, id int64) error {
	resolved, err := r.resolve()
	if err != nil {
		return err
	}

	return resolved.AckEvent(ctx, id)
}

func (r Repository) LockDocument(ctx context.Context, slug string) error {
	resolved, err := r.resolve()
	if err != nil {
		return err
	}

	return resolved.LockDocument(ctx, slug)
}

func (r Repository) resolve() (*Store, error) {
	value := r.dependency.Get()
	if value == nil {
		return nil, fmt.Errorf("legal store unavailable: %w", deps.ErrNotReady)
	}

	return value, nil
}
