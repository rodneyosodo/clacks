package record

import (
	"sort"
	"time"

	"github.com/google/uuid"
)

// Record is the atuin-shaped encrypted unit of sync.
type Record struct {
	ID        string `json:"id"`
	Host      string `json:"host"`
	Tag       string `json:"tag"`
	Idx       uint64 `json:"idx"`
	Version   string `json:"version,omitempty"`
	Timestamp int64  `json:"timestamp"`
	Nonce     []byte `json:"nonce"`
	Data      []byte `json:"data"`
}

// NewRecordID mints a UUIDv7 record id.
func NewRecordID() string { return uuid.Must(uuid.NewV7()).String() }

// Status maps host -> tag -> max idx.
type Status map[string]map[string]uint64

// Series identifies a (host, tag) stream.
type Series struct {
	Host string
	Tag  string
}

// NowMicros returns unix microseconds.
func NowMicros() int64 { return time.Now().UnixMicro() }

// SortedSeries returns series keys in deterministic order.
func (s Status) SortedSeries() []Series {
	var out []Series
	for h, tags := range s {
		for t := range tags {
			out = append(out, Series{Host: h, Tag: t})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Host != out[j].Host {
			return out[i].Host < out[j].Host
		}
		return out[i].Tag < out[j].Tag
	})
	return out
}

// MaxIdx returns the max idx for a series, and whether it exists.
func (s Status) MaxIdx(host, tag string) (uint64, bool) {
	tags, ok := s[host]
	if !ok {
		return 0, false
	}
	v, ok := tags[tag]
	return v, ok
}
