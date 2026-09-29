package record

import (
	"encoding/json"

	"github.com/klauspost/compress/zstd"
	"github.com/rodneyosodo/clacks/internal/crypto"
)

// Change is a single row mutation synced between machines.
type Change struct {
	Table       string         `json:"table"`
	PK          string         `json:"pk"`
	TimeUpdated int64          `json:"time_updated"`
	Columns     map[string]any `json:"columns,omitempty"`
	Tombstone   bool           `json:"tombstone,omitempty"`
	Version     string         `json:"version,omitempty"`
}

// Payload is the decrypted body of a Record: a batch of changes.
type Payload struct {
	Changes []Change `json:"changes"`
}

// MaxBatchRows and MaxBatchBytes bound record sizes (~1MB / 500 rows).
const (
	MaxBatchRows  = 500
	MaxBatchBytes = 1 << 20
)

// EncodePayload marshals, compresses and encrypts changes for a record.
func EncodePayload(key [32]byte, aad []byte, changes []Change) (nonce, data []byte, err error) {
	p := Payload{Changes: changes}
	raw, err := json.Marshal(p)
	if err != nil {
		return nil, nil, err
	}
	enc, err := zstd.NewWriter(nil)
	if err != nil {
		return nil, nil, err
	}
	compressed := enc.EncodeAll(raw, nil)
	_ = enc.Close()
	return crypto.Encrypt(key, aad, compressed)
}

// DecodePayload decrypts and decompresses a record body.
func DecodePayload(key [32]byte, aad, nonce, data []byte) ([]Change, error) {
	compressed, err := crypto.Decrypt(key, aad, nonce, data)
	if err != nil {
		return nil, err
	}
	dec, err := zstd.NewReader(nil)
	if err != nil {
		return nil, err
	}
	defer dec.Close()
	raw, err := dec.DecodeAll(compressed, nil)
	if err != nil {
		return nil, err
	}
	var p Payload
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	return p.Changes, nil
}

// BatchChanges splits changes into ~1MB / 500-row batches.
func BatchChanges(changes []Change) [][]Change {
	var out [][]Change
	for len(changes) > 0 {
		n := len(changes)
		if n > MaxBatchRows {
			n = MaxBatchRows
		}
		// Refine by encoded size.
		end := n
		for end > 1 {
			raw, err := json.Marshal(Payload{Changes: changes[:end]})
			if err != nil || len(raw) <= MaxBatchBytes {
				break
			}
			end /= 2
		}
		out = append(out, changes[:end])
		changes = changes[end:]
	}
	return out
}
