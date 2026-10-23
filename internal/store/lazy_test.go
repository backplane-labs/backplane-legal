package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/backplane-labs/backplane/pkg/backplane/deps"

	"github.com/backplane-labs/backplane-legal/internal/domain"
	"github.com/backplane-labs/backplane-legal/internal/store"
)

func TestUnavailableRepository(t *testing.T) {
	t.Parallel()

	repo := store.NewRepository(deps.Dependency[*store.Store]{})
	checks := []func(context.Context) error{
		func(ctx context.Context) error {
			return repo.InTx(ctx, func(context.Context) error { t.Error("unavailable transaction ran"); return nil })
		},
		repo.Lock,
		func(ctx context.Context) error { return repo.LockDocument(ctx, "privacy") },
		func(ctx context.Context) error { return repo.CreateDocument(ctx, domain.Document{}) },
		func(ctx context.Context) error { _, err := repo.Document(ctx, "privacy"); return err },
		func(ctx context.Context) error { _, err := repo.Documents(ctx, ""); return err },
		func(ctx context.Context) error { _, err := repo.Versions(ctx, "privacy"); return err },
		func(ctx context.Context) error { return repo.SaveVersion(ctx, domain.Version{}) },
		func(ctx context.Context) error { _, err := repo.Records(ctx, domain.Filter{}); return err },
		func(ctx context.Context) error { _, _, err := repo.Head(ctx); return err },
		func(ctx context.Context) error { return repo.Append(ctx, domain.Record{}) },
		func(ctx context.Context) error { return repo.Enqueue(ctx, "event", domain.Record{}) },
		func(ctx context.Context) error { _, err := repo.PendingEvents(ctx, 1); return err },
		func(ctx context.Context) error { return repo.AckEvent(ctx, 1) },
	}
	for i, check := range checks {
		if err := check(t.Context()); !errors.Is(err, deps.ErrNotReady) {
			t.Errorf("method %d returned %v", i, err)
		}
	}
}
