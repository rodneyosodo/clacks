package source

import (
	"context"

	"github.com/rodneyosodo/clacks/internal/record"
)

// Source syncs one tool's storage (opencode today, claude later).
type Source interface {
	// Tag is the record tag, e.g. "opencode".
	Tag() string
	// Scan emits local changes as new records would carry them.
	Scan(ctx context.Context, versions map[string]int64) ([]record.Change, error)
	// Apply writes remote changes into local tool storage.
	Apply(ctx context.Context, changes []record.Change) error
}
