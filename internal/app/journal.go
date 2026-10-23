package app

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/backplane-labs/backplane-legal/internal/domain"
)

func (s Service) Accept(ctx context.Context, input domain.AcceptInput) ([]domain.Record, error) {
	if strings.TrimSpace(input.Subject) == "" || len(input.Items) == 0 {
		return nil, fmt.Errorf("%w: subject and items required", domain.ErrInvalid)
	}

	result := make([]domain.Record, 0, len(input.Items))

	err := s.mutate(ctx, func(ctx context.Context) error {
		for _, item := range input.Items {
			record, err := s.acceptItem(ctx, input, item)
			if err != nil {
				return wrap(err)
			}

			result = append(result, record)
		}

		return nil
	})
	if err != nil {
		return nil, wrap(err)
	}

	return result, nil
}

func (s Service) acceptItem(ctx context.Context, input domain.AcceptInput, item domain.Item) (domain.Record, error) {
	if item.Version < 1 || !domain.ValidLanguage(item.Lang) {
		return domain.Record{}, fmt.Errorf("%w: explicit version and language required", domain.ErrInvalid)
	}

	if err := s.repo.LockDocument(ctx, item.Document); err != nil {
		return domain.Record{}, wrap(err)
	}

	// Any published version already in effect: the subject accepted the
	// text it was shown, even if a newer version took effect since (Pending
	// asks for that one next). A draft or a version not yet in effect is not.
	view, err := s.GetDocument(ctx, item.Document, item.Version, item.Lang)
	if errors.Is(err, domain.ErrNotFound) {
		if _, docErr := s.repo.Document(ctx, item.Document); docErr != nil {
			return domain.Record{}, wrap(docErr)
		}

		return domain.Record{}, fmt.Errorf("%w: version not published or not in effect", domain.ErrConflict)
	}

	if err != nil {
		return domain.Record{}, wrap(err)
	}

	if view.Version != item.Version || !strings.EqualFold(view.Text.Lang, item.Lang) {
		return domain.Record{}, fmt.Errorf("%w: language unavailable", domain.ErrConflict)
	}

	records, err := s.repo.Records(ctx, domain.Filter{
		Subject: input.Subject, Document: item.Document, Version: item.Version,
	})
	if err != nil {
		return domain.Record{}, wrap(err)
	}

	latest, exists := domain.LatestState(records, input.Subject, item.Document, item.Version)
	if exists && latest.Kind == domain.Accepted {
		return latest, nil
	}

	record := domain.Record{
		Subject: input.Subject, Document: item.Document, Version: item.Version,
		Lang: view.Text.Lang, ContentHash: view.Text.ContentHash, Kind: domain.Accepted, Context: input.Context,
	}

	return s.appendRecord(ctx, record, "legal.Accepted")
}

func (s Service) Withdraw(ctx context.Context, input domain.WithdrawInput) (domain.Record, error) {
	if strings.TrimSpace(input.Subject) == "" || input.Document == "" {
		return domain.Record{}, fmt.Errorf("%w: subject and document required", domain.ErrInvalid)
	}

	var result domain.Record

	err := s.mutate(ctx, func(ctx context.Context) error {
		doc, err := s.repo.Document(ctx, input.Document)
		if err != nil {
			return wrap(err)
		}

		if doc.Required {
			return fmt.Errorf("%w: required document cannot be withdrawn", domain.ErrConflict)
		}

		records, err := s.repo.Records(ctx, domain.Filter{Subject: input.Subject, Document: input.Document})
		if err != nil {
			return wrap(err)
		}

		// Every version still accepted is withdrawn: Accept takes any version
		// in effect, so an older acceptance may outlive a newer one.
		versions := map[int64]bool{}
		for i := range records {
			versions[records[i].Version] = true
		}

		withdrawn := false

		for _, version := range slices.Sorted(maps.Keys(versions)) {
			latest, _ := domain.LatestState(records, input.Subject, input.Document, version)
			if latest.Kind != domain.Accepted {
				continue
			}

			latest.Kind = domain.Withdrawn
			latest.Context = input.Context

			result, err = s.appendRecord(ctx, latest, "legal.Withdrawn")
			if err != nil {
				return wrap(err)
			}

			withdrawn = true
		}

		if withdrawn {
			return nil
		}

		latest, exists := domain.LatestState(records, input.Subject, input.Document, 0)
		if !exists {
			return domain.ErrNotFound
		}

		result = latest

		return nil
	})

	return result, wrap(err)
}

func (s Service) appendRecord(ctx context.Context, record domain.Record, event string) (domain.Record, error) {
	seq, hash, err := s.repo.Head(ctx)
	if err != nil {
		return domain.Record{}, wrap(err)
	}

	record.ID = uuid.NewString()
	record.Seq = seq + 1
	record.PrevHash = hash
	record.AcceptedAt = time.Now().UTC().Truncate(time.Microsecond)

	record.Context = maps.Clone(record.Context)
	if record.Context == nil {
		record.Context = map[string]string{}
	}

	record.Hash = domain.RecordHash(record)
	if err := s.repo.Append(ctx, record); err != nil {
		return domain.Record{}, wrap(err)
	}

	if err := s.repo.Enqueue(ctx, event, map[string]any{
		"subject": record.Subject, "document": record.Document, "version": record.Version,
		"lang": record.Lang, "seq": record.Seq,
	}); err != nil {
		return domain.Record{}, wrap(err)
	}

	return record, nil
}

// Check reports whether items cover every required document of audience
// (registration includes the installation-wide ones) by its current
// version in an available language, without writing anything: what a
// registration flow checks before it lets the identity be created. Items
// that name no published, effective text are rejected.
func (s Service) Check(ctx context.Context, audience string, items []domain.Item) (domain.CheckResult, error) {
	result := domain.CheckResult{Missing: []domain.View{}, Rejected: []domain.Item{}}

	for _, item := range items {
		if item.Version < 1 || !domain.ValidLanguage(item.Lang) {
			result.Rejected = append(result.Rejected, item)

			continue
		}

		view, err := s.GetDocument(ctx, item.Document, item.Version, item.Lang)

		switch {
		case errors.Is(err, domain.ErrNotFound), errors.Is(err, domain.ErrInvalid):
			result.Rejected = append(result.Rejected, item)
		case err != nil:
			return domain.CheckResult{}, wrap(err)
		case !strings.EqualFold(view.Text.Lang, item.Lang):
			result.Rejected = append(result.Rejected, item)
		}
	}

	if audience == domain.AudienceRegistration {
		audience = ""
	}

	views, err := s.ListDocuments(ctx, audience, "")
	if err != nil {
		return domain.CheckResult{}, wrap(err)
	}

	for i := range views {
		if !views[i].Required {
			continue
		}

		if !covers(items, result.Rejected, &views[i]) {
			result.Missing = append(result.Missing, views[i])
		}
	}

	result.OK = len(result.Missing) == 0 && len(result.Rejected) == 0

	return result, nil
}

// covers reports whether an accepted item names view's current version.
func covers(items, rejected []domain.Item, view *domain.View) bool {
	for _, item := range items {
		if item.Document == view.Slug && item.Version == view.Version && !slices.Contains(rejected, item) {
			return true
		}
	}

	return false
}
