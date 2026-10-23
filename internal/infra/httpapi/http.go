// Package httpapi serves the unauthenticated, read-only legal API.
package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"

	"github.com/backplane-labs/backplane-legal/internal/domain"
)

// Reader is the published-document application port.
type Reader interface {
	GetDocument(ctx context.Context, slug string, version int64, lang string) (domain.View, error)
	ListDocuments(ctx context.Context, audience, lang string) ([]domain.View, error)
}

// Handler has no mutable state; every representation is derived from a current read.
type Handler struct {
	reader    Reader
	ancestors string
}

// New creates a public handler. Frame ancestors must already be validated.
func New(reader Reader, ancestors []string) Handler {
	return Handler{reader: reader, ancestors: strings.Join(ancestors, " ")}
}

// ValidateAncestors restricts configuration to CSP source expressions, not directives.
func ValidateAncestors(ancestors []string) error {
	if len(ancestors) == 0 {
		return fmt.Errorf("%w: frame_ancestors is empty", domain.ErrInvalid)
	}

	for _, source := range ancestors {
		if source == "*" || source == "'self'" || source == "'none'" {
			continue
		}

		if !validAncestorURL(source) {
			return fmt.Errorf("%w: invalid frame ancestor %q", domain.ErrInvalid, source)
		}
	}

	if len(ancestors) > 1 && strings.Contains(strings.Join(ancestors, " "), "'none'") {
		return fmt.Errorf("%w: 'none' cannot be combined", domain.ErrInvalid)
	}

	return nil
}

func validAncestorURL(source string) bool {
	parsed, err := url.Parse(source)
	if err != nil || strings.ContainsAny(source, ";\r\n\t ") {
		return false
	}

	validOrigin := (parsed.Scheme == "https" || parsed.Scheme == "http") && parsed.Host != ""
	noExtras := parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == ""

	return validOrigin && noExtras && (parsed.Path == "" || parsed.Path == "/")
}

// Render converts Markdown without enabling raw HTML and sanitizes the result.
func Render(markdown string) (string, error) {
	var out bytes.Buffer
	if err := goldmark.Convert([]byte(markdown), &out); err != nil {
		return "", fmt.Errorf("render markdown: %w", err)
	}

	return bluemonday.UGCPolicy().Sanitize(out.String()), nil
}

type representation struct {
	domain.Document
	Version     int64     `json:"version"`
	EffectiveAt time.Time `json:"effective_at"`
	ChangeNote  string    `json:"change_note"`
	Lang        string    `json:"lang"`
	Title       string    `json:"title"`
	Body        string    `json:"body"`
	URL         string    `json:"url"`
	ContentHash string    `json:"content_hash"`
	Languages   []string  `json:"languages"`
	TextURL     string    `json:"text_url"`
}

func present(view domain.View) representation {
	query := url.Values{"lang": {view.Text.Lang}, "version": {strconv.FormatInt(view.Version, 10)}}

	return representation{
		Document:    view.Document,
		Version:     view.Version,
		EffectiveAt: view.EffectiveAt,
		ChangeNote:  view.ChangeNote,
		Lang:        view.Text.Lang,
		Title:       view.Text.Title,
		Body:        view.Text.Body,
		URL:         view.Text.URL,
		ContentHash: view.Text.ContentHash,
		Languages:   view.Languages,
		TextURL:     "/legal/" + url.PathEscape(view.Slug) + "?" + query.Encode(),
	}
}

// ServeHTTP implements GET and HEAD on the legal namespace only.
func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")

	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)

		return
	}

	if r.URL.Path == "/legal/v1/documents" {
		h.list(w, r)
		return
	}

	var slug string

	jsonResponse := strings.HasPrefix(r.URL.Path, "/legal/v1/documents/")
	if jsonResponse {
		slug = strings.TrimPrefix(r.URL.Path, "/legal/v1/documents/")
	} else if strings.HasPrefix(r.URL.Path, "/legal/") {
		slug = strings.TrimPrefix(r.URL.Path, "/legal/")
	}

	if slug == "" || strings.Contains(slug, "/") {
		http.NotFound(w, r)
		return
	}

	version, err := versionNumber(r.URL.Query().Get("version"))
	if err != nil {
		h.fail(w, err)
		return
	}

	view, err := h.reader.GetDocument(r.Context(), slug, version, r.URL.Query().Get("lang"))
	if err != nil {
		h.fail(w, err)
		return
	}

	if jsonResponse {
		h.json(w, r, present(view))
		return
	}

	h.page(w, r, view)
}

func (h Handler) page(w http.ResponseWriter, r *http.Request, view domain.View) {
	content, err := Render(view.Text.Body)
	if err != nil {
		h.fail(w, err)
		return
	}

	var out bytes.Buffer

	page, err := template.New("document").Parse(pageTemplate)
	if err != nil {
		h.fail(w, err)
		return
	}
	// Render is the only source of trusted HTML; titles and links remain escaped.
	data := struct {
		Lang, Title, URL string
		Body             template.HTML
	}{
		view.Text.Lang,
		view.Text.Title,
		view.Text.URL,
		template.HTML(content), //nolint:gosec // Render sanitizes Markdown before this conversion.
	}
	if err := page.Execute(&out, data); err != nil {
		h.fail(w, err)
		return
	}

	w.Header().Set("Content-Security-Policy",
		"default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; "+
			"form-action 'none'; frame-ancestors "+h.ancestors)
	h.send(w, r, "text/html; charset=utf-8", out.Bytes())
}

func versionNumber(raw string) (int64, error) {
	if raw == "" {
		return 0, nil
	}

	version, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || version <= 0 {
		return 0, fmt.Errorf("%w: version must be a positive integer", domain.ErrInvalid)
	}

	return version, nil
}

func (h Handler) list(w http.ResponseWriter, r *http.Request) {
	audience := r.URL.Query().Get("audience")
	if audience != "" && audience != "all" && audience != "registration" {
		h.fail(w, fmt.Errorf("%w: unknown audience", domain.ErrInvalid))
		return
	}

	views, err := h.reader.ListDocuments(r.Context(), audience, r.URL.Query().Get("lang"))
	if err != nil {
		h.fail(w, err)
		return
	}

	out := make([]representation, 0, len(views))
	for i := range views {
		out = append(out, present(views[i]))
	}

	h.json(w, r, struct {
		Documents []representation `json:"documents"`
	}{out})
}

func (h Handler) json(w http.ResponseWriter, r *http.Request, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		h.fail(w, err)
		return
	}

	h.send(w, r, "application/json; charset=utf-8", data)
}

func (h Handler) send(w http.ResponseWriter, r *http.Request, kind string, data []byte) {
	sum := sha256.Sum256(data)
	etag := `"` + hex.EncodeToString(sum[:]) + `"`
	w.Header().Set("ETag", etag)
	// Revalidate current-version responses, including when a scheduled publication becomes effective.
	w.Header().Set("Cache-Control", "public, max-age=0, must-revalidate")
	w.Header().Set("Content-Type", kind)

	for _, candidate := range strings.Split(r.Header.Get("If-None-Match"), ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || strings.TrimPrefix(candidate, "W/") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}

	if r.Method == http.MethodHead {
		return
	}

	_, _ = w.Write(data)
}

func (Handler) fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrInvalid):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, domain.ErrNotFound):
		http.Error(w, "document not found", http.StatusNotFound)
	case errors.Is(err, domain.ErrConflict):
		http.Error(w, "document unavailable", http.StatusConflict)
	default:
		http.Error(w, "legal store unavailable", http.StatusServiceUnavailable)
	}
}

const pageTemplate = `<!doctype html><html lang="{{.Lang}}"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1"><title>{{.Title}}</title>
<style>:root{color-scheme:light dark}
body{font:17px/1.7 system-ui,sans-serif;max-width:52rem;margin:2rem auto;padding:0 16px;overflow-wrap:anywhere}
pre{overflow:auto}img{max-width:100%}a{color:LinkText}h1,h2,h3{line-height:1.3}table{display:block;overflow:auto}
</style></head><body><main><h1>{{.Title}}</h1>{{.Body}}
{{if .URL}}<p><a href="{{.URL}}" rel="noopener noreferrer">External document</a></p>{{end}}
</main></body></html>`
