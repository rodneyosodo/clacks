package record

import (
	"errors"
	"sync"

	"github.com/goccy/go-json"
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

// Constructing a zstd Encoder or Decoder allocates window buffers and hash
// tables: measured at ~5ms per record against ~0.35ms for the compression
// itself. A sync encodes thousands of records, so we pool them instead.
//
// The pool also unlocks parallelism. EncodeAll and DecodeAll are safe for
// concurrent use, but each EncodeAll call runs on a single goroutine, so one
// shared encoder would serialise. Handing each worker its own encoder gets us
// both reuse and parallelism.
var (
	encoderPool = sync.Pool{New: func() any {
		// SpeedFastest: record bodies are repetitive JSON, so the fastest
		// level compresses marginally better than the default (2.1% either
		// way) at ~2x the speed. Level choice is a speed decision here, not
		// a storage one.
		enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedFastest))
		if err != nil {
			panic(err) // only fails on invalid options, which are constant
		}

		return enc
	}}
	decoderPool = sync.Pool{New: func() any {
		dec, err := zstd.NewReader(nil)
		if err != nil {
			panic(err) // only fails on invalid options, which are constant
		}

		return dec
	}}
)

// EncodePayload marshals, compresses and encrypts changes for a record.
func EncodePayload(key [32]byte, aad []byte, changes []Change) ([]byte, []byte, error) {
	p := Payload{Changes: changes}
	raw, err := json.Marshal(p)
	if err != nil {
		return nil, nil, err
	}

	return EncodeJSON(key, aad, raw)
}

// EncodeJSON compresses and encrypts an already-marshalled payload. Callers
// that went through BatchChanges should prefer this: it skips the second
// serialisation of the same rows.
func EncodeJSON(key [32]byte, aad, raw []byte) ([]byte, []byte, error) {
	enc, ok := encoderPool.Get().(*zstd.Encoder)
	if !ok {
		return nil, nil, errors.New("clacks: zstd encoder pool corrupted")
	}
	compressed := enc.EncodeAll(raw, nil)
	encoderPool.Put(enc)

	return crypto.Encrypt(key, aad, compressed)
}

// DecodePayload decrypts and decompresses a record body.
func DecodePayload(key [32]byte, aad, nonce, data []byte) ([]Change, error) {
	compressed, err := crypto.Decrypt(key, aad, nonce, data)
	if err != nil {
		return nil, err
	}
	dec, ok := decoderPool.Get().(*zstd.Decoder)
	if !ok {
		return nil, errors.New("clacks: zstd decoder pool corrupted")
	}
	raw, err := dec.DecodeAll(compressed, nil)
	decoderPool.Put(dec)
	if err != nil {
		return nil, err
	}
	var p Payload
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}

	return p.Changes, nil
}

// Batch is one record's worth of changes, already serialised. Serialising is
// the most expensive part of encoding, so the size check and the payload are
// produced by a single Marshal: every row is serialised exactly once.
type Batch struct {
	Changes []Change
	// JSON is the marshalled Payload for Changes.
	JSON []byte
}

// BatchChanges splits changes into ~1MB / 500-row batches, marshalling each
// one exactly once and returning the bytes ready for compression.
func BatchChanges(changes []Change) ([]Batch, error) {
	var out []Batch
	for len(changes) > 0 {
		end := min(len(changes), MaxBatchRows)
		// Refine by encoded size, keeping the marshal that fit.
		var raw []byte
		for {
			b, err := json.Marshal(Payload{Changes: changes[:end]})
			if err != nil {
				return nil, err
			}
			raw = b
			if len(raw) <= MaxBatchBytes || end == 1 {
				break
			}
			end = max(end/2, 1)
		}
		out = append(out, Batch{Changes: changes[:end], JSON: raw})
		changes = changes[end:]
	}

	return out, nil
}
