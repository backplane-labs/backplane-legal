// Package outbox relays committed legal facts to the SDK event transport.
package outbox

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/backplane-labs/backplane/pkg/backplane/deps"
	"github.com/backplane-labs/backplane/pkg/backplane/event"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/gopherex/xlog"

	"github.com/backplane-labs/backplane-legal/internal/domain"
	legaleventsv1 "github.com/backplane-labs/backplane-legal/proto/legal/events/v1"
)

// Source owns durable messages; acknowledgement follows confirmed publication.
type Source interface {
	PendingEvents(ctx context.Context, limit int32) ([]domain.Event, error)
	AckEvent(ctx context.Context, id int64) error
}

// Relay publishes typed events with stable IDs, retaining messages on any failure.
type Relay struct {
	deps.Component
	source    Source
	published event.Ref[*legaleventsv1.Published]
	accepted  event.Ref[*legaleventsv1.Accepted]
	withdrawn event.Ref[*legaleventsv1.Withdrawn]
}

// New declares events and attaches a cancellable SDK-managed relay loop.
func New(parent deps.Scope, source Source) *Relay {
	relay := &Relay{Component: deps.NewComponent(parent, "outbox"), source: source}
	relay.published = event.Declare[*legaleventsv1.Published](relay, "Published",
		event.Describe("a legal version was published and frozen"))
	relay.accepted = event.Declare[*legaleventsv1.Accepted](relay, "Accepted",
		event.Describe("a subject accepted an exact legal text"))
	relay.withdrawn = event.Declare[*legaleventsv1.Withdrawn](relay, "Withdrawn",
		event.Describe("a subject withdrew optional consent"))
	relay.Go(relay.Run)

	return relay
}

// Accepted exposes the typed event contract for SDK harness consumers.
func (r *Relay) Accepted() event.Ref[*legaleventsv1.Accepted] { return r.accepted }

// Run retries durable delivery without failing the service when NATS is down.
func (r *Relay) Run(ctx context.Context) error {
	const interval = time.Second

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		if err := r.Drain(ctx); err != nil && ctx.Err() == nil {
			r.Log().Warn("legal outbox delivery deferred", xlog.Err(err))
		}

		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// Drain publishes one bounded batch. Concurrent replicas may publish duplicates,
// but each message keeps the same ID and is never acknowledged before delivery.
func (r *Relay) Drain(ctx context.Context) error {
	const batchSize = 100

	events, err := r.source.PendingEvents(ctx, batchSize)
	if err != nil {
		return fmt.Errorf("read legal outbox: %w", err)
	}

	for i := range events {
		if err := r.publish(ctx, events[i]); err != nil {
			return err
		}

		if err := r.source.AckEvent(ctx, events[i].ID); err != nil {
			return fmt.Errorf("ack legal outbox: %w", err)
		}
	}

	return nil
}

func (r *Relay) publish(ctx context.Context, message domain.Event) error {
	options := []event.PublishOption{event.ID("legal-outbox/" + strconv.FormatInt(message.ID, 10))}
	switch strings.TrimPrefix(message.Name, "legal.") {
	case "Published":
		value := &legaleventsv1.Published{}
		if err := protojson.Unmarshal(message.Payload, value); err != nil {
			return fmt.Errorf("decode Published: %w", err)
		}

		options = append(options, event.Key(value.GetDocument()))
		if err := r.published.Publish(ctx, value, options...); err != nil {
			return fmt.Errorf("publish Published: %w", err)
		}
	case "Accepted":
		value := &legaleventsv1.Accepted{}
		if err := protojson.Unmarshal(message.Payload, value); err != nil {
			return fmt.Errorf("decode Accepted: %w", err)
		}

		options = append(options, event.Key(value.GetSubject()))
		if err := r.accepted.Publish(ctx, value, options...); err != nil {
			return fmt.Errorf("publish Accepted: %w", err)
		}
	case "Withdrawn":
		value := &legaleventsv1.Withdrawn{}
		if err := protojson.Unmarshal(message.Payload, value); err != nil {
			return fmt.Errorf("decode Withdrawn: %w", err)
		}

		options = append(options, event.Key(value.GetSubject()))
		if err := r.withdrawn.Publish(ctx, value, options...); err != nil {
			return fmt.Errorf("publish Withdrawn: %w", err)
		}
	default:
		return fmt.Errorf("%w: unknown outbox event %q", domain.ErrInvalid, message.Name)
	}

	return nil
}
