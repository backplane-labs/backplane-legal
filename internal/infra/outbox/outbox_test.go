package outbox_test

import (
	"context"
	"errors"
	"testing"

	"github.com/backplane-labs/backplane/pkg/backplane/backplanetest"

	"github.com/backplane-labs/backplane-legal/internal/domain"
	"github.com/backplane-labs/backplane-legal/internal/infra/outbox"
	legaleventsv1 "github.com/backplane-labs/backplane-legal/proto/legal/events/v1"
)

type source struct {
	ack    []int64
	events []domain.Event
}

func (s *source) PendingEvents(_ context.Context, _ int32) ([]domain.Event, error) {
	return s.events, nil
}
func (s *source) AckEvent(_ context.Context, id int64) error { s.ack = append(s.ack, id); return nil }
func TestPublishBeforeAck(t *testing.T) {
	t.Parallel()
	h := backplanetest.New(t, backplanetest.Name("legal"))
	st := &source{events: []domain.Event{{ID: 1, Name: "legal.Accepted", Payload: []byte(`{"subject":"user","document":"terms","version":1,"lang":"en","seq":12}`)}}}
	relay := outbox.New(h.Root(), st)
	// Drain is tested directly before lifecycle start to avoid a concurrent relay.
	boom := errors.New("broker unavailable")
	backplanetest.FailPublish(h, boom)

	if err := relay.Drain(t.Context()); err == nil {
		t.Fatal("publish failure not returned")
	}

	if len(st.ack) != 0 {
		t.Fatal("acknowledged undelivered event")
	}

	backplanetest.FailPublish(h, nil)

	if err := relay.Drain(t.Context()); err != nil {
		t.Fatal(err)
	}

	if len(st.ack) != 1 || st.ack[0] != 1 {
		t.Fatal("missing acknowledgement")
	}

	events := backplanetest.Events[*legaleventsv1.Accepted](h, relay.Accepted())
	if len(events) != 1 || events[0].GetSeq() != 12 {
		t.Fatalf("wrong payload: %+v", events)
	}
}

func TestMalformedEventIsNotAcknowledged(t *testing.T) {
	t.Parallel()
	h := backplanetest.New(t, backplanetest.Name("legal"))
	st := &source{events: []domain.Event{{ID: 1, Name: "legal.Accepted", Payload: []byte(`{"seq":"invalid"}`)}}}

	relay := outbox.New(h.Root(), st)
	if err := relay.Drain(t.Context()); err == nil || len(st.ack) != 0 {
		t.Fatal("malformed event lost")
	}
}
