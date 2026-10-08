package webhookout

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/uptrace/bun"
)

// A Subscription is an ops.webhook_endpoints row with direction = 'outbound'
// (migration 0134). We map the SAME table the inbound engine's Endpoint maps,
// but only the columns the outbound pipeline reads -- deliberately a separate,
// narrow struct so this package never imports internal/webhook (which pulls in
// oms/wam/faults and would cycle: those packages import webhookout for Emit).
type Subscription struct {
	bun.BaseModel `bun:"table:ops.webhook_endpoints,alias:webhook_endpoints"`

	ID               string          `bun:"id,pk"`
	Name             string          `bun:"name"`
	Active           bool            `bun:"active"`
	Direction        string          `bun:"direction"`
	SigningSecretEnc []byte          `bun:"signing_secret_enc"`
	Config           json.RawMessage `bun:"config"`
}

// outboundConfig is the outbound slice of an endpoint's config jsonb (design
// doc §3). target_url is where the event is POSTed; event_types is the allow-list
// a subscription hears (empty = all); class_filter narrows feature events to named
// classes; territory_id scopes to one territory; mapping (S4) is an optional spec
// that reshapes the envelope into the receiver's own shape before sending.
type outboundConfig struct {
	TargetURL   string          `json:"target_url"`
	EventTypes  []string        `json:"event_types"`
	ClassFilter []string        `json:"class_filter"`
	TerritoryID string          `json:"territory_id"`
	Mapping     json.RawMessage `json:"mapping"`
}

// parseConfig reads the outbound slice of a subscription's config jsonb.
func (s *Subscription) parseConfig() (outboundConfig, error) {
	var c outboundConfig
	if len(s.Config) == 0 {
		return c, nil
	}
	if err := json.Unmarshal(s.Config, &c); err != nil {
		return c, fmt.Errorf("webhookout: parsing subscription %s config: %w", s.ID, err)
	}
	return c, nil
}

// matches reports whether this subscription should receive the given event: its
// event_types allow-list must include the type (empty = all), its class_filter
// (when set) must include a feature event's class, and its territory_id (when set)
// must equal the event's territory.
func (c outboundConfig) matches(eventType, featureClass, territoryID string) bool {
	if len(c.EventTypes) > 0 && !contains(c.EventTypes, eventType) {
		return false
	}
	// class_filter only constrains events that actually carry a class; an
	// operational event (no class) is never excluded by it.
	if featureClass != "" && len(c.ClassFilter) > 0 && !contains(c.ClassFilter, featureClass) {
		return false
	}
	// territory_id is fail-closed, mirroring Phase 58 read scope: a territory-
	// scoped subscription hears an event ONLY when it carries that exact territory.
	// An event with no territory (operational events, geometry-less features) never
	// matches a territory-scoped subscription.
	if c.TerritoryID != "" && territoryID != c.TerritoryID {
		return false
	}
	return true
}

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

// getSubscription loads one subscription by id (the delivery worker needs its
// target_url + signing secret at send time). A missing row (a subscription
// deleted after its deliveries were fanned out, though the FK cascade normally
// removes those too) is returned as sql.ErrNoRows for the caller to classify.
func getSubscription(ctx context.Context, db bun.IDB, id string) (*Subscription, error) {
	s := new(Subscription)
	if err := db.NewSelect().Model(s).Where("id = ?", id).Scan(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// activeSubscriptions loads every enabled outbound subscription. The fan-out
// evaluates each against an event; the set is small (admin-configured endpoints)
// so loading all of them per fan-out is cheap and always current.
func activeSubscriptions(ctx context.Context, db bun.IDB) ([]Subscription, error) {
	var subs []Subscription
	err := db.NewSelect().Model(&subs).
		Where("direction = ?", "outbound").
		Where("active").
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("webhookout: loading outbound subscriptions: %w", err)
	}
	return subs, nil
}
