package app

import (
	"context"
	"fmt"
	"maps"
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

	view, err := s.GetDocument(ctx, item.Document, 0, item.Lang)
	if err != nil {
		return domain.Record{}, wrap(err)
	}

	if view.Version != item.Version || !strings.EqualFold(view.Text.Lang, item.Lang) {
		return domain.Record{}, fmt.Errorf("%w: version not current or language unavailable", domain.ErrConflict)
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

		latest, exists := domain.LatestState(records, input.Subject, input.Document, 0)
		if !exists {
			return domain.ErrNotFound
		}

		if latest.Kind == domain.Withdrawn {
			result = latest
			return nil
		}

		latest.Kind = domain.Withdrawn
		latest.Context = input.Context
		result, err = s.appendRecord(ctx, latest, "legal.Withdrawn")

		return wrap(err)
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
