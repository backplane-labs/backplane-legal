package store_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/backplane-labs/backplane/pkg/backplane/backplanetest"
	"github.com/backplane-labs/backplane/pkg/backplane/config"
	"github.com/backplane-labs/backplane/pkg/backplane/deps"
	"github.com/backplane-labs/backplane/pkg/backplane/infra/postgres"
	"github.com/google/uuid"

	"github.com/backplane-labs/backplane-legal/internal/app"
	"github.com/backplane-labs/backplane-legal/internal/domain"
	"github.com/backplane-labs/backplane-legal/internal/store"
	"github.com/backplane-labs/backplane-legal/internal/store/db"
)

func TestPostgresEvidence(t *testing.T) {
	t.Parallel()
	// Shared append-only evidence is deliberately not cleaned by truncation. Each run
	// uses fresh opaque identities; tampering tests roll back their entire transaction.
	dsn := os.Getenv("LEGAL_TEST_PG")
	if dsn == "" {
		t.Skip("LEGAL_TEST_PG not set")
	}

	harness := backplanetest.New(t, backplanetest.Name("legal"))
	dependency := deps.NewDependency(harness.Root(), store.New(postgres.Config{DSN: config.Secret(dsn)}))
	harness.Start()

	if err := backplanetest.Ready(harness); err != nil {
		t.Fatal(err)
	}

	repo := dependency.Get()
	service := app.New(repo, "en")

	slug := "consent-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if err := service.CreateDocument(t.Context(), domain.Document{Slug: slug, Audience: domain.AudienceAll}); err != nil {
		t.Fatal(err)
	}

	draft, err := service.SaveDraft(t.Context(), domain.Version{Document: slug, EffectiveAt: time.Now().Add(-time.Hour), Texts: []domain.Text{{Lang: "en", Title: "Consent", Body: "Exact text"}}})
	if err != nil {
		t.Fatal(err)
	}

	published, err := service.Publish(t.Context(), slug, draft.Number)
	if err != nil {
		t.Fatal(err)
	}

	item := domain.Item{Document: slug, Version: published.Number, Lang: "en"}
	subject := "subject-" + uuid.NewString()

	records, err := service.Accept(t.Context(), domain.AcceptInput{Subject: subject, Items: []domain.Item{item}})
	if err != nil {
		t.Fatal(err)
	}

	seq := records[0].Seq
	testIdempotency(t, service, subject, item, seq)
	testConcurrency(t, service, item)
	testAtomicity(t, service, repo, item)
	testOutboxAtomicity(t, service, repo, item)
	testOutbox(t, repo, slug)
	testImmutability(t, repo, slug, published.Number, seq)
	testCorruption(t, service, repo, seq)
	testPublicationLock(t, service, repo, item)

	result, err := service.Verify(t.Context(), 0, 0)
	if err != nil || !result.OK {
		t.Fatal(result, err)
	}
}

func testIdempotency(t *testing.T, service *app.Service, subject string, item domain.Item, seq int64) {
	t.Helper()

	again, err := service.Accept(t.Context(), domain.AcceptInput{Subject: subject, Items: []domain.Item{item}})
	if err != nil || again[0].Seq != seq {
		t.Fatal(again, err)
	}

	withdrawal, err := service.Withdraw(t.Context(), domain.WithdrawInput{Subject: subject, Document: item.Document})
	if err != nil || withdrawal.Kind != domain.Withdrawn {
		t.Fatal(withdrawal, err)
	}

	reaccepted, err := service.Accept(t.Context(), domain.AcceptInput{Subject: subject, Items: []domain.Item{item}})
	if err != nil || reaccepted[0].Seq <= withdrawal.Seq {
		t.Fatal(reaccepted, err)
	}
}

func testConcurrency(t *testing.T, service *app.Service, item domain.Item) {
	t.Helper()

	const workers = 24

	var group sync.WaitGroup
	for i := range workers {
		group.Go(func() {
			input := domain.AcceptInput{Subject: fmt.Sprintf("%s-%d", uuid.NewString(), i), Items: []domain.Item{item}}
			if _, err := service.Accept(t.Context(), input); err != nil {
				t.Error(err)
			}
		})
	}

	group.Wait()

	subject := uuid.NewString()

	for range workers {
		group.Go(func() {
			if _, err := service.Accept(t.Context(), domain.AcceptInput{Subject: subject, Items: []domain.Item{item}}); err != nil {
				t.Error(err)
			}
		})
	}

	group.Wait()

	records, err := service.Acceptances(t.Context(), domain.Filter{Subject: subject})
	if err != nil || len(records) != 1 {
		t.Fatal("concurrent retry appended duplicate", records, err)
	}

	verified, err := service.Verify(t.Context(), 0, 0)
	if err != nil || !verified.OK {
		t.Fatal(verified, err)
	}
}

func testAtomicity(t *testing.T, service *app.Service, repo *store.Store, item domain.Item) {
	t.Helper()

	before, _, err := repo.Head(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	subject := uuid.NewString()

	_, err = service.Accept(t.Context(), domain.AcceptInput{Subject: subject, Items: []domain.Item{item, {Document: "absent-" + uuid.NewString(), Version: 1, Lang: "en"}}})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatal(err)
	}

	after, _, err := repo.Head(t.Context())
	if err != nil || after != before {
		t.Fatal("batch partially committed", before, after, err)
	}

	records, err := service.Acceptances(t.Context(), domain.Filter{Subject: subject})
	if err != nil || len(records) != 0 {
		t.Fatal(records, err)
	}
}

func testOutbox(t *testing.T, repo *store.Store, slug string) {
	t.Helper()

	events, err := repo.PendingEvents(t.Context(), 10000)
	if err != nil {
		t.Fatal(err)
	}

	var found bool

	for _, event := range events {
		if event.Name == "legal.Published" && strings.Contains(string(event.Payload), slug) {
			found = true

			if err := repo.AckEvent(t.Context(), event.ID); err != nil {
				t.Fatal(err)
			}
		}
	}

	if !found {
		t.Fatal("publication has no durable event")
	}
}

func testImmutability(t *testing.T, repo *store.Store, slug string, number, seq int64) {
	t.Helper()

	checks := []func(context.Context) error{
		func(ctx context.Context) error { return repo.Q.TestUpdateRecord(ctx, seq) },
		func(ctx context.Context) error { return repo.Q.TestDeleteRecord(ctx, seq) },
		repo.Q.TestTruncateJournal,
		func(ctx context.Context) error {
			return repo.Q.TestUpdatePublished(ctx, db.TestUpdatePublishedParams{Document: slug, Number: number})
		},
		func(ctx context.Context) error {
			return repo.Q.TestDeletePublished(ctx, db.TestDeletePublishedParams{Document: slug, Number: number})
		},
		repo.Q.TestTruncateVersions,
	}
	for i, check := range checks {
		if err := repo.InTx(t.Context(), check); err == nil {
			t.Fatalf("immutability check %d unexpectedly succeeded", i)
		}
	}
}

func testCorruption(t *testing.T, service *app.Service, repo *store.Store, seq int64) {
	t.Helper()

	head, _, err := repo.Head(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		seq    int64
		mutate func(context.Context, int64) error
	}{
		{seq, repo.Q.TestTamperRecord},
		{seq, repo.Q.TestUpdateRecord},
		{seq, repo.Q.TestDeleteRecord},
		{head, repo.Q.TestDeleteRecord},
	}
	for _, test := range cases {
		rollback := fmt.Errorf("%w: roll back deliberate corruption", domain.ErrConflict)

		err := repo.InTx(t.Context(), func(ctx context.Context) error {
			if err := repo.Lock(ctx); err != nil {
				return err
			}
			if err := repo.Q.TestDisableJournalGuard(ctx); err != nil {
				return err
			}

			if err := test.mutate(ctx, test.seq); err != nil {
				return err
			}

			result, err := service.Verify(ctx, 0, 0)
			if err != nil || result.OK || result.BrokenSeq != test.seq {
				t.Errorf("corruption not detected: %+v %v", result, err)
			}

			return rollback
		})
		if !errors.Is(err, rollback) {
			t.Fatal(err)
		}
	}
}
