// Package domain defines legal documents and immutable evidence encodings.
package domain

import "time"

type Text struct {
	Lang        string `json:"lang"`
	Title       string `json:"title"`
	Body        string `json:"body"`
	URL         string `json:"url"`
	ContentHash string `json:"content_hash"`
}
type Document struct {
	Slug        string `json:"slug"`
	Audience    string `json:"audience"`
	Required    bool   `json:"required"`
	DefaultLang string `json:"default_lang"`
}
type Version struct {
	Document    string    `json:"document"`
	Number      int64     `json:"number"`
	EffectiveAt time.Time `json:"effective_at"`
	ChangeNote  string    `json:"change_note"`
	Status      string    `json:"status"`
	Texts       []Text    `json:"texts"`
}
type View struct {
	Document
	Version          int64     `json:"version"`
	EffectiveAt      time.Time `json:"effective_at"`
	ChangeNote       string    `json:"change_note"`
	Text             Text      `json:"text"`
	Languages        []string  `json:"languages"`
	PreviousAccepted bool      `json:"previous_accepted"`
}
type Record struct {
	ID          string            `json:"id"`
	Seq         int64             `json:"seq"`
	Subject     string            `json:"subject"`
	Document    string            `json:"document"`
	Version     int64             `json:"version"`
	Lang        string            `json:"lang"`
	ContentHash string            `json:"content_hash"`
	Kind        string            `json:"kind"`
	AcceptedAt  time.Time         `json:"accepted_at"`
	Context     map[string]string `json:"context"`
	PrevHash    string            `json:"prev_hash"`
	Hash        string            `json:"hash"`
}
type Item struct {
	Document string `json:"document"`
	Version  int64  `json:"version"`
	Lang     string `json:"lang"`
}
type AcceptInput struct {
	Subject string            `json:"subject"`
	Items   []Item            `json:"items"`
	Context map[string]string `json:"context"`
}

// CheckResult is legal.Check's answer: OK when nothing is missing or
// rejected.
type CheckResult struct {
	OK bool `json:"ok"`
	// Missing are the required documents (current version) no item covers.
	Missing []View `json:"missing"`
	// Rejected are items naming no published, effective text.
	Rejected []Item `json:"rejected"`
}
type WithdrawInput struct {
	Subject  string            `json:"subject"`
	Document string            `json:"document"`
	Context  map[string]string `json:"context"`
}
type Filter struct {
	Subject  string    `json:"subject"`
	Document string    `json:"document"`
	Version  int64     `json:"version"`
	From     time.Time `json:"from"`
	To       time.Time `json:"to"`
}
type Verification struct {
	OK        bool  `json:"ok"`
	BrokenSeq int64 `json:"broken_seq"`
	Checked   int64 `json:"checked"`
}
