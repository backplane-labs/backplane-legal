// Package app implements legal document lifecycle and acceptance evidence scenarios.
package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/backplane-labs/backplane-legal/internal/app/port"
	"github.com/backplane-labs/backplane-legal/internal/domain"
)

// Service is immutable; transaction state lives in the repository context.
type Service struct {
	repo        port.Repository
	defaultLang string
}

func New(repo port.Repository, defaultLang string) *Service {
	return &Service{repo: repo, defaultLang: defaultLang}
}

func (s Service) mutate(ctx context.Context, fn func(context.Context) error) error {
	err := s.repo.InTx(ctx, func(ctx context.Context) error {
		if err := s.repo.Lock(ctx); err != nil {
			return wrap(err)
		}

		return fn(ctx)
	})

	return wrap(err)
}

func (s Service) CreateDocument(ctx context.Context, doc domain.Document) error {
	if doc.DefaultLang == "" {
		doc.DefaultLang = s.defaultLang
	}

	if err := domain.ValidateDocument(doc); err != nil {
		return wrap(err)
	}

	return wrap(s.repo.CreateDocument(ctx, doc))
}

func (s Service) SaveDraft(ctx context.Context, version domain.Version) (domain.Version, error) {
	if version.Status != "" && version.Status != domain.Draft {
		return domain.Version{}, fmt.Errorf("%w: save only drafts", domain.ErrConflict)
	}

	if err := domain.ValidateVersion(version); err != nil {
		return domain.Version{}, wrap(err)
	}

	version.Status = domain.Draft
	version.EffectiveAt = version.EffectiveAt.UTC().Truncate(time.Microsecond)

	version.Texts = slices.Clone(version.Texts)
	for i := range version.Texts {
		version.Texts[i].ContentHash = ""
	}

	err := s.documentMutation(ctx, version.Document, func(ctx context.Context) error {
		if _, err := s.repo.Document(ctx, version.Document); err != nil {
			return wrap(err)
		}

		versions, err := s.repo.Versions(ctx, version.Document)
		if err != nil {
			return wrap(err)
		}

		var highest int64

		exists := false

		for _, old := range versions {
			if old.Number > highest {
				highest = old.Number
			}

			if old.Number == version.Number {
				exists = true
			}

			if old.Number == version.Number && old.Status == domain.Published {
				return fmt.Errorf("%w: published version is immutable", domain.ErrConflict)
			}
		}

		if version.Number == 0 {
			version.Number = highest + 1
		} else if !exists {
			return domain.ErrNotFound
		}

		return wrap(s.repo.SaveVersion(ctx, version))
	})

	return version, wrap(err)
}

func (s Service) Publish(ctx context.Context, slug string, number int64) (domain.Version, error) {
	var published domain.Version

	err := s.documentMutation(ctx, slug, func(ctx context.Context) error {
		doc, err := s.repo.Document(ctx, slug)
		if err != nil {
			return wrap(err)
		}

		version, err := s.version(ctx, slug, number)
		if err != nil {
			return wrap(err)
		}

		published, err = domain.Publish(version)
		if err != nil {
			return wrap(err)
		}

		if err := s.repo.SaveVersion(ctx, published); err != nil {
			return wrap(err)
		}

		return wrap(s.repo.Enqueue(ctx, "legal.Published", map[string]any{
			"document": slug, "version": number, "effective_at": published.EffectiveAt,
			"required": doc.Required, "audience": doc.Audience,
		}))
	})

	return published, wrap(err)
}

func (s Service) Documents(ctx context.Context, audience string) ([]domain.Document, error) {
	if audience != "" && !domain.ValidAudience(audience) {
		return nil, fmt.Errorf("%w: audience", domain.ErrInvalid)
	}

	result, err := s.repo.Documents(ctx, audience)

	return result, wrap(err)
}

func (s Service) Versions(ctx context.Context, slug string) ([]domain.Version, error) {
	if _, err := s.repo.Document(ctx, slug); err != nil {
		return nil, wrap(err)
	}

	result, err := s.repo.Versions(ctx, slug)

	return result, wrap(err)
}

func (s Service) version(ctx context.Context, slug string, number int64) (domain.Version, error) {
	versions, err := s.repo.Versions(ctx, slug)
	if err != nil {
		return domain.Version{}, wrap(err)
	}

	for _, version := range versions {
		if version.Number == number {
			return version, nil
		}
	}

	return domain.Version{}, domain.ErrNotFound
}

func (s Service) GetDocument(ctx context.Context, slug string, number int64, lang string) (domain.View, error) {
	if number < 0 || (lang != "" && !domain.ValidLanguage(lang)) {
		return domain.View{}, fmt.Errorf("%w: version or language", domain.ErrInvalid)
	}

	doc, err := s.repo.Document(ctx, slug)
	if err != nil {
		return domain.View{}, wrap(err)
	}

	versions, err := s.repo.Versions(ctx, slug)
	if err != nil {
		return domain.View{}, wrap(err)
	}

	now := time.Now()
	for _, version := range versions {
		if number != 0 && version.Number == number && version.Status == domain.Published && !version.EffectiveAt.After(now) {
			return domain.Localize(doc, version, lang), nil
		}
	}

	if number != 0 {
		return domain.View{}, domain.ErrNotFound
	}

	current, err := domain.Current(versions, now)
	if err != nil {
		return domain.View{}, wrap(err)
	}

	return domain.Localize(doc, current, lang), nil
}

func (s Service) ListDocuments(ctx context.Context, audience, lang string) ([]domain.View, error) {
	docs, err := s.Documents(ctx, audience)
	if err != nil {
		return nil, wrap(err)
	}

	if lang != "" && !domain.ValidLanguage(lang) {
		return nil, fmt.Errorf("%w: language", domain.ErrInvalid)
	}

	views := make([]domain.View, 0, len(docs))
	for _, doc := range docs {
		view, err := s.GetDocument(ctx, doc.Slug, 0, lang)
		if errors.Is(err, domain.ErrNotFound) {
			continue
		}

		if err != nil {
			return nil, wrap(err)
		}

		views = append(views, view)
	}

	return views, nil
}

func (s Service) Pending(ctx context.Context, subject, audience, lang string) ([]domain.View, error) {
	if strings.TrimSpace(subject) == "" {
		return nil, fmt.Errorf("%w: subject", domain.ErrInvalid)
	}

	// Registration includes every installation-wide obligation as well as
	// registration-only consents. Other audience filters remain exact.
	if audience == domain.AudienceRegistration {
		audience = ""
	}

	views, err := s.ListDocuments(ctx, audience, lang)
	if err != nil {
		return nil, wrap(err)
	}

	records, err := s.repo.Records(ctx, domain.Filter{Subject: subject})
	if err != nil {
		return nil, wrap(err)
	}

	pending := make([]domain.View, 0, len(views))
	for i := range views {
		view := views[i]

		state, exists := domain.LatestState(records, subject, view.Slug, view.Version)
		if exists && state.Kind == domain.Accepted {
			continue
		}

		for i := range records {
			record := &records[i]
			if record.Document == view.Slug && record.Version < view.Version && record.Kind == domain.Accepted {
				view.PreviousAccepted = true
			}
		}

		pending = append(pending, view)
	}

	return pending, nil
}

func (s Service) Acceptances(ctx context.Context, filter domain.Filter) ([]domain.Record, error) {
	if filter.Version < 0 || (!filter.From.IsZero() && !filter.To.IsZero() && filter.From.After(filter.To)) {
		return nil, fmt.Errorf("%w: filter", domain.ErrInvalid)
	}

	result, err := s.repo.Records(ctx, filter)

	return result, wrap(err)
}

func (s Service) Verify(ctx context.Context, from, until int64) (domain.Verification, error) {
	if from < 0 || until < 0 || (until != 0 && from > until) {
		return domain.Verification{}, fmt.Errorf("%w: verification range", domain.ErrInvalid)
	}

	var result domain.Verification

	err := s.mutate(ctx, func(ctx context.Context) error {
		records, err := s.repo.Records(ctx, domain.Filter{})
		if err != nil {
			return wrap(err)
		}

		seq, hash, err := s.repo.Head(ctx)
		if err != nil {
			return wrap(err)
		}

		result = domain.Verify(records, seq, hash, from, until)

		return nil
	})

	return result, wrap(err)
}

func wrap(err error) error {
	if err == nil {
		return nil
	}

	return fmt.Errorf("legal scenario: %w", err)
}

func (s Service) documentMutation(ctx context.Context, slug string, fn func(context.Context) error) error {
	err := s.repo.InTx(ctx, func(ctx context.Context) error {
		if err := s.repo.LockDocument(ctx, slug); err != nil {
			return wrap(err)
		}

		return fn(ctx)
	})

	return wrap(err)
}
