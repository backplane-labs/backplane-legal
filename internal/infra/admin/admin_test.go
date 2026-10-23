package admin_test

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/backplane-labs/backplane/pkg/backplane/backplanetest"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/backplane-labs/backplane-legal/internal/domain"
	"github.com/backplane-labs/backplane-legal/internal/infra/admin"
	legalconsolev1 "github.com/backplane-labs/backplane-legal/proto/legal/console/v1"
)

func TestEvidenceExport(t *testing.T) {
	t.Parallel()

	records := []domain.Record{{ID: "one", Seq: 1, Subject: "=FORMULA", Document: "privacy", Version: 2, Lang: "en", Kind: "accepted", AcceptedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Context: map[string]string{"ip": "127.0.0.1"}, PrevHash: "previous", Hash: "evidence", ContentHash: "content"}}

	data, err := admin.ExportRecords(records, "json")
	if err != nil {
		t.Fatal(err)
	}

	var decoded []domain.Record
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}

	if len(decoded) != 1 || decoded[0].Hash != "evidence" || decoded[0].Context["ip"] != "127.0.0.1" {
		t.Fatal("lost evidence")
	}

	data, err = admin.ExportRecords(records, "csv")
	if err != nil {
		t.Fatal(err)
	}

	rows, err := csv.NewReader(strings.NewReader(string(data))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}

	if len(rows) != 2 || rows[0][0] != "id" || !strings.Contains(string(data), "content_hash") || !strings.Contains(string(data), "evidence") {
		t.Fatal("incomplete CSV")
	}

	if _, err = admin.ExportRecords(records, "xml"); err == nil {
		t.Fatal("unsupported format accepted")
	}
}

type failingCases struct{ err error }

func (c failingCases) Documents(context.Context, string) ([]domain.Document, error) {
	return nil, c.err
}
func (c failingCases) CreateDocument(context.Context, domain.Document) error      { return c.err }
func (c failingCases) Versions(context.Context, string) ([]domain.Version, error) { return nil, c.err }
func (c failingCases) SaveDraft(context.Context, domain.Version) (domain.Version, error) {
	return domain.Version{}, c.err
}

func (c failingCases) Publish(context.Context, string, int64) (domain.Version, error) {
	return domain.Version{}, c.err
}

func (c failingCases) Acceptances(context.Context, domain.Filter) ([]domain.Record, error) {
	return nil, c.err
}

func (c failingCases) Verify(context.Context, int64, int64) (domain.Verification, error) {
	return domain.Verification{}, c.err
}

func TestErrorMapping(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		err  error
		code codes.Code
	}{
		{domain.ErrInvalid, codes.InvalidArgument},
		{domain.ErrNotFound, codes.NotFound},
		{domain.ErrConflict, codes.FailedPrecondition},
		{errors.New("private database details"), codes.Unavailable},
	} {
		h := backplanetest.New(t)
		adapter := admin.New(h.Root(), failingCases{err: tc.err})

		_, err := adapter.ListDocuments(t.Context(), &legalconsolev1.ListDocumentsRequest{})
		if status.Code(err) != tc.code || strings.Contains(err.Error(), "private database") {
			t.Fatalf("%v: %v", tc.code, err)
		}
	}
}

func TestInvalidRequests(t *testing.T) {
	t.Parallel()
	h := backplanetest.New(t)

	adapter := admin.New(h.Root(), failingCases{err: errors.New("unexpected application call")})
	for _, version := range []*legalconsolev1.Version{nil, {}, {EffectiveAt: "not a date"}} {
		_, err := adapter.SaveDraft(t.Context(), &legalconsolev1.SaveDraftRequest{Version: version})
		if status.Code(err) != codes.InvalidArgument {
			t.Fatalf("version %v: %v", version, err)
		}
	}

	for _, filter := range []*legalconsolev1.Filter{
		{Version: -1}, {From: "bad"}, {To: "bad"}, {From: "2026-02-01T00:00:00Z", To: "2026-01-01T00:00:00Z"},
	} {
		_, err := adapter.Journal(t.Context(), &legalconsolev1.JournalRequest{Filter: filter})
		if status.Code(err) != codes.InvalidArgument {
			t.Fatalf("journal filter %v: %v", filter, err)
		}

		_, err = adapter.Export(t.Context(), &legalconsolev1.ExportRequest{Filter: filter, Format: "json"})
		if status.Code(err) != codes.InvalidArgument {
			t.Fatalf("export filter %v: %v", filter, err)
		}
	}

	_, err := adapter.Preview(t.Context(), &legalconsolev1.PreviewRequest{Markdown: strings.Repeat("x", (2<<20)+1)})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("oversize preview: %v", err)
	}
}
