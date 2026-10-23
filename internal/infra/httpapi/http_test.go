package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/backplane-labs/backplane-legal/internal/domain"
	"github.com/backplane-labs/backplane-legal/internal/infra/httpapi"
)

type reader struct{ err error }

func (r reader) GetDocument(context.Context, string, int64, string) (domain.View, error) {
	if r.err != nil {
		return domain.View{}, r.err
	}

	return domain.View{Document: domain.Document{Slug: "privacy", Audience: "all", Required: true}, Version: 1, EffectiveAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Text: domain.Text{Lang: "en", Title: `<privacy>`, Body: "# Hello\n\n<script>alert(1)</script>\n\n[bad](javascript:alert(1))", ContentHash: "abc"}, Languages: []string{"en"}}, nil
}

func (r reader) ListDocuments(ctx context.Context, _, lang string) ([]domain.View, error) {
	v, e := r.GetDocument(ctx, "privacy", 0, lang)
	return []domain.View{v}, e
}

func TestPublicRepresentations(t *testing.T) {
	t.Parallel()

	h := httpapi.New(reader{}, []string{"https://example.com"})

	for _, path := range []string{"/legal/v1/documents", "/legal/v1/documents/privacy", "/legal/privacy"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()

			res := httptest.NewRecorder()
			h.ServeHTTP(res, httptest.NewRequest(http.MethodGet, path, http.NoBody))

			if res.Code != http.StatusOK {
				t.Fatalf("%d: %s", res.Code, res.Body.String())
			}

			if res.Header().Get("ETag") == "" || res.Header().Get("Cache-Control") == "" {
				t.Fatal("missing cache headers")
			}

			req := httptest.NewRequest(http.MethodGet, path, http.NoBody)
			req.Header.Set("If-None-Match", res.Header().Get("ETag"))

			cached := httptest.NewRecorder()
			h.ServeHTTP(cached, req)

			if cached.Code != http.StatusNotModified || cached.Body.Len() != 0 {
				t.Fatal("conditional request not honored")
			}

			if path == "/legal/privacy" {
				body := res.Body.String()
				if strings.Contains(body, "<script") || strings.Contains(body, "javascript:") || !strings.Contains(body, "&lt;privacy&gt;") || !strings.Contains(body, `<html lang="en">`) {
					t.Fatalf("unsafe HTML: %s", body)
				}

				if !strings.Contains(res.Header().Get("Content-Security-Policy"), "frame-ancestors https://example.com") {
					t.Fatal("missing CSP")
				}
			} else if !json.Valid(res.Body.Bytes()) {
				t.Fatal("invalid JSON")
			}
		})
	}
}

func TestRejectInvalidQueries(t *testing.T) {
	t.Parallel()

	h := httpapi.New(reader{}, []string{"*"})

	for _, path := range []string{"/legal/v1/documents/privacy?version=no", "/legal/v1/documents/privacy?version=-1", "/legal/v1/documents?audience=other"} {
		res := httptest.NewRecorder()
		h.ServeHTTP(res, httptest.NewRequest(http.MethodGet, path, http.NoBody))

		if res.Code != 400 {
			t.Fatalf("%s: %d", path, res.Code)
		}
	}
}

func TestJSONFields(t *testing.T) {
	t.Parallel()

	h := httpapi.New(reader{}, []string{"*"})

	for _, path := range []string{"/legal/v1/documents", "/legal/v1/documents/privacy"} {
		res := httptest.NewRecorder()
		h.ServeHTTP(res, httptest.NewRequest(http.MethodGet, path, http.NoBody))

		var document map[string]json.RawMessage
		if path == "/legal/v1/documents" {
			var envelope map[string][]map[string]json.RawMessage
			if err := json.Unmarshal(res.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}

			if len(envelope) != 1 || len(envelope["documents"]) != 1 {
				t.Fatalf("wrong envelope: %s", res.Body.String())
			}

			document = envelope["documents"][0]
		} else if err := json.Unmarshal(res.Body.Bytes(), &document); err != nil {
			t.Fatal(err)
		}

		fields := make([]string, 0, len(document))
		for field := range document {
			fields = append(fields, field)
		}

		slices.Sort(fields)

		want := []string{"audience", "body", "change_note", "content_hash", "default_lang", "effective_at", "lang", "languages", "required", "slug", "text_url", "title", "url", "version"}
		if !reflect.DeepEqual(fields, want) {
			t.Fatalf("fields: %v", fields)
		}

		if string(document["slug"]) != `"privacy"` || string(document["version"]) != "1" || string(document["content_hash"]) != `"abc"` || string(document["text_url"]) != `"/legal/privacy?lang=en\u0026version=1"` {
			t.Fatalf("wrong document: %s", res.Body.String())
		}
	}
}

func TestMethodsAndCacheVariants(t *testing.T) {
	t.Parallel()

	h := httpapi.New(reader{}, []string{"*"})

	for _, path := range []string{"/legal/v1/documents", "/legal/v1/documents/privacy", "/legal/privacy"} {
		get := httptest.NewRecorder()
		h.ServeHTTP(get, httptest.NewRequest(http.MethodGet, path, http.NoBody))

		etag := get.Header().Get("ETag")
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			for _, candidate := range []string{"", etag, "W/" + etag, "*", `"other", W/` + etag, `"other"`} {
				req := httptest.NewRequest(method, path, http.NoBody)
				req.Header.Set("If-None-Match", candidate)

				res := httptest.NewRecorder()
				h.ServeHTTP(res, req)

				want := http.StatusNotModified
				if candidate == "" || candidate == `"other"` {
					want = http.StatusOK
				}

				if res.Code != want || res.Header().Get("ETag") != etag || res.Header().Get("Cache-Control") != get.Header().Get("Cache-Control") {
					t.Fatalf("%s %s candidate %q: %d", method, path, candidate, res.Code)
				}

				if (method == http.MethodHead || want == http.StatusNotModified) && res.Body.Len() != 0 {
					t.Fatal("unexpected response body")
				}
			}
		}

		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodOptions} {
			res := httptest.NewRecorder()
			h.ServeHTTP(res, httptest.NewRequest(method, path, http.NoBody))

			if res.Code != http.StatusMethodNotAllowed || res.Header().Get("Allow") != "GET, HEAD" {
				t.Fatalf("method %s: %d", method, res.Code)
			}
		}
	}
}

func TestNotFoundAndReaderErrors(t *testing.T) {
	t.Parallel()

	for _, path := range []string{"/", "/other", "/legal/", "/legal/privacy/extra", "/legal/v1/documents/"} {
		res := httptest.NewRecorder()
		httpapi.New(reader{}, []string{"*"}).ServeHTTP(res, httptest.NewRequest(http.MethodGet, path, http.NoBody))

		if res.Code != http.StatusNotFound {
			t.Fatalf("%s: %d", path, res.Code)
		}
	}

	for _, tc := range []struct {
		err  error
		code int
	}{
		{domain.ErrNotFound, http.StatusNotFound},
		{domain.ErrInvalid, http.StatusBadRequest},
		{domain.ErrConflict, http.StatusConflict},
		{errors.New("private database details"), http.StatusServiceUnavailable},
	} {
		for _, path := range []string{"/legal/v1/documents", "/legal/v1/documents/missing", "/legal/missing"} {
			res := httptest.NewRecorder()
			httpapi.New(reader{err: tc.err}, []string{"*"}).ServeHTTP(res, httptest.NewRequest(http.MethodGet, path, http.NoBody))

			if res.Code != tc.code || strings.Contains(res.Body.String(), "private database") {
				t.Fatalf("%s: %d %s", path, res.Code, res.Body.String())
			}
		}
	}
}

func TestFrameAncestors(t *testing.T) {
	t.Parallel()

	for _, sources := range [][]string{{"*"}, {"'self'"}, {"'none'"}, {"https://example.com", "http://localhost:8080/", "'self'"}} {
		if err := httpapi.ValidateAncestors(sources); err != nil {
			t.Fatalf("%v: %v", sources, err)
		}
	}

	for _, sources := range [][]string{nil, {}, {"'none'", "'self'"}, {"https://a.example;script-src"}, {"https://a.example\n"}, {"https://a.example path"}, {"https://user@a.example"}, {"https://a.example/path"}, {"https://a.example?query"}, {"https://a.example#fragment"}, {"javascript:alert(1)"}, {"//a.example"}, {"https:"}, {"https://%"}} {
		if err := httpapi.ValidateAncestors(sources); !errors.Is(err, domain.ErrInvalid) {
			t.Fatalf("%v: %v", sources, err)
		}
	}
}
