package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/backplane-labs/backplane-legal/internal/domain"
)

func TestLifecycleAndLanguage(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC().Truncate(time.Microsecond)
	draft := domain.Version{Document: "privacy", Number: 1, Status: domain.Draft, EffectiveAt: now, Texts: []domain.Text{{Lang: "en", Title: "Privacy", Body: "Text"}, {Lang: "ru", Title: "Policy", Body: "Other"}}}

	published, err := domain.Publish(draft)
	if err != nil {
		t.Fatal(err)
	}

	if draft.Texts[0].ContentHash != "" || published.Texts[0].ContentHash == "" {
		t.Fatal("publish must clone texts and hash content")
	}

	if _, err = domain.Publish(published); !errors.Is(err, domain.ErrConflict) {
		t.Fatal(err)
	}

	future := published
	future.Number = 2
	future.EffectiveAt = now.Add(time.Hour)

	current, err := domain.Current([]domain.Version{published, future}, now)
	if err != nil || current.Number != 1 {
		t.Fatal(current, err)
	}

	doc := domain.Document{Slug: "privacy", DefaultLang: "ru"}
	if got := domain.Localize(doc, published, "de"); got.Text.Lang != "ru" {
		t.Fatal(got)
	}

	if got := domain.Localize(doc, published, "en"); got.Text.Lang != "en" {
		t.Fatal(got)
	}
}

func TestHashesAndVerification(t *testing.T) {
	t.Parallel()

	first := domain.Record{ID: "1", Seq: 1, Subject: "a", Document: "privacy", Version: 1, Lang: "en", Kind: domain.Accepted, AcceptedAt: time.Unix(1, 0), Context: map[string]string{"ip": "a", "device": "b"}}

	first.Hash = domain.RecordHash(first)
	if first.Hash != "e1fac4dfb4011c5921113287a9768655225410154ba881345d3708057a8ce579" {
		t.Fatal("canonical record encoding changed", first.Hash)
	}

	reordered := first

	reordered.Context = map[string]string{"device": "b", "ip": "a"}
	if domain.RecordHash(reordered) != first.Hash {
		t.Fatal("context ordering changed hash")
	}

	second := first
	second.ID = "2"
	second.Seq = 2
	second.PrevHash = first.Hash
	second.Hash = domain.RecordHash(second)

	records := []domain.Record{first, second}
	if got := domain.Verify(records, 2, second.Hash, 0, 0); !got.OK || got.Checked != 2 {
		t.Fatal(got)
	}

	if got := domain.Verify(records[1:], 2, second.Hash, 0, 0); got.OK || got.BrokenSeq != 1 {
		t.Fatal(got)
	}

	if got := domain.Verify(records[:1], 2, second.Hash, 0, 0); got.OK || got.BrokenSeq != 2 {
		t.Fatal(got)
	}

	records[1].Subject = "tampered"
	if got := domain.Verify(records, 2, second.Hash, 2, 2); got.OK || got.BrokenSeq != 2 {
		t.Fatal(got)
	}
}

func TestContentHashCanonicalVector(t *testing.T) {
	t.Parallel()

	text := domain.Text{Lang: "en", Title: "Terms", Body: "Read this."}

	hash := domain.ContentHash("terms", 1, text)
	if hash != "8fe96cce4b68c913308fb63a8eb400423ef1554d37b49c84998ac443d1b5497b" {
		t.Fatal("canonical text encoding changed", hash)
	}

	text.ContentHash = "ignored"
	if domain.ContentHash("terms", 1, text) != hash {
		t.Fatal("hash included itself")
	}

	if domain.ContentHash("terms", 2, text) == hash {
		t.Fatal("version omitted from hash")
	}
}
