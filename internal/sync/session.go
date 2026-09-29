package sync

import (
	"context"
	"fmt"

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
	local, err := s.Store.Status()
	if err != nil {
		return err
	}
	remote, err := s.Client.Status()
	if err != nil {
		return fmt.Errorf("remote status: %w", err)
	}
	for _, d := range Diff(local, remote) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		switch d.Op {
		case Upload:
			if err := s.upload(ctx, d.Host, d.Tag, remote); err != nil {
				return err
			}
		case Download:
			if err := s.download(ctx, d.Host, d.Tag, local); err != nil {
				return err
			}
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
	versions, err := s.Store.RowVersions()
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
	for _, batch := range record.BatchChanges(changes) {
		next, err := s.Store.NextIdx(s.HostID, src.Tag())
		if err != nil {
			return err
		}
		id := uuid.Must(uuid.NewV7()).String()
		aad := aadFor(id, s.HostID, src.Tag(), next)
		nonce, data, err := record.EncodePayload(s.Key, aad, batch)
		if err != nil {
			return err
		}
		rec := &record.Record{
			ID: id, Host: s.HostID, Tag: src.Tag(), Idx: next,
			Version: s.Version, Timestamp: record.NowMicros(),
			Nonce: nonce, Data: data,
		}
		if err := s.Store.Append(rec); err != nil {
			return err
		}
		// Echo suppression: scanned rows are "already synced".
		for _, ch := range batch {
			if err := s.Store.SetRowVersion(ch.Table, ch.PK, ch.TimeUpdated); err != nil {
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

func (s *Session) upload(ctx context.Context, host, tag string, remote record.Status) error {
	start := uint64(0)
	if r, ok := remote.MaxIdx(host, tag); ok {
		start = r + 1
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		recs, err := s.Store.List(host, tag, start, 100)
		if err != nil {
			return err
		}
		if len(recs) == 0 {
			return nil
		}
		if err := s.Client.Upload(recs); err != nil {
			return err
		}
		start = recs[len(recs)-1].Idx + 1
	}
}

func (s *Session) download(ctx context.Context, host, tag string, local record.Status) error {
	start := uint64(0)
	if l, ok := local.MaxIdx(host, tag); ok {
		start = l + 1
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		recs, err := s.Client.Download(host, tag, start, 100)
		if err != nil {
			return err
		}
		if len(recs) == 0 {
			return nil
		}
		for _, r := range recs {
			if err := s.Store.Append(r); err != nil {
				return err
			}
		}
		start = recs[len(recs)-1].Idx + 1
		if len(recs) < 100 {
			return nil
		}
	}
}

func (s *Session) applySource(ctx context.Context, src source.Source) error {
	status, err := s.Store.Status()
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
		cursor, err := s.Store.AppliedCursor(host, src.Tag())
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
			recs, err := s.Store.List(host, src.Tag(), batchStart, 100)
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
				if err := s.Store.SetAppliedCursor(host, src.Tag(), r.Idx); err != nil {
					return err
				}
			}
			batchStart = recs[len(recs)-1].Idx + 1
		}
		if len(changes) > 0 {
			if err := src.Apply(ctx, changes); err != nil {
				return err
			}
			// Echo suppression for applied rows.
			versions, err := s.Store.RowVersions()
			_ = versions
			if err != nil {
				return err
			}
			for _, ch := range changes {
				if err := s.Store.SetRowVersion(ch.Table, ch.PK, ch.TimeUpdated); err != nil {
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
