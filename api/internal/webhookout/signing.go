package webhookout

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"time"
)

// Header names on an outbound delivery. These mirror the inbound receiver's
// (api/internal/webhook/receiver.go) so a VoltEdge instance can subscribe to
// another VoltEdge's outbound webhooks and verify them with the same code.
const (
	eventHeader     = "X-VoltEdge-Event"     // the event_type
	deliveryHeader  = "X-VoltEdge-Delivery"  // the delivery id == idempotency key
	timestampHeader = "X-VoltEdge-Timestamp" // unix seconds, part of the signed string
	signatureHeader = "X-VoltEdge-Signature" // sha256=<hex HMAC>
)

// signBody produces the X-VoltEdge-Signature value for a body under secret, using
// the EXACT scheme the inbound receiver verifies: hex(HMAC-SHA256(secret,
// ts + "." + body)), prefixed "sha256=". ts is the same unix-seconds string sent
// in X-VoltEdge-Timestamp, so the receiver can reconstruct the signed string and
// reject replays outside its window. Returns the signature and the timestamp it
// was computed over (both must travel together).
func signBody(secret, body []byte) (signature, timestamp string) {
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(ts))
	mac.Write([]byte("."))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil)), ts
}
