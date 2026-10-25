package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"golang.org/x/text/language"
)

var (
	ErrInvalid  = errors.New("invalid legal input")
	ErrNotFound = errors.New("legal resource not found")
	ErrConflict = errors.New("legal state conflict")
	slugPattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
)

const (
	Draft                = "draft"
	Published            = "published"
	Accepted             = "accepted"
	Withdrawn            = "withdrawn"
	AudienceAll          = "all"
	AudienceRegistration = "registration"
)

// Event is a durable outbox message. Delivery is at least once; ID is its deduplication key.
type Event struct {
	ID      int64           `json:"id"`
	Name    string          `json:"name"`
	Payload json.RawMessage `json:"payload"`
}

// nilUUID is the id of an identity not stored yet (Kratos before it
// stores a registration): never a subject.
const nilUUID = "00000000-0000-0000-0000-000000000000"

// ValidSubject: a subject is opaque but present, and not the nil UUID,
// which names no one.
func ValidSubject(subject string) bool {
	subject = strings.TrimSpace(subject)

	return subject != "" && !strings.EqualFold(subject, nilUUID)
}

// ValidLanguage checks BCP 47 tags without restricting the service to known products.
func ValidLanguage(lang string) bool {
	if strings.TrimSpace(lang) == "" {
		return false
	}

	_, err := language.Parse(lang)

	return err == nil
}

func ValidateDocument(doc Document) error {
	if !slugPattern.MatchString(doc.Slug) || !ValidAudience(doc.Audience) || !ValidLanguage(doc.DefaultLang) {
		return fmt.Errorf("%w: slug, audience or default language", ErrInvalid)
	}

	return nil
}

func ValidAudience(audience string) bool {
	return audience == AudienceAll || audience == AudienceRegistration
}

func ValidateVersion(version Version) error {
	if !slugPattern.MatchString(version.Document) || version.Number < 0 ||
		len(version.Texts) == 0 || version.EffectiveAt.IsZero() {
		return fmt.Errorf("%w: document, version, effective date and texts required", ErrInvalid)
	}

	seen := make(map[string]bool, len(version.Texts))
	for _, text := range version.Texts {
		lang := strings.ToLower(text.Lang)
		if !validText(text) || seen[lang] {
			return fmt.Errorf("%w: invalid or duplicate language text", ErrInvalid)
		}

		seen[lang] = true

		if text.URL != "" {
			parsed, err := url.Parse(text.URL)
			if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
				return fmt.Errorf("%w: text URL must be absolute HTTP(S)", ErrInvalid)
			}
		}
	}

	return nil
}

// Publish returns a frozen copy; the draft and its text slice remain untouched.
func Publish(version Version) (Version, error) {
	if version.Status != Draft || version.Number < 1 {
		return Version{}, fmt.Errorf("%w: only a saved draft can publish", ErrConflict)
	}

	if err := ValidateVersion(version); err != nil {
		return Version{}, err
	}

	version.Texts = slices.Clone(version.Texts)
	version.EffectiveAt = version.EffectiveAt.UTC().Truncate(time.Microsecond)

	version.Status = Published
	for i := range version.Texts {
		version.Texts[i].ContentHash = ContentHash(version.Document, version.Number, version.Texts[i])
	}

	return version, nil
}

// ContentHash hashes lexicographically ordered JSON keys, UTF-8 strings and an integer version.
// This encoding is versioned by the service contract and independent of DB JSON formatting.
func ContentHash(slug string, number int64, text Text) string {
	return digest("", map[string]any{
		"slug": slug, "version": number, "lang": text.Lang,
		"title": text.Title, "body": text.Body, "url": text.URL,
	})
}

// RecordHash excludes hash itself and includes all evidence fields. Context keys are sorted by encoding/json.
func RecordHash(record Record) string {
	context := record.Context
	if context == nil {
		context = map[string]string{}
	}

	return digest(record.PrevHash, map[string]any{
		"id": record.ID, "seq": record.Seq, "subject": record.Subject, "document": record.Document,
		"version": record.Version, "lang": record.Lang, "content_hash": record.ContentHash, "kind": record.Kind,
		"accepted_at": record.AcceptedAt.UTC().Format(time.RFC3339Nano), "context": context, "prev_hash": record.PrevHash,
	})
}

func digest(prefix string, value map[string]any) string {
	// Only strings, integers and string maps enter these canonical encodings; Marshal cannot fail.
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}

	hash := sha256.Sum256(append([]byte(prefix), encoded...))

	return hex.EncodeToString(hash[:])
}

func Current(versions []Version, now time.Time) (Version, error) {
	var current Version
	for _, version := range versions {
		if version.Status == Published && !version.EffectiveAt.After(now) && version.Number > current.Number {
			current = version
		}
	}

	if current.Number == 0 {
		return Version{}, ErrNotFound
	}

	return current, nil
}

func Localize(doc Document, version Version, lang string) View {
	texts := slices.Clone(version.Texts)
	slices.SortFunc(texts, func(a, b Text) int { return strings.Compare(a.Lang, b.Lang) })

	view := View{
		Document: doc, Version: version.Number, EffectiveAt: version.EffectiveAt,
		ChangeNote: version.ChangeNote, Languages: make([]string, 0, len(texts)),
	}
	for _, text := range texts {
		view.Languages = append(view.Languages, text.Lang)
	}

	if len(texts) == 0 {
		return view
	}

	view.Text = texts[0]
	for _, candidate := range []string{lang, doc.DefaultLang} {
		for _, text := range texts {
			if strings.EqualFold(text.Lang, candidate) {
				view.Text = text
				return view
			}
		}
	}

	return view
}

// LatestState resolves withdrawals and re-acceptances by journal sequence, not request language.
func LatestState(records []Record, subject, document string, version int64) (Record, bool) {
	var latest Record

	for i := range records {
		record := records[i]
		if record.Subject == subject && record.Document == document &&
			(version == 0 || record.Version == version) && record.Seq > latest.Seq {
			latest = record
		}
	}

	return latest, latest.Seq != 0
}

// Verify validates links, payloads and the durable head, including missing first/last records.
func Verify(records []Record, headSeq int64, headHash string, from, until int64) Verification {
	result := Verification{OK: true}
	prev := ""
	next := int64(1)

	if from == 0 {
		from = 1
	}

	if until == 0 || until > headSeq {
		until = headSeq
	}

	if extraTail(records, headSeq, until) {
		return Verification{BrokenSeq: headSeq + 1}
	}

	for i := range records {
		record := records[i]
		if record.Seq > until {
			break
		}

		if record.Seq != next {
			return Verification{BrokenSeq: next, Checked: result.Checked}
		}

		if record.PrevHash != prev || RecordHash(record) != record.Hash {
			return Verification{BrokenSeq: record.Seq, Checked: result.Checked}
		}

		if record.Seq >= from {
			result.Checked++
		}

		prev = record.Hash
		next++
	}

	if next <= until {
		return Verification{BrokenSeq: next, Checked: result.Checked}
	}

	if until == headSeq && prev != headHash {
		return Verification{BrokenSeq: headSeq, Checked: result.Checked}
	}

	return result
}

func validText(text Text) bool {
	return ValidLanguage(text.Lang) && strings.TrimSpace(text.Title) != "" &&
		(strings.TrimSpace(text.Body) != "" || text.URL != "")
}

func extraTail(records []Record, headSeq, until int64) bool {
	return until == headSeq && len(records) > 0 && records[len(records)-1].Seq > headSeq
}
