package app_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/backplane-labs/backplane-legal/internal/app"
	"github.com/backplane-labs/backplane-legal/internal/domain"
)

type memory struct {
	mu         sync.Mutex
	docs       map[string]domain.Document
	versions   map[string][]domain.Version
	records    []domain.Record
	events     []string
	failEvents bool
}

func newMemory() *memory {
	return &memory{docs: map[string]domain.Document{}, versions: map[string][]domain.Version{}}
}

func (m *memory) InTx(ctx context.Context, fn func(context.Context) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	records, events := slices.Clone(m.records), slices.Clone(m.events)
	if err := fn(ctx); err != nil {
		m.records = records
		m.events = events

		return err
	}

	return nil
}
func (*memory) Lock(context.Context) error { return nil }
func (m *memory) CreateDocument(_ context.Context, doc domain.Document) error {
	if _, ok := m.docs[doc.Slug]; ok {
		return domain.ErrConflict
	}

	m.docs[doc.Slug] = doc

	return nil
}

func (m *memory) Document(_ context.Context, slug string) (domain.Document, error) {
	doc, ok := m.docs[slug]
	if !ok {
		return doc, domain.ErrNotFound
	}

	return doc, nil
}

func (m *memory) Documents(_ context.Context, audience string) ([]domain.Document, error) {
	docs := []domain.Document{}
	for _, doc := range m.docs {
		if audience == "" || doc.Audience == audience {
			docs = append(docs, doc)
		}
	}

	return docs, nil
}

func (m *memory) Versions(_ context.Context, slug string) ([]domain.Version, error) {
	return slices.Clone(m.versions[slug]), nil
}

func (m *memory) SaveVersion(_ context.Context, version domain.Version) error {
	for i, old := range m.versions[version.Document] {
		if old.Number == version.Number {
			m.versions[version.Document][i] = version
			return nil
		}
	}

	m.versions[version.Document] = append(m.versions[version.Document], version)

	return nil
}

func (m *memory) Records(_ context.Context, filter domain.Filter) ([]domain.Record, error) {
	records := []domain.Record{}
	for i := range m.records {
		record := m.records[i]
		if (filter.Subject == "" || record.Subject == filter.Subject) && (filter.Document == "" || record.Document == filter.Document) && (filter.Version == 0 || record.Version == filter.Version) {
			records = append(records, record)
		}
	}

	return records, nil
}

func (m *memory) Head(context.Context) (int64, string, error) {
	if len(m.records) == 0 {
		return 0, "", nil
	}

	last := m.records[len(m.records)-1]

	return last.Seq, last.Hash, nil
}

func (m *memory) Append(_ context.Context, record domain.Record) error {
	m.records = append(m.records, record)
	return nil
}

func (m *memory) Enqueue(_ context.Context, event string, _ any) error {
	if m.failEvents {
		return domain.ErrConflict
	}

	m.events = append(m.events, event)

	return nil
}

func fixture(t *testing.T, required bool) (*app.Service, *memory, domain.Version) {
	t.Helper()

	repo := newMemory()

	service := app.New(repo, "en")
	if err := service.CreateDocument(t.Context(), domain.Document{Slug: "privacy", Audience: domain.AudienceAll, Required: required}); err != nil {
		t.Fatal(err)
	}

	draft, err := service.SaveDraft(t.Context(), domain.Version{Document: "privacy", EffectiveAt: time.Now().Add(-time.Hour), Texts: []domain.Text{{Lang: "en", Title: "Privacy", Body: "text"}}})
	if err != nil {
		t.Fatal(err)
	}

	published, err := service.Publish(t.Context(), draft.Document, draft.Number)
	if err != nil {
		t.Fatal(err)
	}

	return service, repo, published
}

func TestAcceptanceAndPending(t *testing.T) {
	t.Parallel()
	service, _, published := fixture(t, true)
	ctx := t.Context()
	input := domain.AcceptInput{Subject: "person", Items: []domain.Item{{Document: "privacy", Version: published.Number, Lang: "en"}}}

	first, err := service.Accept(ctx, input)
	if err != nil {
		t.Fatal(err)
	}

	second, err := service.Accept(ctx, input)
	if err != nil || second[0].ID != first[0].ID {
		t.Fatal(second, err)
	}

	pending, err := service.Pending(ctx, "person", domain.AudienceAll, "en")
	if err != nil || len(pending) != 0 {
		t.Fatal(pending, err)
	}

	next := published
	next.Number = 0
	next.Status = domain.Draft

	next, err = service.SaveDraft(ctx, next)
	if err != nil {
		t.Fatal(err)
	}

	if _, err = service.Publish(ctx, next.Document, next.Number); err != nil {
		t.Fatal(err)
	}

	pending, err = service.Pending(ctx, "person", domain.AudienceAll, "en")
	if err != nil || len(pending) != 1 || !pending[0].PreviousAccepted {
		t.Fatal(pending, err)
	}

	if _, err = service.Accept(ctx, input); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("stale acceptance", err)
	}

	if _, err = service.Withdraw(ctx, domain.WithdrawInput{Subject: "person", Document: "privacy"}); !errors.Is(err, domain.ErrConflict) {
		t.Fatal(err)
	}
}

func TestWithdrawAndReaccept(t *testing.T) {
	t.Parallel()
	service, repo, published := fixture(t, false)
	ctx := t.Context()

	input := domain.AcceptInput{Subject: "person", Items: []domain.Item{{Document: "privacy", Version: published.Number, Lang: "en"}}}
	if _, err := service.Accept(ctx, input); err != nil {
		t.Fatal(err)
	}

	withdrawn, err := service.Withdraw(ctx, domain.WithdrawInput{Subject: "person", Document: "privacy"})
	if err != nil || withdrawn.Kind != domain.Withdrawn {
		t.Fatal(withdrawn, err)
	}

	again, err := service.Withdraw(ctx, domain.WithdrawInput{Subject: "person", Document: "privacy"})
	if err != nil || again.ID != withdrawn.ID {
		t.Fatal(again, err)
	}

	pending, err := service.Pending(ctx, "person", "all", "en")
	if err != nil || len(pending) != 1 {
		t.Fatal(pending, err)
	}

	records, err := service.Accept(ctx, input)
	if err != nil || records[0].Seq != 3 {
		t.Fatal(records, err)
	}

	result, err := service.Verify(ctx, 0, 0)
	if err != nil || !result.OK || result.Checked != 3 {
		t.Fatal(result, err)
	}

	if len(repo.events) != 4 {
		t.Fatal(repo.events)
	}
}

func TestAtomicBatchAndOutbox(t *testing.T) {
	t.Parallel()
	service, repo, published := fixture(t, false)

	input := domain.AcceptInput{Subject: "person", Items: []domain.Item{{Document: "privacy", Version: published.Number, Lang: "en"}, {Document: "missing", Version: 1, Lang: "en"}}}
	if _, err := service.Accept(t.Context(), input); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal(err)
	}

	if len(repo.records) != 0 || len(repo.events) != 1 {
		t.Fatal("partial batch committed")
	}

	input.Items = input.Items[:1]
	repo.failEvents = true

	if _, err := service.Accept(t.Context(), input); err == nil {
		t.Fatal("outbox failure should fail append")
	}

	if len(repo.records) != 0 {
		t.Fatal("evidence committed without event")
	}
}

func TestConcurrentAccept(t *testing.T) {
	t.Parallel()
	service, _, published := fixture(t, false)

	var group sync.WaitGroup
	for i := range 32 {
		group.Go(func() {
			_, err := service.Accept(t.Context(), domain.AcceptInput{Subject: fmt.Sprintf("subject-%d", i), Items: []domain.Item{{Document: published.Document, Version: published.Number, Lang: "en"}}})
			if err != nil {
				t.Error(err)
			}
		})
	}

	group.Wait()

	verified, err := service.Verify(t.Context(), 0, 0)
	if err != nil || !verified.OK || verified.Checked != 32 {
		t.Fatal(verified, err)
	}
}

func TestDraftAndPublicRules(t *testing.T) {
	t.Parallel()
	service, _, published := fixture(t, false)
	ctx := t.Context()

	published.Status = domain.Draft
	if _, err := service.SaveDraft(ctx, published); !errors.Is(err, domain.ErrConflict) {
		t.Fatal(err)
	}

	future := published
	future.Number = 0
	future.EffectiveAt = time.Now().Add(time.Hour)

	draft, err := service.SaveDraft(ctx, future)
	if err != nil {
		t.Fatal(err)
	}

	if _, err = service.GetDocument(ctx, draft.Document, draft.Number, "en"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal(err)
	}

	if _, err = service.Publish(ctx, draft.Document, draft.Number); err != nil {
		t.Fatal(err)
	}

	if _, err = service.GetDocument(ctx, draft.Document, draft.Number, "en"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal(err)
	}

	if got, err := service.GetDocument(ctx, "privacy", 0, "de"); err != nil || got.Version != 1 || got.Text.Lang != "en" {
		t.Fatal(got, err)
	}

	if _, err = service.Accept(ctx, domain.AcceptInput{Subject: "person", Items: []domain.Item{{Document: "privacy", Version: 1, Lang: "ru"}}}); !errors.Is(err, domain.ErrConflict) {
		t.Fatal(err)
	}
}

func (*memory) LockDocument(context.Context, string) error { return nil }

func TestRegistrationIncludesAllDocuments(t *testing.T) {
	t.Parallel()
	service, _, _ := fixture(t, true)

	ctx := t.Context()
	if err := service.CreateDocument(ctx, domain.Document{Slug: "marketing", Audience: domain.AudienceRegistration}); err != nil {
		t.Fatal(err)
	}

	draft, err := service.SaveDraft(ctx, domain.Version{Document: "marketing", EffectiveAt: time.Now().Add(-time.Hour), Texts: []domain.Text{{Lang: "en", Title: "Marketing", Body: "Optional consent"}}})
	if err != nil {
		t.Fatal(err)
	}

	if _, err = service.Publish(ctx, draft.Document, draft.Number); err != nil {
		t.Fatal(err)
	}

	pending, err := service.Pending(ctx, "new-person", domain.AudienceRegistration, "en")
	if err != nil || len(pending) != 2 {
		t.Fatal("registration must include installation-wide obligations", pending, err)
	}

	all, err := service.Pending(ctx, "new-person", domain.AudienceAll, "en")
	if err != nil || len(all) != 1 || all[0].Slug != "privacy" {
		t.Fatal(all, err)
	}
}

func TestWithdrawalDoesNotResurrectEarlierAcceptance(t *testing.T) {
	t.Parallel()
	service, _, published := fixture(t, false)
	ctx := t.Context()

	input := domain.AcceptInput{Subject: "person", Items: []domain.Item{{Document: published.Document, Version: 1, Lang: "en"}}}
	if _, err := service.Accept(ctx, input); err != nil {
		t.Fatal(err)
	}

	published.Status = domain.Draft
	published.Number = 0

	draft, err := service.SaveDraft(ctx, published)
	if err != nil {
		t.Fatal(err)
	}

	if _, err = service.Publish(ctx, draft.Document, draft.Number); err != nil {
		t.Fatal(err)
	}

	input.Items[0].Version = draft.Number
	if _, err = service.Accept(ctx, input); err != nil {
		t.Fatal(err)
	}

	if _, err = service.Withdraw(ctx, domain.WithdrawInput{Subject: "person", Document: published.Document}); err != nil {
		t.Fatal(err)
	}

	pending, err := service.Pending(ctx, "person", domain.AudienceAll, "en")
	if err != nil || len(pending) != 1 || pending[0].Version != draft.Number {
		t.Fatal("old consent resurrected", pending, err)
	}
}

func TestDraftNumberAllocation(t *testing.T) {
	t.Parallel()
	service, _, published := fixture(t, false)
	published.Status = domain.Draft

	published.Number = 99
	if _, err := service.SaveDraft(t.Context(), published); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("arbitrary version inserted", err)
	}

	published.Number = 0

	draft, err := service.SaveDraft(t.Context(), published)
	if err != nil || draft.Number != 2 {
		t.Fatal(draft, err)
	}

	draft.Texts[0].Body = "edited draft"

	saved, err := service.SaveDraft(t.Context(), draft)
	if err != nil || saved.Number != draft.Number || saved.Texts[0].Body != "edited draft" {
		t.Fatal(saved, err)
	}
}
