package sync

import (
	"sort"

	"github.com/rodneyosodo/clacks/internal/record"
)

// Op is the sync decision for one (host, tag) series.
type Op int

const (
	Noop Op = iota
	Upload
	Download
)

// Decision is the diff result for one series.
type Decision struct {
	Op     Op
	Host   string
	Tag    string
	Local  *uint64 // max local idx when relevant
	Remote *uint64 // max remote idx when relevant
}

// Diff compares local and remote status for every known series.
//
//   - local ahead (or remote missing the series) -> Upload
//   - remote ahead (or local missing) -> Download
//   - equal -> Noop
func Diff(local, remote record.Status) []Decision {
	series := map[record.Series]bool{}
	for _, s := range local.SortedSeries() {
		series[s] = true
	}
	for _, s := range remote.SortedSeries() {
		series[s] = true
	}
	var out []Decision
	for s := range series {
		l, lok := local.MaxIdx(s.Host, s.Tag)
		r, rok := remote.MaxIdx(s.Host, s.Tag)
		switch {
		case lok && !rok:
			out = append(out, Decision{Op: Upload, Host: s.Host, Tag: s.Tag, Local: &l})
		case !lok && rok:
			out = append(out, Decision{Op: Download, Host: s.Host, Tag: s.Tag, Remote: &r})
		case lok && rok:
			switch {
			case l > r:
				out = append(out, Decision{Op: Upload, Host: s.Host, Tag: s.Tag, Local: &l, Remote: &r})
			case r > l:
				out = append(out, Decision{Op: Download, Host: s.Host, Tag: s.Tag, Local: &l, Remote: &r})
			default:
				out = append(out, Decision{Op: Noop, Host: s.Host, Tag: s.Tag, Local: &l, Remote: &r})
			}
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
