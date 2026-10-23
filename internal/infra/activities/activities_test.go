package activities_test

import (
	"context"
	"testing"

	"github.com/backplane-labs/backplane/pkg/backplane/activity"
	"github.com/backplane-labs/backplane/pkg/backplane/backplanetest"
	"github.com/backplane-labs/backplane/pkg/backplane/meta"

	"github.com/backplane-labs/backplane-legal/internal/domain"
	"github.com/backplane-labs/backplane-legal/internal/infra/activities"
)

type cases struct{}

func (cases) Pending(_ context.Context, subject, audience, lang string) ([]domain.View, error) {
	if subject == "" {
		return nil, domain.ErrInvalid
	}

	return []domain.View{{Document: domain.Document{Slug: "terms", Audience: audience}, Text: domain.Text{Lang: lang}}}, nil
}

func (cases) Accept(_ context.Context, in domain.AcceptInput) ([]domain.Record, error) {
	return []domain.Record{{Subject: in.Subject, Context: in.Context}}, nil
}

func (cases) Withdraw(_ context.Context, in domain.WithdrawInput) (domain.Record, error) {
	return domain.Record{Subject: in.Subject, Context: in.Context}, nil
}

func (cases) GetDocument(context.Context, string, int64, string) (domain.View, error) {
	return domain.View{}, nil
}

func (cases) ListDocuments(context.Context, string, string) ([]domain.View, error) {
	return []domain.View{}, nil
}

func (cases) Acceptances(context.Context, domain.Filter) ([]domain.Record, error) {
	return []domain.Record{}, nil
}

func (cases) Verify(context.Context, int64, int64) (domain.Verification, error) {
	return domain.Verification{OK: true}, nil
}

func TestActivityPayloadsAndMetadata(t *testing.T) {
	t.Parallel()
	h := backplanetest.New(t, backplanetest.Name("legal"))
	activities.New(h.Root(), cases{})
	h.Start()

	manifest := h.Manifest()
	if len(manifest.GetActivities()) != 7 || len(manifest.GetEvents()) != 0 || len(manifest.GetHooks()) != 0 {
		t.Fatalf("unexpected contract: %v", manifest)
	}

	names := map[string]bool{}
	for _, entry := range manifest.GetActivities() {
		names[entry.GetName()] = true
	}

	for _, name := range []string{"Pending", "Accept", "Withdraw", "GetDocument", "ListDocuments", "Acceptances", "Verify"} {
		if !names[name] {
			t.Errorf("missing activity %s", name)
		}
	}

	out, err := backplanetest.Activity[activities.PendingInput, activities.DocumentsOutput](t.Context(), h, "Pending", activities.PendingInput{Subject: "user", Audience: "all", Lang: "en"})
	if err != nil || len(out.Documents) != 1 || out.Documents[0].Text.Lang != "en" {
		t.Fatalf("%+v, %v", out, err)
	}

	ctx := meta.With(t.Context(), "ip", "192.0.2.1", "channel", "import")

	accepted, err := backplanetest.Activity[domain.AcceptInput, activities.RecordsOutput](ctx, h, "Accept", domain.AcceptInput{Subject: "user", Context: map[string]string{"channel": "registration"}})
	if err != nil || len(accepted.Records) != 1 || accepted.Records[0].Context["ip"] != "192.0.2.1" || accepted.Records[0].Context["channel"] != "registration" {
		t.Fatalf("%+v, %v", accepted, err)
	}

	_, err = backplanetest.Activity[activities.PendingInput, activities.DocumentsOutput](t.Context(), h, "Pending", activities.PendingInput{})
	if err == nil {
		t.Fatal("invalid input was accepted")
	}

	if !activity.IsNonRetryable(err) {
		t.Fatalf("expected non-retryable error, got %v", err)
	}
}
