package ticket

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

// RequestHash is what a store keeps beside an idempotency key: the sha256,
// in hex, of op, a zero byte and req as JSON. A retry under the key
// replays only when its op and hash match. Stored keys hold this hash, so
// its bytes must never change.
func RequestHash(op string, req any) (string, error) {
	reqJSON, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(append([]byte(op+"\x00"), reqJSON...))
	return hex.EncodeToString(sum[:]), nil
}

// IdempotencyKeep is how long a store keeps a key: a reap deletes keys
// older than this, and a retry after that runs its write again. A run's
// files, whose pickups replay keyed writes, stay 7 days after its ack;
// twice that errs long.
const IdempotencyKeep = 14 * 24 * time.Hour
