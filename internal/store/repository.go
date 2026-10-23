package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/backplane-labs/backplane-legal/internal/domain"
	"github.com/backplane-labs/backplane-legal/internal/store/db"
)

func (s *Store) CreateDocument(ctx context.Context, doc domain.Document) error {
	payload, err := encode(doc)
	if err != nil {
		return err
	}

	return translate(s.Q.CreateDocument(ctx, db.CreateDocumentParams{
		Slug: doc.Slug, Audience: doc.Audience, Payload: payload,
	}))
}

func (s *Store) Document(ctx context.Context, slug string) (domain.Document, error) {
	row, err := s.Q.Document(ctx, slug)
	if err != nil {
		return domain.Document{}, translate(err)
	}

	return decode[domain.Document](row.Payload)
}

func (s *Store) Documents(ctx context.Context, audience string) ([]domain.Document, error) {
	rows, err := s.Q.Documents(ctx, audience)
	if err != nil {
		return nil, translate(err)
	}

	result := make([]domain.Document, 0, len(rows))
	for _, row := range rows {
		doc, err := decode[domain.Document](row.Payload)
		if err != nil {
			return nil, err
		}

		result = append(result, doc)
	}

	return result, nil
}

func (s *Store) Versions(ctx context.Context, slug string) ([]domain.Version, error) {
	rows, err := s.Q.Versions(ctx, slug)
	if err != nil {
		return nil, translate(err)
	}

	result := make([]domain.Version, 0, len(rows))
	for _, row := range rows {
		version, err := decode[domain.Version](row.Payload)
		if err != nil {
			return nil, err
		}

		result = append(result, version)
	}

	return result, nil
}

func (s *Store) SaveVersion(ctx context.Context, version domain.Version) error {
	payload, err := encode(version)
	if err != nil {
		return err
	}

	return translate(s.Q.SaveVersion(ctx, db.SaveVersionParams{
		Document: version.Document, Number: version.Number, Status: version.Status,
		EffectiveAt: version.EffectiveAt, Payload: payload,
	}))
}

func (s *Store) Records(ctx context.Context, filter domain.Filter) ([]domain.Record, error) {
	rows, err := s.Q.Records(ctx, db.RecordsParams{
		Subject: filter.Subject, Document: filter.Document, Version: filter.Version,
		FromTime: optionalTime(filter.From), ToTime: optionalTime(filter.To),
	})
	if err != nil {
		return nil, translate(err)
	}

	result := make([]domain.Record, 0, len(rows))
	for i := range rows {
		row := &rows[i]

		record, err := decode[domain.Record](row.Payload)
		if err != nil {
			return nil, err
		}

		// Indexed projections are evidence too: privileged corruption of either
		// representation must not disappear when verifying the canonical payload.
		if !matchingProjection(record, row) {
			record.Hash = ""
		}

		result = append(result, record)
	}

	return result, nil
}

func (s *Store) Head(ctx context.Context) (int64, string, error) {
	row, err := s.Q.Head(ctx)
	return row.Seq, row.Hash, translate(err)
}

func (s *Store) Append(ctx context.Context, record domain.Record) error {
	payload, err := encode(record)
	if err != nil {
		return err
	}

	id, err := uuid.Parse(record.ID)
	if err != nil {
		return fmt.Errorf("%w: journal UUID: %w", domain.ErrInvalid, err)
	}

	err = s.Q.AppendRecord(ctx, db.AppendRecordParams{
		Seq: record.Seq, ID: id, Subject: record.Subject, Document: record.Document,
		Version: record.Version, AcceptedAt: record.AcceptedAt, Kind: record.Kind, Payload: payload,
	})
	if err != nil {
		return translate(err)
	}

	return translate(s.Q.UpdateHead(ctx, db.UpdateHeadParams{Seq: record.Seq, Hash: record.Hash}))
}

func (s *Store) Enqueue(ctx context.Context, name string, value any) error {
	payload, err := encode(value)
	if err != nil {
		return err
	}

	return translate(s.Q.Enqueue(ctx, db.EnqueueParams{Name: name, Payload: payload}))
}

func (s *Store) PendingEvents(ctx context.Context, limit int32) ([]domain.Event, error) {
	if limit < 1 {
		return nil, fmt.Errorf("%w: positive event limit required", domain.ErrInvalid)
	}

	rows, err := s.Q.PendingEvents(ctx, limit)
	if err != nil {
		return nil, translate(err)
	}

	events := make([]domain.Event, 0, len(rows))
	for _, row := range rows {
		events = append(events, domain.Event{ID: row.ID, Name: row.Name, Payload: row.Payload})
	}

	return events, nil
}

// AckEvent removes a message only after the caller's successful publish.
func (s *Store) AckEvent(ctx context.Context, id int64) error {
	return translate(s.Q.AckEvent(ctx, id))
}

func optionalTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}

	return &value
}

func encode(value any) (json.RawMessage, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode legal state: %w", err)
	}

	return payload, nil
}

func decode[T any](payload json.RawMessage) (T, error) {
	var value T

	err := json.Unmarshal(payload, &value)
	if err != nil {
		return value, fmt.Errorf("decode legal state: %w", err)
	}

	return value, nil
}

func (s *Store) LockDocument(ctx context.Context, slug string) error {
	_, err := s.Q.LockDocument(ctx, slug)
	return translate(err)
}

func matchingProjection(record domain.Record, row *db.RecordsRow) bool {
	return record.Seq == row.Seq && record.ID == row.ID.String() && record.Subject == row.Subject &&
		record.Document == row.Document && record.Version == row.Version && record.Kind == row.Kind &&
		record.AcceptedAt.Equal(row.AcceptedAt)
}
