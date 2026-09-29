package sync

import (
	"testing"

	"github.com/rodneyosodo/clacks/internal/record"
)

func TestDiff(t *testing.T) {
	cases := []struct {
		name   string
		local  record.Status
		remote record.Status
		want   map[string]Op // "host/tag" -> op
	}{
		{"both empty", record.Status{}, record.Status{}, map[string]Op{}},
		{
			"local only uploads",
			record.Status{"h1": {"opencode": 3}},
			record.Status{},
			map[string]Op{"h1/opencode": Upload},
		},
		{
			"remote only downloads",
			record.Status{},
			record.Status{"h2": {"opencode": 5}},
			map[string]Op{"h2/opencode": Download},
		},
		{
			"equal noop",
			record.Status{"h1": {"opencode": 2}},
			record.Status{"h1": {"opencode": 2}},
			map[string]Op{"h1/opencode": Noop},
		},
		{
			"local ahead uploads",
			record.Status{"h1": {"opencode": 4}},
			record.Status{"h1": {"opencode": 2}},
			map[string]Op{"h1/opencode": Upload},
		},
		{
			"remote ahead downloads",
			record.Status{"h1": {"opencode": 2}},
			record.Status{"h1": {"opencode": 7}},
			map[string]Op{"h1/opencode": Download},
		},
		{
			"mixed series",
			record.Status{"h1": {"opencode": 1}, "h2": {"opencode": 9}},
			record.Status{"h1": {"opencode": 3}, "h3": {"opencode": 0}},
			map[string]Op{"h1/opencode": Download, "h2/opencode": Upload, "h3/opencode": Download},
		},
		{
			"multiple tags",
			record.Status{"h1": {"opencode": 1, "claude": 1}},
			record.Status{"h1": {"opencode": 1, "claude": 2}},
			map[string]Op{"h1/opencode": Noop, "h1/claude": Download},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Diff(tc.local, tc.remote)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d decisions, want %d (%+v)", len(got), len(tc.want), got)
			}
			for _, d := range got {
				key := d.Host + "/" + d.Tag
				want, ok := tc.want[key]
				if !ok {
					t.Fatalf("unexpected series %s", key)
				}
				if d.Op != want {
					t.Fatalf("series %s: got %v want %v", key, d.Op, want)
				}
			}
		})
	}
}

func TestDiffCarriesIdx(t *testing.T) {
	local := record.Status{"h1": {"opencode": 4}}
	remote := record.Status{"h1": {"opencode": 2}}
	ds := Diff(local, remote)
	if len(ds) != 1 || ds[0].Op != Upload {
		t.Fatalf("bad diff: %+v", ds)
	}
	if ds[0].Local == nil || *ds[0].Local != 4 || ds[0].Remote == nil || *ds[0].Remote != 2 {
		t.Fatalf("idx not carried: %+v", ds[0])
	}
}
