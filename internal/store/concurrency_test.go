package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/backplane-labs/backplane-legal/internal/app"
	"github.com/backplane-labs/backplane-legal/internal/domain"
	"github.com/backplane-labs/backplane-legal/internal/store"
)

// pausedAcceptance stops after current-version validation, while its transaction
// still owns the document lock. Publishing must not pass this checkpoint.
type pausedAcceptance struct {
	*store.Store
	reached chan struct{}
	release <-chan struct{}
}

func (p *pausedAcceptance) Records(ctx context.Context, filter domain.Filter) ([]domain.Record, error) {
	records, err := p.Store.Records(ctx, filter)
	if err != nil {
		return nil, err
	}

	close(p.reached)

	select {
	case <-p.release:
		return records, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func testPublicationLock(t *testing.T, service *app.Service, repo *store.Store, item domain.Item) {
	t.Helper()

	draft, err := service.SaveDraft(t.Context(), domain.Version{Document: item.Document, EffectiveAt: time.Now().Add(-time.Hour), Texts: []domain.Text{{Lang: "en", Title: "Updated", Body: "Changed terms"}}})
	if err != nil {
		t.Fatal(err)
	}

	reached := make(chan struct{})
	release := make(chan struct{})
	accepting := app.New(&pausedAcceptance{Store: repo, reached: reached, release: release}, "en")
	accepted := make(chan error, 1)

	go func() {
		_, acceptErr := accepting.Accept(t.Context(), domain.AcceptInput{Subject: uuid.NewString(), Items: []domain.Item{item}})
		accepted <- acceptErr
	}()

	select {
	case <-reached:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("acceptance did not reach locked checkpoint")
	}

	err = repo.InTx(t.Context(), func(ctx context.Context) error {
		if err := repo.Q.TestSetLockTimeout(ctx); err != nil {
			return err
		}

		_, err := service.Publish(ctx, item.Document, draft.Number)

		return err
	})

	close(release)

	if err == nil {
		t.Error("publication bypassed acceptance's current-version lock")
	}

	select {
	case err = <-accepted:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("acceptance did not finish")
	}

	if _, err = service.Publish(t.Context(), item.Document, draft.Number); err != nil {
		t.Fatal(err)
	}

	if _, err = service.Accept(t.Context(), domain.AcceptInput{Subject: uuid.NewString(), Items: []domain.Item{item}}); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("stale version accepted after publication", err)
	}
}
