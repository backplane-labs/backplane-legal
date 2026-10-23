package admin

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/backplane-labs/backplane/pkg/backplane"
	"github.com/backplane-labs/backplane/pkg/backplane/deps"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/gopherex/xlog"

	"github.com/backplane-labs/backplane-legal/internal/domain"
	"github.com/backplane-labs/backplane-legal/internal/infra/httpapi"
	legalconsolev1 "github.com/backplane-labs/backplane-legal/proto/legal/console/v1"
)

// Cases is the console's application port.
type Cases interface {
	Documents(ctx context.Context, audience string) ([]domain.Document, error)
	CreateDocument(ctx context.Context, document domain.Document) error
	Versions(ctx context.Context, slug string) ([]domain.Version, error)
	SaveDraft(ctx context.Context, version domain.Version) (domain.Version, error)
	Publish(ctx context.Context, slug string, number int64) (domain.Version, error)
	Acceptances(ctx context.Context, filter domain.Filter) ([]domain.Record, error)
	Verify(ctx context.Context, from, to int64) (domain.Verification, error)
}

// Admin is the operator-facing SDK component.
type Admin struct {
	legalconsolev1.UnimplementedAdminServiceServer
	deps.Component
	cases Cases
}

// New creates the internal console adapter.
func New(parent deps.Scope, cases Cases) *Admin {
	return &Admin{Component: deps.NewComponent(parent, "admin"), cases: cases}
}

// ListDocuments includes documents that have no published version yet.
func (a *Admin) ListDocuments(
	ctx context.Context,
	req *legalconsolev1.ListDocumentsRequest,
) (*legalconsolev1.ListDocumentsResponse, error) {
	docs, err := a.cases.Documents(ctx, req.GetAudience())
	if err != nil {
		return nil, a.failure(err)
	}

	out := &legalconsolev1.ListDocumentsResponse{}
	for _, doc := range docs {
		out.Documents = append(out.Documents, &legalconsolev1.Document{
			Slug:        doc.Slug,
			Audience:    doc.Audience,
			Required:    doc.Required,
			DefaultLang: doc.DefaultLang,
		})
	}

	return out, nil
}

// CreateDocument establishes a stable policy; later corrections are versions.
func (a *Admin) CreateDocument(
	ctx context.Context,
	req *legalconsolev1.CreateDocumentRequest,
) (*legalconsolev1.CreateDocumentResponse, error) {
	doc := req.GetDocument()

	value := domain.Document{
		Slug:        doc.GetSlug(),
		Audience:    doc.GetAudience(),
		Required:    doc.GetRequired(),
		DefaultLang: doc.GetDefaultLang(),
	}
	if err := a.cases.CreateDocument(ctx, value); err != nil {
		return nil, a.failure(err)
	}

	a.audit(ctx, "legal.document.created", value.Slug)

	return &legalconsolev1.CreateDocumentResponse{}, nil
}

// GetVersions returns draft and publication history for an operator.
func (a *Admin) GetVersions(
	ctx context.Context,
	req *legalconsolev1.GetVersionsRequest,
) (*legalconsolev1.GetVersionsResponse, error) {
	versions, err := a.cases.Versions(ctx, req.GetDocument())
	if err != nil {
		return nil, a.failure(err)
	}

	out := &legalconsolev1.GetVersionsResponse{}
	for _, version := range versions {
		out.Versions = append(out.Versions, versionProto(version))
	}

	return out, nil
}

// SaveDraft updates an existing draft or allocates the next version number.
func (a *Admin) SaveDraft(
	ctx context.Context,
	req *legalconsolev1.SaveDraftRequest,
) (*legalconsolev1.SaveDraftResponse, error) {
	version, err := versionDomain(req.GetVersion())
	if err != nil {
		return nil, a.failure(err)
	}

	saved, err := a.cases.SaveDraft(ctx, version)
	if err != nil {
		return nil, a.failure(err)
	}

	a.audit(ctx, "legal.draft.saved", saved.Document)

	return &legalconsolev1.SaveDraftResponse{Version: versionProto(saved)}, nil
}

// Publish freezes the version and every language's evidence hash.
func (a *Admin) Publish(
	ctx context.Context,
	req *legalconsolev1.PublishRequest,
) (*legalconsolev1.PublishResponse, error) {
	saved, err := a.cases.Publish(ctx, req.GetDocument(), req.GetNumber())
	if err != nil {
		return nil, a.failure(err)
	}

	a.audit(ctx, "legal.version.published", saved.Document)

	return &legalconsolev1.PublishResponse{Version: versionProto(saved)}, nil
}

// Journal reads evidence without mutating it.
func (a *Admin) Journal(
	ctx context.Context,
	req *legalconsolev1.JournalRequest,
) (*legalconsolev1.JournalResponse, error) {
	filter, err := filterDomain(req.GetFilter())
	if err != nil {
		return nil, a.failure(err)
	}

	records, err := a.cases.Acceptances(ctx, filter)
	if err != nil {
		return nil, a.failure(err)
	}

	out := &legalconsolev1.JournalResponse{}

	for i := range records {
		record := &records[i]
		out.Records = append(out.Records, recordProto(*record))
	}

	return out, nil
}

// Verify checks the journal and its chain head, not a filtered export.
func (a *Admin) Verify(ctx context.Context, req *legalconsolev1.VerifyRequest) (*legalconsolev1.VerifyResponse, error) {
	result, err := a.cases.Verify(ctx, req.GetFrom(), req.GetTo())
	if err != nil {
		return nil, a.failure(err)
	}

	return &legalconsolev1.VerifyResponse{Ok: result.OK, BrokenSeq: result.BrokenSeq, Checked: result.Checked}, nil
}

// Export produces exact evidence bytes for download through the authenticated relay.
func (a *Admin) Export(ctx context.Context, req *legalconsolev1.ExportRequest) (*legalconsolev1.ExportResponse, error) {
	filter, err := filterDomain(req.GetFilter())
	if err != nil {
		return nil, a.failure(err)
	}

	records, err := a.cases.Acceptances(ctx, filter)
	if err != nil {
		return nil, a.failure(err)
	}

	data, err := ExportRecords(records, req.GetFormat())
	if err != nil {
		return nil, a.failure(err)
	}

	kind := "application/json"
	if req.GetFormat() == "csv" {
		kind = "text/csv; charset=utf-8"
	}

	a.audit(ctx, "legal.journal.exported", filter.Document)

	return &legalconsolev1.ExportResponse{Data: data, ContentType: kind, Filename: "legal-journal." + req.GetFormat()}, nil
}

// Preview uses exactly the public page's safe Markdown renderer.
func (a *Admin) Preview(
	_ context.Context,
	req *legalconsolev1.PreviewRequest,
) (*legalconsolev1.PreviewResponse, error) {
	const maxMarkdown = 2 << 20
	if len(req.GetMarkdown()) > maxMarkdown {
		return nil, a.failure(fmt.Errorf("%w: Markdown exceeds 2 MiB", domain.ErrInvalid))
	}

	html, err := httpapi.Render(req.GetMarkdown())
	if err != nil {
		return nil, a.failure(err)
	}

	return &legalconsolev1.PreviewResponse{Html: html}, nil
}

func (a *Admin) failure(err error) error {
	switch {
	case errors.Is(err, domain.ErrInvalid):
		return status.Error(codes.InvalidArgument, err.Error()) //nolint:wrapcheck // Return gRPC status.
	case errors.Is(err, domain.ErrNotFound):
		return status.Error(codes.NotFound, err.Error()) //nolint:wrapcheck // Return gRPC status.
	case errors.Is(err, domain.ErrConflict):
		return status.Error(codes.FailedPrecondition, err.Error()) //nolint:wrapcheck // Return gRPC status.
	default:
		a.Log().Error("legal operation failed", xlog.Err(err))
		//nolint:wrapcheck // Return gRPC status.
		return status.Error(codes.Unavailable, "legal store unavailable; see service log")
	}
}

func (a *Admin) audit(ctx context.Context, name, document string) {
	actor := "internal"
	if operator, ok := backplane.OperatorOf(ctx); ok {
		actor = operator.Actor()
	}

	a.Log().Ctx().Info(ctx, name, backplane.Audit(),
		xlog.String("event.name", name),
		xlog.String(backplane.AuditID, uuid.NewString()),
		xlog.String(backplane.AuditSubject, "document/"+document),
		xlog.String(backplane.AuditActor, actor),
		xlog.String(backplane.AuditOutcome, "succeeded"),
	)
}

func versionProto(value domain.Version) *legalconsolev1.Version {
	out := &legalconsolev1.Version{
		Document:    value.Document,
		Number:      value.Number,
		EffectiveAt: value.EffectiveAt.UTC().Format(time.RFC3339Nano),
		ChangeNote:  value.ChangeNote,
		Status:      value.Status,
	}
	for _, text := range value.Texts {
		out.Texts = append(out.Texts, &legalconsolev1.Text{
			Lang:        text.Lang,
			Title:       text.Title,
			Body:        text.Body,
			Url:         text.URL,
			ContentHash: text.ContentHash,
		})
	}

	return out
}

func versionDomain(value *legalconsolev1.Version) (domain.Version, error) {
	effective, err := time.Parse(time.RFC3339Nano, value.GetEffectiveAt())
	if err != nil {
		return domain.Version{}, fmt.Errorf("%w: effective_at must be RFC 3339", domain.ErrInvalid)
	}

	out := domain.Version{
		Document:    value.GetDocument(),
		Number:      value.GetNumber(),
		EffectiveAt: effective,
		ChangeNote:  value.GetChangeNote(),
		Status:      value.GetStatus(),
	}
	for _, text := range value.GetTexts() {
		out.Texts = append(out.Texts, domain.Text{
			Lang:  text.GetLang(),
			Title: text.GetTitle(),
			Body:  text.GetBody(),
			URL:   text.GetUrl(),
		})
	}

	return out, nil
}

func filterDomain(value *legalconsolev1.Filter) (domain.Filter, error) {
	out := domain.Filter{Subject: value.GetSubject(), Document: value.GetDocument(), Version: value.GetVersion()}
	if out.Version < 0 {
		return out, fmt.Errorf("%w: negative version", domain.ErrInvalid)
	}

	if value.GetFrom() != "" {
		parsed, err := time.Parse(time.RFC3339Nano, value.GetFrom())
		if err != nil {
			return out, fmt.Errorf("%w: from must be RFC 3339", domain.ErrInvalid)
		}

		out.From = parsed
	}

	if value.GetTo() != "" {
		parsed, err := time.Parse(time.RFC3339Nano, value.GetTo())
		if err != nil {
			return out, fmt.Errorf("%w: to must be RFC 3339", domain.ErrInvalid)
		}

		out.To = parsed
	}

	if !out.From.IsZero() && !out.To.IsZero() && out.From.After(out.To) {
		return out, fmt.Errorf("%w: from is after to", domain.ErrInvalid)
	}

	return out, nil
}

func recordProto(value domain.Record) *legalconsolev1.Record {
	return &legalconsolev1.Record{
		Id:          value.ID,
		Seq:         value.Seq,
		Subject:     value.Subject,
		Document:    value.Document,
		Version:     value.Version,
		Lang:        value.Lang,
		ContentHash: value.ContentHash,
		Kind:        value.Kind,
		AcceptedAt:  value.AcceptedAt.UTC().Format(time.RFC3339Nano),
		Context:     value.Context,
		PrevHash:    value.PrevHash,
		Hash:        value.Hash,
	}
}
