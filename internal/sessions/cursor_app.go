package sessions

// The Cursor app keeps its composers in one SQLite database, state.vscdb,
// in the app's user data: ~/Library/Application Support/Cursor on a Mac,
// %APPDATA%\Cursor on Windows, $XDG_CONFIG_HOME/Cursor (~/.config/Cursor)
// on Linux, under User/globalStorage. It is in WAL mode and written all
// the while Cursor runs.
//
//	composerHeaders  one row a composer: composerId, createdAt and
//	                 lastUpdatedAt (milliseconds), isSubagent, and value,
//	                 JSON with name (its title), subtitle, workspaceIdentifier
//	                 .uri.fsPath (the folder it works in) and, for a
//	                 subagent, subagentInfo.parentComposerId and
//	                 rootParentConversationId
//	cursorDiskKV    composerData:<id> (modelConfig.modelName) and
//	                 bubbleId:<id>:<bubble> (type 1 a prompt, type 2 a reply,
//	                 tokenCount.inputTokens and outputTokens)
//
// A row with no title and no bubbles is a draft nothing was said in, and
// is left out. A subagent's bubbles count on the composer it was spawned
// from. magpie only reads the database, from a snapshot, and never deletes
// a composer: the rows live in Cursor's own file. The app has no command
// that picks one composer up again, so these sessions are listed read only.
// The CLI's chats stay where they are, under ~/.cursor/chats.

import (
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// cursorAppClean removes the snapshots cursorAppFiles opened, once the
// listing that asked for them has finished (closeDBs).
var cursorAppClean []func()

// cursorAppDBPath is where the Cursor app keeps state.vscdb. Tests point
// it at a database of their own.
var cursorAppDBPath = defaultCursorAppDB

func defaultCursorAppDB() string {
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		return ""
	}
	return filepath.Join(dir, "Cursor", "User", "globalStorage", "state.vscdb")
}

func cursorAppDB() string { return cursorAppDBPath() }

// cursorAppStore is the snapshot a listing reads composers from.
type cursorAppStore struct{ db *sql.DB }

type cursorAppHeader struct {
	Name          string `json:"name"`
	Subtitle      string `json:"subtitle"`
	CreatedAt     int64  `json:"createdAt"`
	LastUpdatedAt int64  `json:"lastUpdatedAt"`
	Workspace     struct {
		URI struct {
			FsPath string `json:"fsPath"`
			Path   string `json:"path"`
		} `json:"uri"`
	} `json:"workspaceIdentifier"`
	Subagent *struct {
		Parent string `json:"parentComposerId"`
		Root   string `json:"rootParentConversationId"`
	} `json:"subagentInfo"`
}

func (h cursorAppHeader) cwd() string {
	if p := strings.TrimSpace(h.Workspace.URI.FsPath); p != "" {
		return p
	}
	return strings.TrimSpace(h.Workspace.URI.Path)
}

func (h cursorAppHeader) parent() string {
	if h.Subagent == nil {
		return ""
	}
	if p := h.Subagent.Root; safeID.MatchString(p) {
		return p
	}
	if p := h.Subagent.Parent; safeID.MatchString(p) {
		return p
	}
	return ""
}

// cursorAppBubbleMax is how many of a composer's bubbles are summed. A long
// chat keeps every bubble in the same table, and reading all of them just
// to add token counts that Cursor usually leaves at zero holds the listing
// up. Past this, the composer is still listed from its header.
const cursorAppBubbleMax = 4000

// cursorAppFiles are the Cursor app's composers, one a session: its path
// the database's and #<id>. A named composer is read again when its header
// changes. The database file changes with every write to any composer, so
// the header's own time is what a composer is kept by.
func cursorAppFiles() []file {
	path := cursorAppDB()
	if path == "" || !fileExists(path) {
		return nil
	}
	db, cleanup, err := openCursorApp(path)
	if err != nil {
		return nil
	}
	rows, err := db.Query(`SELECT composerId, COALESCE(isSubagent, 0), COALESCE(createdAt, 0), COALESCE(lastUpdatedAt, 0), COALESCE(value, '') FROM composerHeaders`)
	if err != nil {
		cleanup()
		return nil
	}
	defer rows.Close()
	st := &cursorAppStore{db: db}
	var out []file
	for rows.Next() {
		var id, raw string
		var sub, created, updated int64
		if rows.Scan(&id, &sub, &created, &updated, &raw) != nil || !safeID.MatchString(id) {
			continue
		}
		var h cursorAppHeader
		if json.Unmarshal([]byte(raw), &h) != nil {
			continue
		}
		named := cursorTitle(h.Name) != "" || title(h.Subtitle) != ""
		var bubbles int64
		if !named {
			lo, hi := cursorBubbleSpan(id)
			if db.QueryRow(`SELECT COUNT(*) FROM cursorDiskKV WHERE key >= ? AND key < ?`, lo, hi).Scan(&bubbles) != nil || bubbles == 0 {
				continue
			}
		}
		if updated == 0 {
			updated = h.LastUpdatedAt
		}
		if created == 0 {
			created = h.CreatedAt
		}
		key, main := "cursor:"+id, true
		if sub != 0 {
			if p := h.parent(); p != "" && p != id {
				key, main = "cursor:"+p, false
			}
		}
		mod := ms(updated)
		if mod.IsZero() {
			mod = ms(created)
		}
		out = append(out, file{
			agent:    "cursor",
			key:      key,
			path:     path + "#" + id,
			sid:      id,
			main:     main,
			size:     int64(len(raw)),
			mod:      mod,
			app:      st,
			readOnly: true,
		})
	}
	if err := rows.Err(); err != nil || len(out) == 0 {
		cleanup()
		return nil
	}
	cursorAppClean = append(cursorAppClean, cleanup)
	return out
}

// cursorBubbleSpan is the key range of one composer's bubbles. ';' follows
// ':' , so the range stops at the next composer.
func cursorBubbleSpan(id string) (lo, hi string) {
	return "bubbleId:" + id + ":", "bubbleId:" + id + ";"
}

// openCursorApp opens state.vscdb without writing a byte beside it. SQLite
// opening it even read-only writes its read marks into -shm, in Cursor's
// own folder. A file with no log is opened immutable. One with a log is
// read from a copy of that log beside a hard link of the database (or a
// copy, when the database is small or a link can't be made), in a folder
// of magpie's own that cleanup removes.
func openCursorApp(path string) (db *sql.DB, cleanup func(), err error) {
	open := func(p, query string) (*sql.DB, error) {
		u := url.URL{Scheme: "file", Path: filepath.ToSlash(p), RawQuery: query}
		if len(u.Path) >= 2 && u.Path[1] == ':' {
			u.Path = "/" + u.Path
		}
		db, err := sql.Open("sqlite", u.String())
		if err != nil {
			return nil, err
		}
		if err := db.Ping(); err != nil {
			db.Close()
			return nil, err
		}
		return db, nil
	}
	wal := path + "-wal"
	fi, statErr := os.Stat(wal)
	if statErr != nil || fi.Size() == 0 {
		db, err := open(path, "immutable=1")
		if err != nil {
			return nil, nil, err
		}
		return db, func() { db.Close() }, nil
	}
	tmp, err := os.MkdirTemp("", "magpie-cursor-app-")
	if err != nil {
		return nil, nil, err
	}
	gone := func() { os.RemoveAll(tmp) }
	dest := filepath.Join(tmp, filepath.Base(path))
	info, err := os.Stat(path)
	if err != nil {
		gone()
		return nil, nil, err
	}
	linked := false
	if info.Size() > 64<<20 {
		linked = os.Link(path, dest) == nil
	}
	if !linked {
		if err := copyFile(path, dest); err != nil {
			gone()
			if info.Size() > 64<<20 {
				db, err := open(path, "immutable=1")
				if err != nil {
					return nil, nil, err
				}
				return db, func() { db.Close() }, nil
			}
			return nil, nil, err
		}
	}
	if err := copyFile(wal, dest+"-wal"); err != nil {
		gone()
		return nil, nil, err
	}
	db, err = open(dest, "mode=ro&_pragma=busy_timeout(3000)")
	if err != nil {
		gone()
		return nil, nil, err
	}
	return db, func() { db.Close(); gone() }, nil
}

// parseCursorApp reads one composer from the snapshot cursorAppFiles opened.
func parseCursorApp(f file) *state {
	s := &state{Size: f.size, Mod: f.mod.UnixNano()}
	if f.app == nil || f.app.db == nil || f.sid == "" {
		return s
	}
	var raw string
	var created, updated int64
	if f.app.db.QueryRow(`SELECT COALESCE(value, ''), COALESCE(createdAt, 0), COALESCE(lastUpdatedAt, 0) FROM composerHeaders WHERE composerId = ?`, f.sid).Scan(&raw, &created, &updated) != nil {
		return s
	}
	var h cursorAppHeader
	if json.Unmarshal([]byte(raw), &h) != nil {
		return s
	}
	s.Cwd = h.cwd()
	if f.main {
		s.Named = cursorTitle(h.Name)
		if s.Named == "" {
			s.Named = title(h.Subtitle)
		}
	}
	if created == 0 {
		created = h.CreatedAt
	}
	if updated == 0 {
		updated = h.LastUpdatedAt
	}
	s.saw(ms(created), f.main)
	s.saw(ms(updated), f.main)
	lo, hi := cursorBubbleSpan(f.sid)
	var n int64
	if f.app.db.QueryRow(`SELECT COUNT(*) FROM cursorDiskKV WHERE key >= ? AND key < ?`, lo, hi).Scan(&n) != nil || n > cursorAppBubbleMax {
		return s
	}
	var in, out, prompts, replies int64
	if f.app.db.QueryRow(`SELECT
		COALESCE(SUM(json_extract(value, '$.tokenCount.inputTokens')), 0),
		COALESCE(SUM(json_extract(value, '$.tokenCount.outputTokens')), 0),
		COALESCE(SUM(CASE WHEN json_extract(value, '$.type') = 1 THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN json_extract(value, '$.type') = 2 THEN 1 ELSE 0 END), 0)
		FROM cursorDiskKV WHERE key >= ? AND key < ?`, lo, hi).Scan(&in, &out, &prompts, &replies) != nil {
		return s
	}
	model := "default"
	var named string
	if f.app.db.QueryRow(`SELECT COALESCE(json_extract(value, '$.modelConfig.modelName'), '') FROM cursorDiskKV WHERE key = ?`, "composerData:"+f.sid).Scan(&named) == nil {
		if named = strings.TrimSpace(named); named != "" {
			model = named
		}
	}
	when := s.Last
	if when.IsZero() {
		when = f.mod
	}
	s.use(dateOf(when), model, Tokens{Input: int(in), Output: int(out)})
	if f.main && !when.IsZero() {
		d := s.day(dateOf(when))
		d.Prompts += int(prompts)
		d.Replies += int(replies)
	}
	return s
}
