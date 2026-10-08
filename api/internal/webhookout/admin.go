package webhookout

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/uptrace/bun"

	"github.com/kwaabs/voltedge/api/internal/secretbox"
)

// This file is the admin surface S3 drives (via the webhook admin handler, which
// may import this package -- webhookout imports neither webhook nor the domain
// packages, so there is no cycle): a synthetic test-ping, the outbound delivery
// log, and a replay.

// pingEventType is the event_type of a test ping -- a reserved, synthetic event a
// subscriber can recognise and ignore (it corresponds to no real change).
const pingEventType = "voltedge.ping"

// PingResult is the outcome of a test-ping: the URL it hit, the subscriber's HTTP
// status, and whether it was accepted. A failed ping is NOT an error -- it is a
// result the admin reads to see why the connection does not work.
type PingResult struct {
	TargetURL  string `json:"target_url"`
	StatusCode int    `json:"status_code,omitempty"`
	Delivered  bool   `json:"delivered"`
	Error      string `json:"error,omitempty"`
}

// Ping sends a synthetic, signed event to a subscription's target_url right now,
// synchronously, and reports the outcome -- the outbound analogue of the inbound
// "Pull now" test, and the "does this connection work" check. It persists no
// delivery row and schedules no retry; it is a one-shot probe.
func Ping(ctx context.Context, db bun.IDB, cipher *secretbox.Cipher, subscriptionID string) (*PingResult, error) {
	sub, err := getSubscription(ctx, db, subscriptionID)
	if err != nil {
		return nil, fmt.Errorf("webhookout: loading subscription %s: %w", subscriptionID, err)
	}
	cfg, _ := sub.parseConfig() // a bad config surfaces as a send error below

	id := randomDeliveryID()
	body, err := json.Marshal(envelope{
		EventID:    id,
		EventType:  pingEventType,
		OccurredAt: time.Now().UTC(),
		Entity:     entityRef{Kind: "ping", ID: id},
		Data:       json.RawMessage(`{"message":"VoltEdge outbound webhook test ping"}`),
	})
	if err != nil {
		return nil, fmt.Errorf("webhookout: building ping envelope: %w", err)
	}

	res := &PingResult{TargetURL: cfg.TargetURL}
	code, serr := sendDelivery(ctx, db, cipher, &OutboundDelivery{
		ID:             id,
		SubscriptionID: subscriptionID,
		EventType:      pingEventType,
		Payload:        body,
	})
	res.StatusCode = code
	if serr != nil {
		res.Error = serr.Error()
	} else {
		res.Delivered = true
	}
	return res, nil
}

// DeliveryView is one row of the admin delivery log (json-tagged, unlike the bun
// OutboundDelivery model). The payload is omitted from the list -- the log shows
// status, timing and errors; the full envelope is large and rarely needed there.
type DeliveryView struct {
	ID            string     `json:"id"`
	EventType     string     `json:"event_type"`
	EntityKind    string     `json:"entity_kind"`
	EntityID      string     `json:"entity_id"`
	Status        string     `json:"status"`
	Attempts      int        `json:"attempts"`
	ResponseCode  *int       `json:"response_code,omitempty"`
	Error         *string    `json:"error,omitempty"`
	NextAttemptAt time.Time  `json:"next_attempt_at"`
	CreatedAt     time.Time  `json:"created_at"`
	FinishedAt    *time.Time `json:"finished_at,omitempty"`
}

// ListDeliveries returns a subscription's most recent deliveries, newest first.
func ListDeliveries(ctx context.Context, db bun.IDB, subscriptionID string, limit int) ([]DeliveryView, error) {
	var rows []OutboundDelivery
	err := db.NewSelect().Model(&rows).
		Where("subscription_id = ?", subscriptionID).
		OrderExpr("created_at DESC").
		Limit(limit).
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("webhookout: list deliveries: %w", err)
	}
	out := make([]DeliveryView, 0, len(rows))
	for i := range rows {
		d := &rows[i]
		out = append(out, DeliveryView{
			ID:            d.ID,
			EventType:     d.EventType,
			EntityKind:    d.EntityKind,
			EntityID:      d.EntityID,
			Status:        d.Status,
			Attempts:      d.Attempts,
			ResponseCode:  d.ResponseCode,
			Error:         d.Error,
			NextAttemptAt: d.NextAttemptAt,
			CreatedAt:     d.CreatedAt,
			FinishedAt:    d.FinishedAt,
		})
	}
	return out, nil
}

// ReplayDelivery re-queues a TERMINAL delivery (dead or succeeded) for the worker
// to send again, with a fresh attempt budget. Reports whether a matching terminal
// delivery was found (false -> the handler returns a clean 404/400). A delivery
// still queued/running is left alone -- it is already on its way.
func ReplayDelivery(ctx context.Context, db bun.IDB, subscriptionID, deliveryID string) (bool, error) {
	res, err := db.NewUpdate().Model((*OutboundDelivery)(nil)).
		Set("status = 'queued'").
		Set("attempts = 0").
		Set("next_attempt_at = now()").
		Set("error = NULL").
		Set("response_code = NULL").
		Set("worker_id = NULL").
		Set("lease_expires_at = NULL").
		Set("finished_at = NULL").
		Where("id = ?", deliveryID).
		Where("subscription_id = ?", subscriptionID).
		Where("status IN ('dead', 'succeeded')").
		Exec(ctx)
	if err != nil {
		return false, fmt.Errorf("webhookout: replay delivery: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("webhookout: replay delivery: %w", err)
	}
	return n > 0, nil
}

// randomDeliveryID returns a random 128-bit hex id for a ping's synthetic
// delivery (which is never persisted, so it only needs to be unique on the wire
// as the X-VoltEdge-Delivery header).
func randomDeliveryID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
