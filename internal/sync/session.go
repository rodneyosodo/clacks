package sync

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"
	"github.com/rodneyosodo/clacks/internal/client"
	"github.com/rodneyosodo/clacks/internal/record"
	"github.com/rodneyosodo/clacks/internal/source"
)

// Session drives one `clacks sync`: scan, diff, upload/download, apply.
type Session struct {
	HostID  string
	Key     [32]byte
	Version string // opencode version stamped on new records
	Store   *record.Store
	Client  *client.Client
	Sources []source.Source
	// Force re-emits all local rows, ignoring row_versions cursors.
	Force bool
}

// Sync runs the full loop.
func (s *Session) Sync(ctx context.Context) error {
	// 1. Scan: local changes -> new records under this host.
	for _, src := range s.Sources {
		if err := s.scanSource(ctx, src); err != nil {
			return fmt.Errorf("scan %s: %w", src.Tag(), err)
		}
	}

	// 2. Diff.
	local, err := s.Store.Status(ctx)
	if err != nil {
		return err
	}
	remote, err := s.Client.Status(ctx)
	if err != nil {
		return fmt.Errorf("remote status: %w", err)
	}
	decisions := Diff(local, remote)
	for _, d := range decisions {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		switch d.Op {
		case Upload:
			slog.Info("uploading", slog.String("host", d.Host), slog.String("tag", d.Tag))
			if err := s.upload(ctx, d.Host, d.Tag, remote); err != nil {
				return err
			}
		case Download:
			slog.Info("downloading", slog.String("host", d.Host), slog.String("tag", d.Tag))
			if d.Remote == nil {
				continue
			}
			if err := s.download(ctx, d.Host, d.Tag, local, *d.Remote); err != nil {
				return err
			}
		case Noop:
			slog.Debug("up to date", slog.String("host", d.Host), slog.String("tag", d.Tag))
		}
	}

	// 3. Apply records from other hosts past the applied cursor.
	for _, src := range s.Sources {
		if err := s.applySource(ctx, src); err != nil {
			return fmt.Errorf("apply %s: %w", src.Tag(), err)
		}
	}

	return nil
}

func (s *Session) scanSource(ctx context.Context, src source.Source) error {
	versions, err := s.Store.RowVersions(ctx)
	if err != nil {
		return err
	}
	if s.Force {
		versions = map[string]int64{}
	}
	changes, err := src.Scan(ctx, versions)
	if err != nil {
		return err
	}
	if len(changes) == 0 {
		return nil
	}
	slog.Debug("scanned local changes", slog.String("tag", src.Tag()), slog.Int("changes", len(changes)))
	batches, err := record.BatchChanges(changes)
	if err != nil {
		return err
	}
	for _, batch := range batches {
		next, err := s.Store.NextIdx(ctx, s.HostID, src.Tag())
		if err != nil {
			return err
		}
		id := uuid.Must(uuid.NewV7()).String()
		aad := aadFor(id, s.HostID, src.Tag(), next)
		// batch.JSON was marshalled once by BatchChanges; don't redo it.
		nonce, data, err := record.EncodeJSON(s.Key, aad, batch.JSON)
		if err != nil {
			return err
		}
		rec := &record.Record{
			ID: id, Host: s.HostID, Tag: src.Tag(), Idx: next,
			Version: s.Version, Timestamp: record.NowMicros(),
			Nonce: nonce, Data: data,
		}
		if err := s.Store.Append(ctx, rec); err != nil {
			return err
		}
		// Echo suppression: scanned rows are "already synced".
		for _, ch := range batch.Changes {
			if err := s.Store.SetRowVersion(ctx, ch.Table, ch.PK, ch.TimeUpdated); err != nil {
				return err
			}
		}
	}

	return nil
}

func aadFor(id, host, tag string, idx uint64) []byte {
	return []byte(id + "|" + host + "|" + tag + "|" + itoa(idx))
}

func itoa(n uint64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}

	return string(b[i:])
}

// pageSize is how many records move per HTTP request. 1000 is the maximum
// the server accepts, and it cuts a 3000-record first sync from 31 round-trips
// to 4.
const pageSize = 1000

// transferConcurrency is how many pages may be in flight at once. Transfers
// are the slow part of a sync when the server is remote, and pages are
// disjoint idx ranges, so overlapping them is safe.
const transferConcurrency = 4

// nextPage fetches and moves one page. It reports the last idx it handled and
// whether any pages remain.
type nextPage func(ctx context.Context) (last uint64, more bool, err error)

// runPages drives nextPage across transferConcurrency workers. The next start
// idx is shared, so each worker claims a distinct range. It returns the first
// error, cancelling the rest.
func runPages(ctx context.Context, fn nextPage) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		ferr error
	)
	fail := func(err error) {
		mu.Lock()
		if ferr == nil {
			ferr = err
		}
		mu.Unlock()
		cancel()
	}

	for range transferConcurrency {
		wg.Go(func() {
			for {
				if ctx.Err() != nil {
					return
				}
				_, more, err := fn(ctx)
				if err != nil {
					fail(err)

					return
				}
				if !more {
					return
				}
			}
		})
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()

	return ferr
}

func (s *Session) upload(ctx context.Context, host, tag string, remote record.Status) error {
	start := uint64(0)
	if r, ok := remote.MaxIdx(host, tag); ok {
		start = r + 1
	}
	var next atomic.Uint64
	next.Store(start)

	return runPages(ctx, func(ctx context.Context) (uint64, bool, error) {
		recs, err := s.Store.List(ctx, host, tag, next.Load(), pageSize)
		if err != nil {
			return 0, false, err
		}
		if len(recs) == 0 {
			return 0, false, nil
		}
		last := recs[len(recs)-1].Idx
		next.Store(last + 1)
		// Pages are disjoint ranges and the server upserts on
		// (owner, host, tag, idx), so out-of-order arrival is harmless.
		if err := s.Client.Upload(ctx, recs); err != nil {
			return 0, false, err
		}

		return last, true, nil
	})
}

// download pulls every record up to remoteMax. Knowing the remote high-water
// mark up front means we can size the work exactly instead of probing until a
// short page comes back.
func (s *Session) download(ctx context.Context, host, tag string, local record.Status, remoteMax uint64) error {
	start := uint64(0)
	if l, ok := local.MaxIdx(host, tag); ok {
		start = l + 1
	}
	if start > remoteMax {
		return nil
	}
	var next atomic.Uint64
	next.Store(start)

	return runPages(ctx, func(ctx context.Context) (uint64, bool, error) {
		from := next.Load()
		if from > remoteMax {
			return 0, false, nil
		}
		recs, err := s.Client.Download(ctx, host, tag, from, pageSize)
		if err != nil {
			return 0, false, err
		}
		if len(recs) == 0 {
			return 0, false, nil
		}
		for _, r := range recs {
			if err := s.Store.Append(ctx, r); err != nil {
				return 0, false, err
			}
		}
		last := recs[len(recs)-1].Idx
		next.Store(last + 1)

		return last, last < remoteMax, nil
	})
}

func (s *Session) applySource(ctx context.Context, src source.Source) error {
	status, err := s.Store.Status(ctx)
	if err != nil {
		return err
	}
	tags, ok := statusKeys(status, src.Tag())
	if !ok {
		return nil
	}
	for _, host := range tags {
		if host == s.HostID {
			continue
		}
		cursor, err := s.Store.AppliedCursor(ctx, host, src.Tag())
		if err != nil {
			return err
		}
		start := uint64(0)
		if cursor >= 0 {
			start = uint64(cursor) + 1
		}
		maxIdx, _ := status.MaxIdx(host, src.Tag())
		var changes []record.Change
		batchStart := start
		for batchStart <= maxIdx {
			recs, err := s.Store.List(ctx, host, src.Tag(), batchStart, 100)
			if err != nil {
				return err
			}
			if len(recs) == 0 {
				break
			}
			for _, r := range recs {
				aad := aadFor(r.ID, r.Host, r.Tag, r.Idx)
				ch, err := record.DecodePayload(s.Key, aad, r.Nonce, r.Data)
				if err != nil {
					return fmt.Errorf("decrypt %s/%d: %w (wrong key? run `clacks key` on the other machine)", host, r.Idx, err)
				}
				changes = append(changes, ch...)
				if err := s.Store.SetAppliedCursor(ctx, host, src.Tag(), r.Idx); err != nil {
					return err
				}
			}
			batchStart = recs[len(recs)-1].Idx + 1
		}
		if len(changes) > 0 {
			slog.Info("applying remote changes", slog.String("tag", src.Tag()), slog.Int("changes", len(changes)))
			if err := src.Apply(ctx, changes); err != nil {
				return err
			}
			// Echo suppression for applied rows.
			for _, ch := range changes {
				if err := s.Store.SetRowVersion(ctx, ch.Table, ch.PK, ch.TimeUpdated); err != nil {
					return err
				}
			}
		}
		_ = ctx
	}

	return nil
}

func statusKeys(status record.Status, tag string) ([]string, bool) {
	var hosts []string
	for h, tags := range status {
		if _, ok := tags[tag]; ok {
			hosts = append(hosts, h)
		}
	}
	if len(hosts) == 0 {
		return nil, false
	}

	return hosts, true
}
