package service

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	apperr "github.com/thedavidweng/tg-drive-cli/core/errors"
	"github.com/thedavidweng/tg-drive-cli/core/fsmodel"
	"github.com/thedavidweng/tg-drive-cli/core/manifest"
	"github.com/thedavidweng/tg-drive-cli/core/pathcodec"
	"github.com/thedavidweng/tg-drive-cli/core/ports"
	"github.com/thedavidweng/tg-drive-cli/core/telegram"
)

// scanIndexChunk is how many scanned files are committed per transaction.
const scanIndexChunk = 200

// ScanOptions controls scan behavior.
type ScanOptions struct {
	Full           bool
	Strict         bool
	Repair         bool
	IncludeDeleted bool
	Root           string
}

// Scan rebuilds or incrementally updates the index. Existing index state is
// never modified before Telegram history has been fetched and proven complete,
// so a failed or aborted scan cannot corrupt the local cache.
func (a *App) Scan(ctx context.Context, opts ScanOptions) (map[string]any, error) {
	channelID, tgID, err := a.channelID(ctx)
	if err != nil {
		return nil, err
	}
	tgChID, err := a.tgChannelID(ctx)
	if err != nil {
		return nil, err
	}
	root := "/"
	if opts.Root != "" {
		root, err = fsmodel.NormalizeCanonicalPath(opts.Root)
		if err != nil {
			return nil, err
		}
	}
	inRoot := func(p string) bool {
		return root == "/" || p == root || strings.HasPrefix(p, root+"/")
	}
	var afterID int
	mode := "full"
	if !opts.Full {
		mode = "incremental"
		_ = a.DB.Raw().QueryRowContext(ctx, `select coalesce(last_scanned_message_id,0) from scan_state where channel_id=?`, channelID).Scan(&afterID)
	}

	r := &scanRun{
		app:       a,
		channelID: channelID,
		tgChID:    tgChID,
		opts:      opts,
		now:       time.Now().UTC().Format(time.RFC3339),

		byID:            map[int]telegram.Message{},
		seenMsgIDs:      map[int]bool{},
		seenMedia:       map[int]bool{},
		seen:            map[string]bool{},
		manifestByMedia: map[int]manifestReply{},
		commentByMedia:  map[int]manifestReply{},
		albumGroups:     map[int64][]int{},
		coveredGroups:   map[int64]bool{},
		albumCorrupt:    map[int64]bool{},
		paths:           newScanPathIndex(),
	}

	if !opts.Full {
		// Incremental scans reconcile against the live index, so conflict
		// bookkeeping starts from the indexed active paths.
		active, err := a.activePaths(ctx, channelID)
		if err != nil {
			return nil, err
		}
		r.paths.seed(active)
	}
	slugs, err := a.loadExistingSlugs(ctx, channelID)
	if err != nil {
		return nil, err
	}
	r.slugMap = slugs
	r.pendingErrs, err = a.pendingScanErrorIDs(ctx, channelID)
	if err != nil {
		return nil, err
	}

	// A full scan with a recorded checkpoint continues an interrupted run.
	// Work the interrupted run committed is identified by row writes at or
	// after the run's start timestamp (a message id alone cannot distinguish
	// committed work from messages that arrived since).
	resumed := false
	var scanStartedAt string
	if opts.Full {
		var checkpoint sql.NullInt64
		_ = a.DB.Raw().QueryRowContext(ctx, `select checkpoint_message_id, coalesce(full_scan_started_at,'') from scan_state where channel_id=?`, channelID).Scan(&checkpoint, &scanStartedAt)
		if checkpoint.Valid && checkpoint.Int64 > 0 && scanStartedAt != "" {
			committed, err := a.committedSince(ctx, channelID, scanStartedAt)
			if err != nil {
				return nil, err
			}
			r.committed = committed
			resumed = true
		}
	}

	// Stream the channel newest-first. Reply-resolution state stays in memory
	// (bounded by message count); the raw message slice is never materialized.
	// Errors noticed while streaming are deferred: if the read turns out to be
	// truncated, nothing at all is written.
	streamMeta, streamErr := a.TG.StreamHistory(ctx, tgChID, afterID, func(msg telegram.Message) error {
		msg.Data = nil
		r.byID[msg.ID] = msg
		r.seenMsgIDs[msg.ID] = true
		if msg.ID > r.maxID {
			r.maxID = msg.ID
		}
		if msg.GroupedID != 0 && (msg.Kind == telegram.KindDocument || msg.Kind == telegram.KindPhoto) {
			r.albumGroups[msg.GroupedID] = append(r.albumGroups[msg.GroupedID], msg.ID)
		}
		if msg.Text == "" || msg.ReplyTo == nil {
			return nil
		}
		if manifest.IsAlbumReply(msg.Text) {
			album, err := manifest.ParseAlbumReply(msg.Text)
			if err != nil {
				// The group cannot be read from the corrupt text; resolve it
				// from the media the reply points at once streaming finishes.
				replyTo := 0
				if msg.ReplyTo != nil {
					replyTo = *msg.ReplyTo
				}
				r.corruptReplies = append(r.corruptReplies, replyTo)
				r.deferredErrors = append(r.deferredErrors, deferredScanError{
					messageID: msg.ID,
					code:      apperr.ErrAlbumInventoryInvalid,
					message:   "album inventory unparseable: " + err.Error(),
					excerpt:   truncate(msg.Text, 200),
				})
				return nil
			}
			r.albumInventories = append(r.albumInventories, albumInventory{replyID: msg.ID, meta: album})
			r.coveredGroups[album.GroupedID] = true
			return nil
		}
		if meta, err := manifest.ParseManifestReply(msg.Text); err == nil {
			if _, dup := r.manifestByMedia[*msg.ReplyTo]; !dup {
				r.manifestByMedia[*msg.ReplyTo] = manifestReply{meta: meta, msgID: msg.ID}
			}
		}
		return nil
	})
	if streamErr != nil {
		return nil, telegram.MapError(streamErr)
	}
	if !streamMeta.Complete {
		// A truncated read must never feed the missing-finalizer: abort with a
		// typed error and leave the index untouched.
		return nil, apperr.New(apperr.ErrScanIncomplete,
			fmt.Sprintf("history read stopped early (oldest message seen: %d, channel reports %d messages); "+
				"the index was not modified — rerun the scan", streamMeta.OldestID, streamMeta.TotalMessages))
	}
	// Resolve corrupt inventories' groups from the media they replied to, so
	// those groups do not also report a missing inventory.
	for _, mediaID := range r.corruptReplies {
		if media, ok := r.byID[mediaID]; ok && media.GroupedID != 0 {
			r.albumCorrupt[media.GroupedID] = true
		}
	}
	// Walk the linked discussion group (ADR 0018): forwarded headers map
	// thread roots back to channel posts; comments carry the machine
	// records.
	discMaxID, err := a.collectDiscussionComments(ctx, r)
	if err != nil {
		return nil, err
	}
	for _, de := range r.deferredErrors {
		r.recordScanError(ctx, de.messageID, de.code, de.message, de.excerpt)
	}
	// Collect index operations from the three metadata sources.
	var ops []scanIndexOp
	r.collectAlbumOps(ctx, inRoot, &ops)
	r.collectManifestReplyOps(ctx, inRoot, &ops)
	r.collectCaptionOps(ctx, inRoot, &ops)

	// Deterministic slug assignment: index in message-id order — the same
	// chronological order uploads were assigned in — so collision fallback
	// suffixes come out identically on every rebuild and match the tag chains
	// already baked into Telegram captions.
	sort.Slice(ops, func(i, j int) bool {
		if ops[i].messageID != ops[j].messageID {
			return ops[i].messageID < ops[j].messageID
		}
		return ops[i].meta.CanonicalPath < ops[j].meta.CanonicalPath
	})
	for _, op := range ops {
		if op.messageID > r.maxOpID {
			r.maxOpID = op.messageID
		}
	}

	// Album groups whose inventory never arrived are scan errors, not silent
	// data loss: the members would vanish from the index.
	r.recordMissingAlbumInventories(ctx)

	if resumed {
		// Seed seen/conflict state from the committed head of the interrupted
		// run so the finalizer keeps those rows and no op repeats. Rows the
		// run committed are exactly those it wrote; messages that arrived
		// since have no rows and still get processed.
		for _, op := range ops {
			if r.committed[op.messageID] {
				r.seen[op.meta.CanonicalPath] = true
				r.seenMedia[op.messageID] = true
				r.paths.addFile(op.meta.CanonicalPath)
			}
		}
	} else if opts.Full {
		if _, err := a.DB.Raw().ExecContext(ctx, `
			insert into scan_state(channel_id,last_scanned_message_id,checkpoint_message_id,full_scan_started_at,updated_at) values(?,0,NULL,?,?)
			on conflict(channel_id) do update set checkpoint_message_id=NULL, full_scan_started_at=excluded.full_scan_started_at, updated_at=excluded.updated_at`,
			channelID, r.now, r.now); err != nil {
			return nil, apperr.Wrap(apperr.ErrDB, "begin full scan", err)
		}
	}

	var pendingOps []scanIndexOp
	if resumed {
		for _, op := range ops {
			if !r.committed[op.messageID] {
				pendingOps = append(pendingOps, op)
			}
		}
	} else {
		pendingOps = ops
	}

	// Commit in chunks; the checkpoint advances only after its chunk commits.
	for start := 0; start < len(pendingOps); start += scanIndexChunk {
		end := start + scanIndexChunk
		if end > len(pendingOps) {
			end = len(pendingOps)
		}
		chunk := pendingOps[start:end]
		if err := r.commitChunk(ctx, chunk, pendingOps[end:]); err != nil {
			return nil, err
		}
	}

	if opts.Full {
		err = a.DB.WithTx(ctx, func(tx *sql.Tx) error {
			// Mark previously active files absent from this full pass missing.
			rows, err := tx.QueryContext(ctx, `select id, canonical_path from files where channel_id=? and status='active'`, channelID)
			if err != nil {
				return err
			}
			type staleRow struct {
				id int64
				cp string
			}
			var stale []staleRow
			for rows.Next() {
				var sr staleRow
				if err := rows.Scan(&sr.id, &sr.cp); err != nil {
					_ = rows.Close()
					return err
				}
				if inRoot(sr.cp) && !r.seen[sr.cp] {
					stale = append(stale, sr)
				}
			}
			if err := rows.Err(); err != nil {
				_ = rows.Close()
				return err
			}
			_ = rows.Close()
			for _, sr := range stale {
				if _, err := tx.ExecContext(ctx, `update files set status='missing', node_id=null, updated_at=? where id=?`, r.now, sr.id); err != nil {
					return err
				}
			}
			// Resolve scan errors whose message disappeared from the channel.
			errRows, err := tx.QueryContext(ctx, `select id, message_id from scan_errors where channel_id=? and status='pending'`, channelID)
			if err != nil {
				return err
			}
			var gone []int64
			for errRows.Next() {
				var id int64
				var msgID sql.NullInt64
				if err := errRows.Scan(&id, &msgID); err != nil {
					_ = errRows.Close()
					return err
				}
				if msgID.Valid && !r.seenMsgIDs[int(msgID.Int64)] {
					gone = append(gone, id)
				}
			}
			if err := errRows.Err(); err != nil {
				_ = errRows.Close()
				return err
			}
			_ = errRows.Close()
			for _, id := range gone {
				if _, err := tx.ExecContext(ctx, `update scan_errors set status='resolved', resolved_at=? where id=?`, r.now, id); err != nil {
					return err
				}
			}
			return r.rebuildNodesTx(ctx, tx, channelID, r.now)
		})
		if err != nil {
			return nil, apperr.Wrap(apperr.ErrDB, "finalize full scan", err)
		}
	} else {
		_ = a.DB.RunDirectoryGC(ctx, channelID)
	}
	if opts.Repair {
		if err := a.DB.WithTx(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, `delete from scan_errors where channel_id=? and status='resolved'`, channelID); err != nil {
				return err
			}
			return r.rebuildNodesTx(ctx, tx, channelID, r.now)
		}); err != nil {
			return nil, apperr.Wrap(apperr.ErrDB, "repair", err)
		}
	}

	fullAt := ""
	if opts.Full {
		fullAt = r.now
		// The scan finished (finalizer committed): clear the checkpoint so the
		// next full scan starts fresh.
		_, _ = a.DB.Raw().ExecContext(ctx, `
			insert into scan_state(channel_id,last_scanned_message_id,last_full_scan_at,checkpoint_message_id,discussion_last_scanned_message_id,updated_at) values(?,?,?,?,?,?)
			on conflict(channel_id) do update set last_scanned_message_id=excluded.last_scanned_message_id,
				last_full_scan_at=excluded.last_full_scan_at, checkpoint_message_id=NULL,
				discussion_last_scanned_message_id=excluded.discussion_last_scanned_message_id, updated_at=excluded.updated_at`,
			channelID, r.maxID, fullAt, nil, discMaxID, r.now)
	} else {
		// Incremental passes never touch the checkpoint of an interrupted
		// full scan.
		_, _ = a.DB.Raw().ExecContext(ctx, `
			insert into scan_state(channel_id,last_scanned_message_id,discussion_last_scanned_message_id,updated_at) values(?,?,?,?)
			on conflict(channel_id) do update set last_scanned_message_id=excluded.last_scanned_message_id,
				discussion_last_scanned_message_id=excluded.discussion_last_scanned_message_id, updated_at=excluded.updated_at`,
			channelID, r.maxID, discMaxID, r.now)
	}

	if r.strictFailure != "" {
		return nil, apperr.New(apperr.ErrScanFailed, r.strictFailure)
	}

	counts := map[string]int{}
	rows, _ := a.DB.Raw().QueryContext(ctx, `select status, count(*) from files where channel_id=? group by status`, channelID)
	if rows != nil {
		func() {
			defer func() { _ = rows.Close() }()
			for rows.Next() {
				var s string
				var n int
				_ = rows.Scan(&s, &n)
				counts[s] = n
			}
		}()
	}
	out := map[string]any{
		"mode":    mode,
		"channel": tgID,
		"active":  counts["active"],
		"deleted": counts["deleted"],
		"invalid": r.invalid + counts["invalid"],
		"missing": counts["missing"],
	}
	if resumed && opts.Full {
		out["resumed"] = true
	}
	if opts.IncludeDeleted {
		out["tombstones"] = r.tombstones
	}
	if !opts.Full {
		var lastFull string
		_ = a.DB.Raw().QueryRowContext(ctx, `select coalesce(last_full_scan_at,'') from scan_state where channel_id=?`, channelID).Scan(&lastFull)
		warn := ""
		if lastFull == "" {
			warn = "no full scan recorded; incremental scans cannot detect old-message edits or deletions — run td scan --full"
		} else if t, err := time.Parse(time.RFC3339, lastFull); err == nil && time.Since(t) > 7*24*time.Hour {
			warn = "last full scan was " + lastFull + "; run td scan --full to detect old-message drift"
		}
		if warn != "" {
			out["full_scan_warning"] = warn
		}
	}
	return out, nil
}

// scanRun carries one scan pass's in-memory state.
type scanRun struct {
	app       *App
	channelID int64
	tgChID    int64
	opts      ScanOptions
	now       string

	byID            map[int]telegram.Message
	seenMsgIDs      map[int]bool
	seenMedia       map[int]bool
	seen            map[string]bool
	manifestByMedia map[int]manifestReply
	// commentByMedia holds td-manifest:v1 records posted as comment threads
	// (ADR 0018), keyed by the media message id the comment sits on. The
	// newest comment per post wins.
	commentByMedia map[int]manifestReply
	// manifestChat is the discussion group's Telegram channel id when the
	// channel has one; comment-carrier ops record it on their rows.
	manifestChat     string
	albumGroups      map[int64][]int
	coveredGroups    map[int64]bool
	albumCorrupt     map[int64]bool
	albumInventories []albumInventory
	deferredErrors   []deferredScanError
	corruptReplies   []int
	maxID            int
	maxOpID          int
	committed        map[int]bool

	paths       *scanPathIndex
	slugMap     map[string]string
	pendingErrs map[int]bool

	invalid       int
	tombstones    int
	strictFailure string
}

type manifestReply struct {
	meta  manifest.ParsedMeta
	msgID int
}

// deferredScanError is a scan error noticed before history completion was
// proven; it is only written once the read is known to be complete.
type deferredScanError struct {
	messageID int
	code      string
	message   string
	excerpt   string
}

type albumInventory struct {
	replyID int
	// chat is the carrier peer: empty for a legacy in-channel reply, the
	// discussion group id for a comment inventory (ADR 0018).
	chat string
	meta manifest.AlbumMeta
}

// scanIndexOp is one file to write into the index.
type scanIndexOp struct {
	messageID     int
	manifestMsgID int
	// manifestChat is the carrier peer of manifestMsgID (ADR 0018).
	manifestChat string
	meta         manifest.ParsedMeta
}

// pendingScanErrorIDs preloads which messages have unresolved scan errors so
// resolution statements only run when an error actually existed.
func (a *App) pendingScanErrorIDs(ctx context.Context, channelID int64) (map[int]bool, error) {
	rows, err := a.DB.Raw().QueryContext(ctx, `select message_id from scan_errors where channel_id=? and status='pending' and message_id is not null`, channelID)
	if err != nil {
		return nil, apperr.Wrap(apperr.ErrDB, "list scan errors", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[int]bool{}
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, apperr.Wrap(apperr.ErrDB, "scan rows", err)
		}
		out[id] = true
	}
	return out, rows.Err()
}

// committedSince returns the message ids whose file rows were written at or
// after since. An interrupted full scan uses it to identify the work it
// already committed; rows written by concurrent operations in the same window
// are equally current and safely skipped.
func (a *App) committedSince(ctx context.Context, channelID int64, since string) (map[int]bool, error) {
	rows, err := a.DB.Raw().QueryContext(ctx, `
		select message_id from files
		where channel_id=? and message_id is not null and updated_at >= ?`, channelID, since)
	if err != nil {
		return nil, apperr.Wrap(apperr.ErrDB, "list committed scan work", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[int]bool{}
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, apperr.Wrap(apperr.ErrDB, "scan rows", err)
		}
		out[id] = true
	}
	return out, rows.Err()
}

// scanPathIndex is the O(1) conflict bookkeeping for one scan pass: which
// paths are files, which are directories, and which directories have active
// descendants.
type scanPathIndex struct {
	entries   map[string]bool // path -> isDir
	dirsInUse map[string]bool // dir path with active file descendants
}

func newScanPathIndex() *scanPathIndex {
	return &scanPathIndex{entries: map[string]bool{}, dirsInUse: map[string]bool{}}
}

func (p *scanPathIndex) seed(active []fsmodel.ActivePath) {
	for _, ap := range active {
		if ap.IsDir {
			p.entries[ap.Canonical] = true
		} else {
			p.addFile(ap.Canonical)
		}
	}
}

// addFile records a file path and marks its ancestor directories in use.
func (p *scanPathIndex) addFile(canonical string) {
	p.entries[canonical] = false
	for _, anc := range fsmodel.AncestorPaths(canonical) {
		p.entries[anc] = true
		p.dirsInUse[anc] = true
	}
}

// conflict mirrors fsmodel.CheckUploadConflict for a scan op. The op's own
// path is excluded (re-indexing a path replaces whatever sits there), matching
// the pre-scan filtered conflict check.
func (p *scanPathIndex) conflict(dest string) error {
	for _, anc := range fsmodel.AncestorPaths(dest) {
		if isDir, ok := p.entries[anc]; ok && !isDir {
			return apperr.New(apperr.ErrPathAncestorIsFile, "ancestor path is a file: "+anc)
		}
	}
	if p.dirsInUse[dest] {
		return apperr.New(apperr.ErrPathIsDirectory, "destination has active descendants: "+dest)
	}
	return nil
}

// recordScanError upserts a scan error row for one message.
func (r *scanRun) recordScanError(ctx context.Context, messageID int, code, message, excerpt string) {
	r.invalid++
	if r.opts.Strict && r.strictFailure == "" {
		r.strictFailure = message
	}
	_, _ = r.app.DB.Raw().ExecContext(ctx, `
		insert into scan_errors(channel_id,message_id,error_code,error_message,raw_excerpt,status,first_seen_at,last_seen_at)
		values(?,?,?,?,?,'pending',?,?)
		on conflict(channel_id,message_id,error_code) do update set last_seen_at=excluded.last_seen_at, status='pending'`,
		r.channelID, messageID, code, message, excerpt, r.now, r.now)
}

// commitChunk conflict-checks, resolves rows, and commits one chunk of index
// ops in a single batched transaction, then advances the checkpoint past the
// committed work.
func (r *scanRun) commitChunk(ctx context.Context, chunk, rest []scanIndexOp) error {
	a := r.app
	var reqs []ports.FileIndexRequest
	var resolveIDs []int
	// Ops are sorted by (path, messageID), so a second claim on a path inside
	// one chunk is always the newer message. The DB duplicate-claim guard
	// cannot see rows that are only queued in this batch, so the newest claim
	// displaces the older one here — the same newest-message-wins rule the
	// guard applies to committed rows. Displaced claims become scan errors.
	claim := map[string]scanIndexOp{}
	for _, op := range chunk {
		if prev, dup := claim[op.meta.CanonicalPath]; dup {
			r.recordScanError(ctx, prev.messageID, apperr.ErrPathConflict,
				fmt.Sprintf("older duplicate of %s (newer message %d wins)", op.meta.CanonicalPath, op.messageID),
				op.meta.CanonicalPath)
		}
		claim[op.meta.CanonicalPath] = op
	}
	for _, op := range chunk {
		if c, ok := claim[op.meta.CanonicalPath]; !ok || c.messageID != op.messageID {
			continue
		}
		// Conflict-check here, after earlier winners registered their paths,
		// so file/dir collisions inside one chunk are caught too.
		if conflictErr := r.paths.conflict(op.meta.CanonicalPath); conflictErr != nil {
			code := apperr.ErrPathInvalid
			if ae, ok := apperr.As(conflictErr); ok {
				code = ae.Code
			}
			r.recordScanError(ctx, op.messageID, code, conflictErr.Error(), truncate(op.meta.CanonicalPath, 200))
			continue
		}
		fileID, supersedeID, ok, err := r.resolveRow(ctx, op)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if supersedeID > 0 {
			if _, err := a.DB.Raw().ExecContext(ctx, `update files set status='superseded', node_id=null, updated_at=? where id=?`, r.now, supersedeID); err != nil {
				return apperr.Wrap(apperr.ErrDB, "supersede duplicate", err)
			}
		}
		req, err := r.buildIndexReq(ctx, op, fileID)
		if err != nil {
			return err
		}
		if req == nil {
			continue
		}
		reqs = append(reqs, *req)
		r.paths.addFile(op.meta.CanonicalPath)
		if r.pendingErrs[op.messageID] {
			resolveIDs = append(resolveIDs, op.messageID)
		}
	}
	if err := a.fileIndex().IndexBatch(ctx, reqs); err != nil {
		return apperr.Wrap(apperr.ErrDB, "index scanned files", err)
	}
	for _, id := range resolveIDs {
		_, _ = a.DB.Raw().ExecContext(ctx, `update scan_errors set status='resolved', resolved_at=? where channel_id=? and message_id=? and status='pending'`, r.now, r.channelID, id)
	}
	if !r.opts.Full {
		return nil
	}
	// The checkpoint advances only after the chunk committed. Its value is an
	// upper bound on the message ids still uncommitted (informational and for
	// status output): resume identifies the interrupted run's committed work
	// by row timestamps, not ids — a non-null checkpoint marks the run as
	// interrupted and keeps the next full scan resuming.
	var checkpoint any = 1
	if r.maxOpID > 1 {
		checkpoint = r.maxOpID
	}
	if len(rest) > 0 {
		highest := rest[0].messageID
		for _, op := range rest[1:] {
			if op.messageID > highest {
				highest = op.messageID
			}
		}
		checkpoint = highest
	}
	_, err := a.DB.Raw().ExecContext(ctx, `
		update scan_state set checkpoint_message_id=?, updated_at=? where channel_id=?`,
		checkpoint, r.now, r.channelID)
	if err != nil {
		return apperr.Wrap(apperr.ErrDB, "advance checkpoint", err)
	}
	return nil
}

// buildIndexReq renders one scan op into an index request (slug chain, tags),
// or returns nil (with a scan error recorded) when the chain cannot be built.
func (r *scanRun) buildIndexReq(ctx context.Context, op scanIndexOp, fileID int64) (*ports.FileIndexRequest, error) {
	meta := manifest.FileMeta{
		CanonicalPath: op.meta.CanonicalPath,
		DisplayName:   op.meta.DisplayName,
		Size:          op.meta.Size,
		Hash:          op.meta.Hash,
		MIME:          op.meta.MIME,
		Created:       op.meta.Created,
	}
	if meta.DisplayName == "" {
		meta.DisplayName = fsmodel.BaseName(meta.CanonicalPath)
	}
	tags, slugMaps, err := pathcodec.GenerateChain(meta.CanonicalPath, r.slugMap)
	if err != nil {
		r.recordScanError(ctx, op.messageID, apperr.ErrSlugCollision, err.Error(), truncate(meta.CanonicalPath, 200))
		return nil, nil
	}
	meta.Tags = tags
	return &ports.FileIndexRequest{
		ChannelRowID:   r.channelID,
		FileID:         fileID,
		MessageID:      op.messageID,
		ManifestMsgID:  op.manifestMsgID,
		ManifestChatID: op.manifestChat,
		Meta:           meta,
		SlugMaps:       slugMaps,
		Tags:           tags,
		SetUploadedAt:  fileID == 0,
		Now:            r.now,
	}, nil
}

// resolveRow finds the file row a scanned message maps to: first by message_id
// (a message belongs to exactly one row), then by a reactivatable row at the
// path. Superseded/deleted history rows are never resurrected by path. The
// duplicate-claim guard keeps the newest message at a path; older duplicates
// become scan errors.
func (r *scanRun) resolveRow(ctx context.Context, op scanIndexOp) (fileID, supersedeID int64, ok bool, err error) {
	a := r.app
	err = a.DB.Raw().QueryRowContext(ctx, `select id from files where channel_id=? and message_id=?`,
		r.channelID, op.messageID).Scan(&fileID)
	if err == sql.ErrNoRows {
		err = a.DB.Raw().QueryRowContext(ctx, `select id from files where channel_id=? and canonical_path=? and status in ('active','missing')`,
			r.channelID, op.meta.CanonicalPath).Scan(&fileID)
	}
	if err != nil && err != sql.ErrNoRows {
		return 0, 0, false, apperr.Wrap(apperr.ErrDB, "lookup scanned file", err)
	}
	if err == sql.ErrNoRows {
		fileID = 0
	}
	var otherID int64
	var otherMsg sql.NullInt64
	dupErr := a.DB.Raw().QueryRowContext(ctx, `select id, message_id from files where channel_id=? and canonical_path=? and status='active' and id != ?`,
		r.channelID, op.meta.CanonicalPath, fileID).Scan(&otherID, &otherMsg)
	if dupErr == nil && otherID != 0 {
		if otherMsg.Valid && int(otherMsg.Int64) > op.messageID {
			r.recordScanError(ctx, op.messageID, apperr.ErrPathConflict,
				fmt.Sprintf("older duplicate of %s (newer message %d wins)", op.meta.CanonicalPath, otherMsg.Int64),
				op.meta.CanonicalPath)
			return 0, 0, false, nil
		}
		supersedeID = otherID
	}
	return fileID, supersedeID, true, nil
}

// rebuildNodesTx truncates derived directory nodes and rebuilds them from the
// current active file set.
func (r *scanRun) rebuildNodesTx(ctx context.Context, tx *sql.Tx, channelID int64, now string) error {
	if _, err := tx.ExecContext(ctx, `delete from nodes where channel_id=? and derived=1`, channelID); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `select canonical_path from files where channel_id=? and status='active'`, channelID)
	if err != nil {
		return err
	}
	var paths []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			_ = rows.Close()
			return err
		}
		paths = append(paths, p)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	_ = rows.Close()
	for anc, name := range fsmodel.DeriveDirectoryNodes(paths) {
		if _, err := tx.ExecContext(ctx, `insert or ignore into nodes(channel_id,canonical_path,parent_path,display_name,type,derived,created_at,updated_at) values(?,?,?,?,'dir',1,?,?)`,
			channelID, anc, fsmodel.ParentPath(anc), name, now, now); err != nil {
			return err
		}
	}
	return nil
}

// truncate shortens s to at most n runes without splitting a multi-byte glyph.
func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}
