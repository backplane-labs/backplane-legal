// Package admin adapts legal application use cases to the internal console API.
package admin

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/backplane-labs/backplane-legal/internal/domain"
)

// ExportRecords preserves every evidence field. CSV is a machine-readable evidence
// format, not a spreadsheet: subjects and context are not rewritten or prefixed.
func ExportRecords(records []domain.Record, format string) ([]byte, error) {
	if format == "json" {
		if records == nil {
			records = []domain.Record{}
		}

		data, err := json.MarshalIndent(records, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("encode journal: %w", err)
		}

		return data, nil
	}

	if format != "csv" {
		return nil, fmt.Errorf("%w: export format must be csv or json", domain.ErrInvalid)
	}

	var out bytes.Buffer

	writer := csv.NewWriter(&out)
	if err := writer.Write([]string{
		"id", "seq", "subject", "document", "version", "lang", "content_hash", "kind", "accepted_at", "context",
		"prev_hash", "hash",
	}); err != nil {
		return nil, fmt.Errorf("CSV header: %w", err)
	}

	for i := range records {
		record := &records[i]

		metadata, err := json.Marshal(record.Context)
		if err != nil {
			return nil, fmt.Errorf("CSV context: %w", err)
		}

		row := []string{
			record.ID,
			strconv.FormatInt(record.Seq, 10),
			record.Subject,
			record.Document,
			strconv.FormatInt(record.Version, 10),
			record.Lang,
			record.ContentHash,
			record.Kind,
			record.AcceptedAt.UTC().Format(time.RFC3339Nano),
			string(metadata),
			record.PrevHash,
			record.Hash,
		}
		if err := writer.Write(row); err != nil {
			return nil, fmt.Errorf("CSV record: %w", err)
		}
	}

	writer.Flush()

	if err := writer.Error(); err != nil {
		return nil, fmt.Errorf("CSV flush: %w", err)
	}

	return out.Bytes(), nil
}
