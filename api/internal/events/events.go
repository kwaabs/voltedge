// Package events is a tiny in-process Server-Sent-Events hub for pushing live
// notifications to a user's open browser tabs -- today, "a work order was just
// assigned to you", so the activity bell and Works board update instantly instead
// of waiting for their poll.
//
// Deliberately in-process and un-persisted. An event is a hint to refetch, never
// the source of truth: the client still polls, so a missed event (a slow tab, or
// -- in a multi-replica deployment -- an assignment made on a DIFFERENT replica
// than the one holding this user's stream) is reconciled within the poll window.
// That lets this stay a thread-safe map with no broker. A Postgres LISTEN/NOTIFY
// bridge is the follow-up IF cross-replica push ever needs to be instant too.
package events

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/kwaabs/voltedge/api/internal/auth"
)

// Hub fans events out to the live SSE connections of each user.
type Hub struct {
	mu   sync.Mutex
	subs map[string]map[chan []byte]struct{} // userID -> set of per-connection channels
}

// NewHub returns an empty hub.
func NewHub() *Hub {
	return &Hub{subs: map[string]map[chan []byte]struct{}{}}
}

// defaultHub is the process-wide hub; the package-level Handler/Publish use it so
// callers don't have to thread an instance through every constructor.
var defaultHub = NewHub()

// Handler streams the caller's events as SSE, using the default hub.
func Handler() http.HandlerFunc { return defaultHub.Handler() }

// Publish delivers one event to the user's live connections, via the default hub.
func Publish(userID, eventType string, data any) { defaultHub.Publish(userID, eventType, data) }

// Broadcast delivers one event to EVERY live connection, via the default hub -- for
// a shared control-room surface (the alarm annunciator) that every operator sees,
// not a per-user notification.
func Broadcast(eventType string, data any) { defaultHub.Broadcast(eventType, data) }

// subscribe registers a connection for userID and returns it plus an unsubscribe
// func (which the connection's handler must defer).
func (h *Hub) subscribe(userID string) (chan []byte, func()) {
	// Small buffer: a slow client drops events rather than blocking a publisher.
	ch := make(chan []byte, 8)
	h.mu.Lock()
	conns := h.subs[userID]
	if conns == nil {
		conns = map[chan []byte]struct{}{}
		h.subs[userID] = conns
	}
	conns[ch] = struct{}{}
	h.mu.Unlock()

	return ch, func() {
		h.mu.Lock()
		if conns := h.subs[userID]; conns != nil {
			delete(conns, ch)
			if len(conns) == 0 {
				delete(h.subs, userID)
			}
		}
		h.mu.Unlock()
		close(ch)
	}
}

// Publish delivers one event to every live connection of userID. Non-blocking: a
// connection whose buffer is full simply misses this event (the poll reconciles),
// so one stalled browser never backs a publisher up. A send and the unsubscribe's
// close can't race -- both take h.mu, and unsubscribe removes the channel from the
// map before closing it, so Publish never sees a closed channel.
func (h *Hub) Publish(userID, eventType string, data any) {
	if userID == "" {
		return
	}
	payload, err := json.Marshal(map[string]any{"type": eventType, "data": data})
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs[userID] {
		select {
		case ch <- payload:
		default:
		}
	}
}

// Broadcast delivers one event to every live connection of every user. Non-blocking
// (a full buffer drops the event, reconciled by the client's poll), same as Publish.
// For a shared surface -- the alarm annunciator -- where every operator's view updates
// on any change, rather than a message aimed at one user.
func (h *Hub) Broadcast(eventType string, data any) {
	payload, err := json.Marshal(map[string]any{"type": eventType, "data": data})
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, conns := range h.subs {
		for ch := range conns {
			select {
			case ch <- payload:
			default:
			}
		}
	}
}

// Handler streams one user's events as Server-Sent Events. It runs behind the
// global, non-failing SessionLoader, so it checks the session itself and 401s an
// anonymous request; no RequirePermission, since a user only ever subscribes to
// their OWN id.
func (h *Hub) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFrom(r.Context())
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		// Tell a reverse proxy (nginx/Coolify) not to buffer the stream.
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		// Open the stream immediately so the client's onopen fires.
		writeAndFlush(w, flusher, []byte(": connected\n\n"))

		ch, unsubscribe := h.subscribe(user.ID)
		defer unsubscribe()

		// Periodic comment keeps the connection (and any idle-timeout proxy) alive.
		ping := time.NewTicker(25 * time.Second)
		defer ping.Stop()

		ctx := r.Context()
		for {
			select {
			case <-ctx.Done():
				return
			case payload, ok := <-ch:
				if !ok {
					return
				}
				writeAndFlush(w, flusher, append(append([]byte("data: "), payload...), '\n', '\n'))
			case <-ping.C:
				writeAndFlush(w, flusher, []byte(": ping\n\n"))
			}
		}
	}
}

func writeAndFlush(w http.ResponseWriter, f http.Flusher, b []byte) {
	// A write error means the client is gone; the next loop sees ctx.Done and the
	// handler returns, so the error itself needs no handling here.
	if _, err := w.Write(b); err == nil {
		f.Flush()
	}
}
