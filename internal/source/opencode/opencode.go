package opencode

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/rodneyosodo/clacks/internal/record"
	_ "modernc.org/sqlite"
)

// schema describes the opencode table layout of one major version. v2 renamed
// `session` to `session_v2`, folded `part` rows into `session_message.data`,
// and dropped `todo`, so the table set differs per version even though the
// record stream is identical.
type schema struct {
	major   int
	tables  []string // synced tables, parents before children
	session string
	message string
	part    string
	todo    string
}

var (
	schemaV1 = &schema{
		major:   1,
		tables:  []string{"project", "session", "message", "part", "todo"},
		session: "session",
		message: "message",
		part:    "part",
		todo:    "todo",
	}
	schemaV2 = &schema{
		major:   2,
		tables:  []string{"project", "session_v2", "session_message"},
		session: "session_v2",
		message: "session_message",
	}
)

// Source adapts opencode's SQLite storage to the sync record stream.
type Source struct {
	DBPath           string
	Since            int64 // only emit rows with time_updated > Since on first upload
	PropagateDeletes bool
	PathMap          [][2]string // from -> to prefix rules
	Version          string      // opencode version stamped on changes
	LocalHome        string
	Store            *record.Store // optional, for echo suppression on standalone Apply
	Warns            []string
}

// Tag implements source.Source.
func (s *Source) Tag() string { return "opencode" }

func openRO(ctx context.Context, path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	_, _ = db.ExecContext(ctx, "PRAGMA busy_timeout=5000;")
	if err := db.PingContext(ctx); err != nil {
		db.Close()

		return nil, fmt.Errorf("open %s: %w", path, err)
	}

	return db, nil
}

func openRW(ctx context.Context, path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	db, err := sql.Open("sqlite", path+"?cache=shared")
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, "PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000; PRAGMA foreign_keys=ON;"); err != nil {
		db.Close()

		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()

		return nil, fmt.Errorf("open %s: %w", path, err)
	}

	return db, nil
}

// siblingDBs lists other opencode*.db files next to path. opencode uses
// per-channel filenames (opencode-<channel>.db for non-latest/beta installs,
// see `opencode debug paths db`), so the live database is often a sibling of
// the default path.
func siblingDBs(path string) []string {
	dir := filepath.Dir(path)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	base := filepath.Base(path)
	var out []string
	for _, e := range entries {
		n := e.Name()
		if n != base && strings.HasPrefix(n, "opencode") && strings.HasSuffix(n, ".db") {
			out = append(out, filepath.Join(dir, n))
		}
	}
	sort.Strings(out)

	return out
}

func siblingHint(path string) string {
	sibs := siblingDBs(path)
	if len(sibs) == 0 {
		return ""
	}

	return fmt.Sprintf("; other opencode databases here: %s (set [sources.opencode] path to the one opencode uses — see `opencode debug paths db`)", strings.Join(sibs, ", "))
}

// rowToChange converts a scanned row map into a Change.
func rowToChange(table string, row map[string]any, version string) (record.Change, bool) {
	tu := toInt64(row["time_updated"])
	pk := pkOf(table, row)
	if pk == "" {
		return record.Change{}, false
	}
	cols := make(map[string]any, len(row))
	maps.Copy(cols, row)

	return record.Change{Table: table, PK: pk, TimeUpdated: tu, Columns: cols, Version: version}, true
}

func pkOf(table string, row map[string]any) string {
	switch table {
	case "todo":
		sid, _ := row["session_id"].(string)
		pos := fmt.Sprint(row["position"])
		if sid == "" {
			return ""
		}

		return sid + ":" + pos
	default:
		id, _ := row["id"].(string)

		return id
	}
}

// toInt64 coerces the driver values SQLite hands back (int64, []byte, string)
// into an integer. Unparseable values become 0.
func toInt64(v any) int64 {
	switch t := v.(type) {
	case int64:
		return t
	case int:
		return int64(t)
	case float64:
		return int64(t)
	case []byte:
		var n int64
		_, err := fmt.Sscan(string(t), &n)
		if err != nil {
			return 0
		}

		return n
	case string:
		var n int64
		_, err := fmt.Sscan(t, &n)
		if err != nil {
			return 0
		}

		return n
	default:
		return 0
	}
}

// resolveSchema identifies the opencode major version from its table names and
// verifies every table clacks needs is present. We deliberately do NOT create
// the tables ourselves: opencode owns its schema via drizzle migrations, and
// pre-creating a subset would collide with them.
func resolveSchema(ctx context.Context, db *sql.DB, path string) (*schema, error) {
	rows, err := db.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type='table'`)
	if err != nil {
		return nil, fmt.Errorf("opencode database at %s: %w", path, err)
	}
	defer rows.Close()
	have := map[string]bool{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, fmt.Errorf("opencode database at %s: %w", path, err)
		}
		have[n] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("opencode database at %s: %w", path, err)
	}

	// Checked in this order because a v1 database already carries
	// `session_message`, so its presence means nothing on its own.
	sc := schemaV1
	if have["session_v2"] {
		sc = schemaV2
	}
	for _, t := range sc.tables {
		if !have[t] {
			return nil, fmt.Errorf("opencode database at %s is not initialised (missing table %q, opencode %d schema)%s: launch opencode once so it creates its tables, then run clacks sync again", path, t, sc.major, siblingHint(path))
		}
	}
	if !have["session"] && !have["session_v2"] {
		return nil, fmt.Errorf("opencode database at %s is not initialised (no session table, expected %s or session_v2)%s: launch opencode once so it creates its tables, then run clacks sync again", path, sc.session, siblingHint(path))
	}

	return sc, nil
}

// queryRows runs q and returns rows as column maps.
func queryRows(ctx context.Context, db *sql.DB, q string, args ...any) ([]map[string]any, error) {
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out []map[string]any
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		m := map[string]any{}
		for i, c := range cols {
			m[c] = vals[i]
		}
		out = append(out, m)
	}

	return out, rows.Err()
}

// scanChildren emits the rows of every child table this schema version has for
// one session. v2 folds parts into the message row and has no todo, so the
// empty table names in schemaV2 are skipped.
func scanChildren(ctx context.Context, db *sql.DB, sc *schema, emit func(string, map[string]any), sid string) error {
	children := []struct{ table, order string }{
		{sc.message, "time_created ASC, id ASC"},
		{sc.part, "time_created ASC, id ASC"},
		{sc.todo, "position ASC"},
	}
	for _, c := range children {
		if c.table == "" {
			continue
		}
		rows, err := queryRows(ctx, db, `SELECT * FROM `+c.table+` WHERE session_id=? ORDER BY `+c.order, sid)
		if err != nil {
			return err
		}
		for _, r := range rows {
			emit(c.table, r)
		}
	}

	return nil
}

// Scan implements source.Source: local opencode.db -> changes.
func (s *Source) Scan(ctx context.Context, versions map[string]int64) ([]record.Change, error) {
	if err := s.checkDB(); err != nil {
		return nil, err
	}
	db, err := openRO(ctx, s.DBPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	sc, err := resolveSchema(ctx, db, s.DBPath)
	if err != nil {
		return nil, err
	}

	since := s.Since
	var out []record.Change

	// Dirty sessions drive the scan (uses existing time_updated ordering;
	// no new indexes on opencode's schema).
	sessRows, err := queryRows(ctx, db, `SELECT id, project_id, time_updated FROM `+sc.session+` WHERE time_updated > ? ORDER BY time_updated ASC`, since)
	if err != nil {
		return nil, err
	}
	// Session id set for delete detection.
	allSess, err := queryRows(ctx, db, `SELECT id FROM `+sc.session)
	if err != nil {
		return nil, err
	}
	liveSessions := map[string]bool{}
	for _, r := range allSess {
		if id, _ := r["id"].(string); id != "" {
			liveSessions[id] = true
		}
	}
	seenProjects := map[string]bool{}

	emitIfNew := func(table string, row map[string]any) {
		ch, ok := rowToChange(table, row, s.Version)
		if !ok {
			return
		}
		if ch.TimeUpdated <= since {
			return
		}
		if known, ok := versions[record.VersionKey(table, ch.PK)]; ok && ch.TimeUpdated <= known {
			return
		}
		out = append(out, ch)
	}

	for _, sr := range sessRows {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		sid, _ := sr["id"].(string)
		if sid == "" {
			continue
		}
		knownSess, sessKnown := versions[record.VersionKey(sc.session, sid)]
		sessTU := toInt64(sr["time_updated"])
		full, err := queryRows(ctx, db, `SELECT * FROM `+sc.session+` WHERE id=?`, sid)
		if err != nil || len(full) == 0 {
			continue
		}
		projID, _ := full[0]["project_id"].(string)
		// Include the project row the first time a session in that project is seen.
		if projID != "" && !seenProjects[projID] {
			seenProjects[projID] = true
			if _, ok := versions[record.VersionKey("project", projID)]; !ok {
				prows, err := queryRows(ctx, db, `SELECT * FROM project WHERE id=?`, projID)
				if err != nil {
					return nil, err
				}
				for _, pr := range prows {
					emitIfNew("project", pr)
				}
			}
		}
		if sessKnown && sessTU <= knownSess {
			continue // session and (by invariant) children unchanged
		}
		emitIfNew(sc.session, full[0])
		if err := scanChildren(ctx, db, sc, emitIfNew, sid); err != nil {
			return nil, err
		}
	}

	// Deletes: sessions tracked in versions but gone locally.
	if s.PropagateDeletes {
		for key := range versions {
			parts := strings.SplitN(key, "\x00", 2)
			if len(parts) != 2 || parts[0] != sc.session {
				continue
			}
			if !liveSessions[parts[1]] {
				out = append(out, record.Change{Table: sc.session, PK: parts[1], Tombstone: true, Version: s.Version})
			}
		}
	}

	sort.Slice(out, func(i, j int) bool {
		oi, oj := tableOrder(sc, out[i].Table), tableOrder(sc, out[j].Table)
		if oi != oj {
			return oi < oj
		}
		if out[i].TimeUpdated != out[j].TimeUpdated {
			return out[i].TimeUpdated < out[j].TimeUpdated
		}

		return out[i].PK < out[j].PK
	})

	return out, nil
}

func tableOrder(sc *schema, t string) int {
	for i, n := range sc.tables {
		if n == t {
			return i
		}
	}

	return len(sc.tables)
}

// columnInfo describes one local table column.
type columnInfo struct {
	name       string
	notNull    bool
	hasDefault bool
	pk         bool
}

func tableInfo(ctx context.Context, db *sql.DB, table string) (map[string]columnInfo, error) {
	rows, err := db.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	info := map[string]columnInfo{}
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return nil, err
		}
		info[name] = columnInfo{name: name, notNull: notnull == 1, hasDefault: dflt.Valid, pk: pk > 0}
	}

	return info, rows.Err()
}

// RewritePath applies path_map rules, defaulting remote $HOME -> local $HOME.
func (s *Source) RewritePath(p string) string {
	for _, rule := range s.PathMap {
		if rule[0] != "" && strings.HasPrefix(p, rule[0]) {
			return rule[1] + strings.TrimPrefix(p, rule[0])
		}
	}

	return rewriteHome(p, s.home())
}

// rewriteHome maps a leading /home/<user> or /Users/<user> onto localHome.
func rewriteHome(p, localHome string) string {
	if localHome == "" || p == "" || p[0] != '/' {
		return p
	}
	rest := strings.TrimPrefix(p, "/")
	parts := strings.SplitN(rest, "/", 3)
	if len(parts) < 2 {
		return p
	}
	if parts[0] != "home" && parts[0] != "Users" {
		return p
	}
	if len(parts) == 2 {
		return localHome
	}

	return localHome + "/" + parts[2]
}

// Apply implements source.Source: changes -> local opencode.db.
func (s *Source) Apply(ctx context.Context, changes []record.Change) error {
	if len(changes) == 0 {
		return nil
	}
	if err := s.checkDB(); err != nil {
		return err
	}
	db, err := openRW(ctx, s.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()
	sc, err := resolveSchema(ctx, db, s.DBPath)
	if err != nil {
		return err
	}

	infos := map[string]map[string]columnInfo{}
	for _, t := range sc.tables {
		info, err := tableInfo(ctx, db, t)
		if err != nil {
			return err
		}
		if len(info) == 0 {
			return fmt.Errorf("opencode table %q missing locally", t)
		}
		infos[t] = info
	}

	// Order: parents first; tombstones (session deletes) last.
	ordered := append([]record.Change{}, changes...)
	sort.SliceStable(ordered, func(i, j int) bool {
		var ti, tj int
		if ordered[i].Tombstone {
			ti = len(sc.tables) + 1
		} else {
			ti = tableOrder(sc, ordered[i].Table)
		}
		if ordered[j].Tombstone {
			tj = len(sc.tables) + 1
		} else {
			tj = tableOrder(sc, ordered[j].Table)
		}

		return ti < tj
	})

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	skipped := map[string]bool{}

	for _, ch := range ordered {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if ch.Tombstone {
			if err := s.applyTombstone(ctx, tx, sc, ch); err != nil {
				return err
			}
			s.markApplied(ctx, ch)

			continue
		}
		info, ok := infos[ch.Table]
		if !ok {
			// Expected when machines run different opencode majors: records for
			// a table this database does not have are reported once, not per row.
			if !skipped[ch.Table] {
				skipped[ch.Table] = true
				s.Warns = append(s.Warns, fmt.Sprintf("skipped table %q: not part of the local opencode %d schema (this machine runs a different opencode major than whoever uploaded it)", ch.Table, sc.major))
			}

			continue
		}
		cols := map[string]any{}
		for k, v := range ch.Columns {
			if _, ok := info[k]; ok {
				cols[k] = v
			}
		}
		// NOT NULL without default must be present.
		skip := false
		for name, ci := range info {
			if ci.notNull && !ci.hasDefault && !ci.pk {
				if v, ok := cols[name]; !ok || v == nil {
					s.Warns = append(s.Warns, fmt.Sprintf("skip %s %s: missing NOT NULL %s", ch.Table, ch.PK, name))
					skip = true

					break
				}
			}
		}
		if skip {
			continue
		}
		s.rewriteRowPaths(ctx, tx, sc, ch.Table, cols)
		if err := upsert(ctx, tx, ch.Table, ch.PK, ch.TimeUpdated, cols, info); err != nil {
			return fmt.Errorf("upsert %s %s: %w", ch.Table, ch.PK, err)
		}
		s.markApplied(ctx, ch)
	}

	return tx.Commit()
}

// upsert writes a row; the newer time_updated wins.
func upsert(ctx context.Context, tx *sql.Tx, table, pk string, tu int64, cols map[string]any, info map[string]columnInfo) error {
	pkCols, pkVals, err := pkColumns(table, pk, cols)
	if err != nil {
		return err
	}
	// Newer-wins check.
	set := make([]string, len(pkCols))
	for i, c := range pkCols {
		set[i] = c + "=?"
	}
	where := strings.Join(set, " AND ")
	var existingTU sql.NullInt64
	qerr := tx.QueryRowContext(ctx, fmt.Sprintf(`SELECT time_updated FROM %s WHERE %s`, table, where), pkVals...).Scan(&existingTU)
	_ = qerr
	if existingTU.Valid && existingTU.Int64 >= tu && tu != 0 {
		return nil
	}
	if _, ok := info["time_updated"]; ok && tu != 0 {
		cols["time_updated"] = tu
	}
	names := make([]string, 0, len(cols))
	for k := range cols {
		names = append(names, k)
	}
	sort.Strings(names)
	// Identifiers can't be bound as parameters, so they are interpolated.
	// They come from the local schema (PRAGMA table_info) and our fixed table
	// list, but validate anyway so a malformed or hostile record can't inject
	// SQL through a column name.
	for _, n := range names {
		if !isSafeIdent(n) {
			return fmt.Errorf("unsafe column identifier %q", n)
		}
	}
	if !isSafeIdent(table) {
		return fmt.Errorf("unsafe table identifier %q", table)
	}
	placeholders := make([]string, len(names))
	vals := make([]any, len(names))
	for i, n := range names {
		placeholders[i] = "?"
		vals[i] = normalize(cols[n])
	}
	conflict := "(" + strings.Join(pkCols, ", ") + ")"
	updates := make([]string, 0, len(names))
	for _, n := range names {
		isPK := false
		for _, p := range pkCols {
			if p == n {
				isPK = true
			}
		}
		if !isPK {
			updates = append(updates, fmt.Sprintf("%s=excluded.%s", n, n))
		}
	}
	var q string
	if len(updates) == 0 {
		q = fmt.Sprintf("INSERT OR IGNORE INTO %s(%s) VALUES(%s)", table, strings.Join(names, ", "), strings.Join(placeholders, ", "))
	} else {
		q = fmt.Sprintf("INSERT INTO %s(%s) VALUES(%s) ON CONFLICT%s DO UPDATE SET %s",
			table, strings.Join(names, ", "), strings.Join(placeholders, ", "), conflict, strings.Join(updates, ", "))
	}
	_, err = tx.ExecContext(ctx, q, vals...)

	return err
}

// isSafeIdent reports whether s is a plain unquoted SQL identifier: letters,
// digits and underscores only, not starting with a digit. Values are always
// bound as parameters; only identifiers take this path.
func isSafeIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
		case r >= '0' && r <= '9':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}

	return true
}

func pkColumns(table, pk string, cols map[string]any) ([]string, []any, error) {
	switch table {
	case "todo":
		sid, pos, ok := strings.Cut(pk, ":")
		if !ok {
			return nil, nil, fmt.Errorf("bad todo pk %q", pk)
		}
		cols["session_id"] = sid
		var n int
		_, err := fmt.Sscan(pos, &n)
		if err != nil {
			return nil, nil, fmt.Errorf("bad todo pk %q", pk)
		}
		cols["position"] = n

		return []string{"session_id", "position"}, []any{sid, n}, nil
	default:
		cols["id"] = pk

		return []string{"id"}, []any{pk}, nil
	}
}

func normalize(v any) any {
	switch t := v.(type) {
	case []byte:
		return string(t)
	case json.Number:
		return string(t)
	default:
		return v
	}
}

// SessionInfo describes one local session plus its sync state.
type SessionInfo struct {
	ID          string `json:"id"`
	ProjectID   string `json:"project_id"`
	Title       string `json:"title"`
	TimeUpdated int64  `json:"time_updated"`
	Synced      bool   `json:"synced"`
}

// ListSessions lists sessions with sync state derived from row versions.
func (s *Source) ListSessions(ctx context.Context, versions map[string]int64) ([]SessionInfo, error) {
	db, err := openRO(ctx, s.DBPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	sc, err := resolveSchema(ctx, db, s.DBPath)
	if err != nil {
		return nil, err
	}
	rows, err := queryRows(ctx, db, `SELECT id, project_id, title, time_updated FROM `+sc.session+` ORDER BY time_updated DESC`)
	if err != nil {
		return nil, err
	}
	var out []SessionInfo
	for _, r := range rows {
		id, _ := r["id"].(string)
		tu := toInt64(r["time_updated"])
		synced := false
		if known, ok := versions[record.VersionKey(sc.session, id)]; ok && tu <= known {
			synced = true
		}
		title, _ := r["title"].(string)
		proj, _ := r["project_id"].(string)
		out = append(out, SessionInfo{ID: id, ProjectID: proj, Title: title, TimeUpdated: tu, Synced: synced})
	}

	return out, nil
}

func (s *Source) home() string {
	if s.LocalHome != "" {
		return s.LocalHome
	}
	h, _ := os.UserHomeDir()

	return h
}

// checkDB fails fast with an actionable message when the opencode database
// isn't where we expect it (fresh machine, wrong path, unmounted volume).
func (s *Source) checkDB() error {
	if _, err := os.Stat(s.DBPath); err != nil {
		return fmt.Errorf("opencode database not found at %s%s: run opencode once to initialise it, or point [sources.opencode] path (or $OPENCODE_DB) at the real opencode.db", s.DBPath, siblingHint(s.DBPath))
	}

	return nil
}

func (s *Source) markApplied(ctx context.Context, ch record.Change) {
	if s.Store != nil {
		_ = s.Store.SetRowVersion(ctx, ch.Table, ch.PK, ch.TimeUpdated)
	}
}

func (s *Source) applyTombstone(ctx context.Context, tx *sql.Tx, sc *schema, ch record.Change) error {
	del := func(q string, args ...any) error {
		_, err := tx.ExecContext(ctx, q, args...)

		return err
	}
	// The v1 todo table is the one with a composite key.
	if ch.Table == sc.todo {
		sid, pos, ok := strings.Cut(ch.PK, ":")
		if !ok {
			return nil
		}

		return del(`DELETE FROM `+sc.todo+` WHERE session_id=? AND position=?`, sid, pos)
	}
	// Tables this database does not have are a cross-version record, not an error.
	if !slices.Contains(sc.tables, ch.Table) {
		return nil
	}

	return del(`DELETE FROM `+ch.Table+` WHERE id=?`, ch.PK)
}

// rewriteRowPaths maps session.directory / project.worktree to local paths.
// A project that already exists locally keeps its local worktree.
func (s *Source) rewriteRowPaths(ctx context.Context, tx *sql.Tx, sc *schema, table string, cols map[string]any) {
	switch table {
	case sc.session:
		if d, ok := cols["directory"].(string); ok && d != "" {
			cols["directory"] = s.RewritePath(d)
		}
	case "project":
		id, _ := cols["id"].(string)
		if id != "" {
			var existing string
			err := tx.QueryRowContext(ctx, `SELECT worktree FROM project WHERE id=?`, id).Scan(&existing)
			if err == nil && existing != "" {
				cols["worktree"] = existing

				return
			}
		}
		if w, ok := cols["worktree"].(string); ok && w != "" {
			cols["worktree"] = s.RewritePath(w)
		}
	}
}
