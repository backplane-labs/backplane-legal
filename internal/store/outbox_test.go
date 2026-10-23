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

type failingOutbox struct{ *store.Store }

func (*failingOutbox) Enqueue(context.Context, string, any) error { return domain.ErrConflict }

func testOutboxAtomicity(t *testing.T, service *app.Service, repo *store.Store, item domain.Item) {
	t.Helper()

	failing := app.New(&failingOutbox{Store: repo}, "en")
	subject := uuid.NewString()

	before, _, err := repo.Head(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if _, err = failing.Accept(t.Context(), domain.AcceptInput{Subject: subject, Items: []domain.Item{item}}); !errors.Is(err, domain.ErrConflict) {
		t.Fatal(err)
	}

	after, _, err := repo.Head(t.Context())
	if err != nil || before != after {
		t.Fatal("outbox failure committed chain head", before, after, err)
	}

	records, err := service.Acceptances(t.Context(), domain.Filter{Subject: subject})
	if err != nil || len(records) != 0 {
		t.Fatal("outbox failure committed evidence", records, err)
	}

	draft, err := service.SaveDraft(t.Context(), domain.Version{Document: item.Document, EffectiveAt: time.Now().Add(-time.Hour), Texts: []domain.Text{{Lang: "en", Title: "Draft", Body: "Not published"}}})
	if err != nil {
		t.Fatal(err)
	}

	if _, err = failing.Publish(t.Context(), draft.Document, draft.Number); !errors.Is(err, domain.ErrConflict) {
		t.Fatal(err)
	}

	versions, err := service.Versions(t.Context(), item.Document)
	if err != nil {
		t.Fatal(err)
	}

	for _, version := range versions {
		if version.Number == draft.Number && version.Status != domain.Draft {
			t.Fatal("outbox failure committed publication", version)
		}
	}
}
