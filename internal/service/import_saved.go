package service

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/thedavidweng/tg-drive-cli/adapters/native/sqlitestore"
	apperr "github.com/thedavidweng/tg-drive-cli/core/errors"
	"github.com/thedavidweng/tg-drive-cli/core/fsmodel"
	"github.com/thedavidweng/tg-drive-cli/core/manifest"
	"github.com/thedavidweng/tg-drive-cli/core/telegram"
	"lukechampine.com/blake3"
)

// td import saved: re-home Saved Messages content into the drive channel.
//
// Saved Messages holds forwarded copies whose bytes belong to the origin
// channel; when that channel deletes the post, the saved copy dies with it.
// The import therefore re-uploads rather than claiming in place (which is
// what td adopt does for content already in the drive channel): each saved
// item is downloaded, republished as a fresh td upload with the original
// caption kept on top, and annotated with a td-origin:v1 provenance record.
// Items whose bytes already live in the tree are skipped, and their captions
// preserved in a td-dupe:v1 record so the text does not die with the source.

// DefaultImportSavedInto is where imported saved content lands by default.
const DefaultImportSavedInto = "/saved"

// Photo presentation choices for --photos-as.
const (
	PhotosAsDocument = telegram.KindDocument
	PhotosAsPhoto    = telegram.KindPhoto
)

// Item kinds reported by the import plan.
const (
	importKindDocument = "document"
	importKindPhoto    = "photo"
	importKindVideo    = "video"
	importKindText     = "text"
)

// ImportSavedOptions controls td import saved.
type ImportSavedOptions struct {
	// MessageIDs limits the import to specific saved messages; empty imports
	// the whole saved chat.
	MessageIDs []int
	// Into is the destination directory (DefaultImportSavedInto when empty).
	Into string
	// PhotosAs selects how photo messages are republished. It has no default
	// on purpose: documents keep the bytes (hash-verifiable), native photos
	// are recompressed by Telegram, and neither is a safe silent choice.
	PhotosAs string
	// Policy resolves destination path conflicts, as in td cp.
	Policy ConflictPolicy
	// MergeCaptions appends a skipped duplicate's caption to the matched
	// file's caption instead of only recording it.
	MergeCaptions bool
	// DeleteSource deletes the saved originals of published and duplicate
	// items after verification. Failures are never deleted.
	DeleteSource bool
	// NoDedupe uploads even when the content hash already exists in the tree.
	NoDedupe bool
	DryRun   bool
	// ContinueErr keeps the batch going after a per-item failure.
	ContinueErr bool
	// PhotoPrompt is consulted when the plan holds photos and PhotosAs was
	// not set. A nil prompt (non-interactive runs) turns the missing choice
	// into a usage error rather than a silent default.
	PhotoPrompt func(photoCount int) (string, error)
	// Emit optionally receives per-item progress events.
	Emit func(event string, payload any)
}

// ImportSavedItem is one saved message in the plan or the result.
type ImportSavedItem struct {
	MessageID int    `json:"message_id"`
	Kind      string `json:"kind"`
	Action    string `json:"action"`
	Path      string `json:"path,omitempty"`
	Size      int64  `json:"size,omitempty"`
	Hash      string `json:"hash,omitempty"`
	MIME      string `json:"mime,omitempty"`
	Reason    string `json:"reason,omitempty"`
	// GroupedID is the saved album the message belongs to, 0 when none.
	GroupedID int64 `json:"grouped_id,omitempty"`
	// SubChat is the Saved Messages sub-chat the item came from.
	SubChat string `json:"sub_chat,omitempty"`
	// NewMessageID is the republished message in the drive channel.
	NewMessageID int `json:"new_message_id,omitempty"`
	// DuplicateOf names the existing file whose bytes matched.
	DuplicateOf string `json:"duplicate_of,omitempty"`
	// DupeRecordID and OriginRecordID are the provenance comments written
	// for this item.
	DupeRecordID   int `json:"dupe_record_id,omitempty"`
	OriginRecordID int `json:"origin_record_id,omitempty"`
	// CaptionMerged reports that the duplicate's caption was merged into the
	// matched file's caption.
	CaptionMerged bool `json:"caption_merged,omitempty"`
	// SourceDeleted reports that the saved original was deleted.
	SourceDeleted bool `json:"source_deleted,omitempty"`
	// Error carries a per-item failure that did not stop the batch.
	Error string `json:"error,omitempty"`
}

// ImportSavedResult is the dry-run plan or the execution summary.
type ImportSavedResult struct {
	DryRun   bool   `json:"dry_run"`
	Source   string `json:"source"`
	Into     string `json:"into"`
	PhotosAs string `json:"photos_as,omitempty"`
	// HistoryComplete reports whether the saved-chat read provably reached
	// the end of history. A partial read imports less, never wrongly.
	HistoryComplete bool              `json:"history_complete"`
	Imported        int               `json:"imported"`
	Skipped         int               `json:"skipped"`
	Failed          int               `json:"failed"`
	Duplicates      int               `json:"duplicates"`
	CaptionsMerged  int               `json:"captions_merged"`
	SourcesDeleted  int               `json:"sources_deleted"`
	Photos          int               `json:"photos"`
	Items           []ImportSavedItem `json:"items"`
}

// importUnit is one publishable group: a single saved message, or the members
// of one saved album that share a presentation kind.
type importUnit struct {
	groupedID int64
	kind      string
	caption   string
	items     []*ImportSavedItem
	msgs      []telegram.Message
	pres      []Presentation
	// conflicts holds a per-item destination conflict whose verdict waits for
	// the content hash: an occupied destination may well be this very item,
	// imported by an earlier run, and that is a duplicate, not a collision.
	conflicts []error
}

// importTarget is an active, hash-bearing file the import can recognize as
// already holding a saved item's bytes.
type importTarget struct {
	path         string
	messageID    int
	manifestChat string
}

// ImportSaved plans and, unless DryRun is set, executes the saved-chat import.
func (a *App) ImportSaved(ctx context.Context, opts ImportSavedOptions) (*ImportSavedResult, error) {
	switch opts.PhotosAs {
	case "", PhotosAsDocument, PhotosAsPhoto:
	default:
		return nil, apperr.New(apperr.ErrUsage,
			fmt.Sprintf("unknown --photos-as value %q (want document or photo)", opts.PhotosAs))
	}
	if opts.Policy == "" {
		opts.Policy = ConflictFail
	}
	into := opts.Into
	if into == "" {
		into = DefaultImportSavedInto
	}
	into, err := fsmodel.NormalizeCanonicalPath(into)
	if err != nil {
		return nil, err
	}
	channelID, _, err := a.channelID(ctx)
	if err != nil {
		return nil, err
	}
	tgChID, err := a.tgChannelID(ctx)
	if err != nil {
		return nil, err
	}
	// Imported files carry ADR 0018 machine records, so the discussion group
	// must exist before anything is downloaded — including for a dry run,
	// whose plan would otherwise promise an import that cannot happen.
	manifestChat, err := a.discussionChatID(ctx, channelID)
	if err != nil {
		return nil, err
	}

	msgs, complete, err := a.collectSavedMessages(ctx, opts)
	if err != nil {
		return nil, err
	}
	out := &ImportSavedResult{
		DryRun:          opts.DryRun,
		Source:          manifest.SourceSaved,
		Into:            into,
		PhotosAs:        opts.PhotosAs,
		HistoryComplete: complete,
		Items:           []ImportSavedItem{},
	}
	units, items, err := a.planImportSaved(ctx, channelID, into, opts, msgs, out)
	if err != nil {
		return nil, err
	}
	if out.Photos > 0 && opts.PhotosAs == "" {
		choice, err := a.resolvePhotoChoice(opts, out.Photos)
		if err != nil {
			return nil, err
		}
		opts.PhotosAs = choice
		out.PhotosAs = choice
		applyPhotoChoice(units, choice)
	}
	if opts.DryRun {
		for _, item := range items {
			a.emitImportItem(opts, item)
		}
		finishImportResult(out, items)
		return out, nil
	}
	for _, item := range items {
		if item.Action != "import" {
			a.emitImportItem(opts, item)
		}
	}
	if err := a.executeImportSaved(ctx, channelID, tgChID, manifestChat, opts, units, out); err != nil {
		finishImportResult(out, items)
		return out, err
	}
	finishImportResult(out, items)
	return out, nil
}

// resolvePhotoChoice asks the caller how photos should be republished. There
// is deliberately no default: documents keep the bytes byte-for-byte, native
// photos are recompressed, and picking either silently loses something the
// user was never asked about.
func (a *App) resolvePhotoChoice(opts ImportSavedOptions, photos int) (string, error) {
	if opts.PhotoPrompt == nil {
		return "", apperr.New(apperr.ErrUsage,
			fmt.Sprintf("%d photo message(s) need an explicit presentation: pass --photos-as document (keeps the bytes) or --photos-as photo (native photo, recompressed by Telegram)", photos))
	}
	choice, err := opts.PhotoPrompt(photos)
	if err != nil {
		return "", err
	}
	switch choice {
	case PhotosAsDocument, PhotosAsPhoto:
		return choice, nil
	default:
		return "", apperr.New(apperr.ErrUsage,
			fmt.Sprintf("unknown photo presentation %q (want document or photo)", choice))
	}
}

// applyPhotoChoice rewrites the planned presentation of photo items once the
// choice is known, and re-splits units whose kind changed.
func applyPhotoChoice(units []*importUnit, choice string) {
	for _, u := range units {
		if u.kind != importKindPhoto {
			continue
		}
		for i := range u.pres {
			u.pres[i] = Presentation{Kind: choice}
		}
	}
}

// finishImportResult materializes the item list and the counters in plan
// order, so the JSON envelope reads the same for a dry run and a real run.
func finishImportResult(out *ImportSavedResult, items []*ImportSavedItem) {
	out.Items = out.Items[:0]
	out.Imported, out.Skipped, out.Failed, out.Duplicates = 0, 0, 0, 0
	out.CaptionsMerged, out.SourcesDeleted = 0, 0
	for _, it := range items {
		switch it.Action {
		case "import":
			out.Imported++
		case "skip":
			out.Skipped++
		case "fail":
			out.Failed++
		}
		if it.DuplicateOf != "" {
			out.Duplicates++
		}
		if it.CaptionMerged {
			out.CaptionsMerged++
		}
		if it.SourceDeleted {
			out.SourcesDeleted++
		}
		out.Items = append(out.Items, *it)
	}
}

// collectSavedMessages reads the source items: the named saved messages, or
// the whole saved chat walked oldest-first so albums and ids stay in order.
func (a *App) collectSavedMessages(ctx context.Context, opts ImportSavedOptions) ([]telegram.Message, bool, error) {
	if len(opts.MessageIDs) > 0 {
		var out []telegram.Message
		for _, id := range opts.MessageIDs {
			msg, err := a.TG.GetSavedMessage(ctx, id)
			if err != nil {
				return nil, false, telegram.MapError(err)
			}
			out = append(out, msg)
		}
		sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
		return out, true, nil
	}
	var out []telegram.Message
	meta, err := a.TG.StreamSavedHistory(ctx, 0, func(msg telegram.Message) error {
		msg.Data = nil
		out = append(out, msg)
		return nil
	})
	if err != nil {
		return nil, false, telegram.MapError(err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, meta.Complete, nil
}

// planImportSaved classifies every source message, resolves its destination,
// and groups the importable ones into publishable units. Nothing is
// downloaded or uploaded here.
func (a *App) planImportSaved(ctx context.Context, channelID int64, into string, opts ImportSavedOptions, msgs []telegram.Message, out *ImportSavedResult) ([]*importUnit, []*ImportSavedItem, error) {
	active, err := a.activePaths(ctx, channelID)
	if err != nil {
		return nil, nil, err
	}
	limit := a.uploadLimit(ctx)
	// used holds only the destinations this run already claimed. Existing
	// files are not in it on purpose: an occupied destination belongs to the
	// conflict policy (--replace / --skip-existing / --auto-rename), while
	// used only keeps two items of the same run from claiming one name.
	used := map[string]bool{}

	var items []*ImportSavedItem
	var units []*importUnit
	unitByKey := map[string]*importUnit{}
	for _, msg := range msgs {
		item := &ImportSavedItem{
			MessageID: msg.ID,
			GroupedID: msg.GroupedID,
			SubChat:   savedSubChatName(msg),
			MIME:      msg.MIME,
			Size:      msg.FileSize,
		}
		items = append(items, item)
		kind, reason := classifySavedMessage(msg)
		item.Kind = kind
		if reason != "" {
			item.Action = "skip"
			item.Reason = reason
			continue
		}
		if kind == importKindText {
			item.Size = int64(len(savedText(msg)))
		}
		if item.Size > limit {
			item.Action = "fail"
			item.Error = fmt.Sprintf("exceeds the %d byte upload limit", limit)
			if !opts.ContinueErr {
				return nil, nil, apperr.New(apperr.ErrFileTooLarge,
					fmt.Sprintf("saved message %d exceeds the %d byte upload limit (use --continue-on-error to skip it)", msg.ID, limit))
			}
			continue
		}
		dest, ok, conflict, err := a.planImportDest(into, msg, kind, opts.Policy, msg.GroupedID != 0, !opts.NoDedupe, active, used)
		if err != nil {
			item.Action = "fail"
			item.Error = err.Error()
			if !opts.ContinueErr {
				return nil, nil, err
			}
			continue
		}
		if !ok {
			item.Action = "skip"
			item.Reason = "destination exists"
			continue
		}
		item.Action = "import"
		item.Path = dest
		used[dest] = true
		active = append(active, fsmodel.ActivePath{Canonical: dest})
		if kind == importKindPhoto {
			out.Photos++
		}

		pres := importPresentation(msg, kind, opts.PhotosAs)
		// One unit per (album, presentation kind): a Telegram media group is
		// uniform, and one saved album can mix photos and videos.
		key := fmt.Sprintf("%d/%s", msg.GroupedID, pres.Kind)
		unit := unitByKey[key]
		if unit == nil || msg.GroupedID == 0 {
			unit = &importUnit{groupedID: msg.GroupedID, kind: kind, caption: savedCaption(msg)}
			units = append(units, unit)
			if msg.GroupedID != 0 {
				unitByKey[key] = unit
			}
		}
		if unit.caption == "" {
			unit.caption = savedCaption(msg)
		}
		unit.items = append(unit.items, item)
		unit.msgs = append(unit.msgs, msg)
		unit.pres = append(unit.pres, pres)
		unit.conflicts = append(unit.conflicts, conflict)
	}
	return units, items, nil
}

// planImportDest resolves one item's destination under the conflict policy.
// ok is false when the policy dropped the item. Album members cannot use
// --replace (a media group cannot supersede individual files), so a conflict
// there is a per-item failure with the remedies named.
//
// deferDupe makes an occupied destination a deferred conflict rather than an
// immediate failure: with dedupe on, the occupant is very often this same item
// from an earlier run, which the content hash resolves as a duplicate. The
// returned conflict error is raised only if the hash proves otherwise.
func (a *App) planImportDest(into string, msg telegram.Message, kind string, policy ConflictPolicy, albumMember, deferDupe bool, active []fsmodel.ActivePath, used map[string]bool) (dest string, ok bool, conflict, err error) {
	dir := into
	if sub := savedSubChatName(msg); sub != "" {
		if dir == "/" {
			dir = "/" + sub
		} else {
			dir = dir + "/" + sub
		}
	}
	if dir != "/" {
		dir += "/"
	}
	dest, err = fsmodel.NormalizeCanonicalPath(dir + savedItemName(msg, kind))
	if err != nil {
		return "", false, nil, err
	}
	if used[dest] {
		// Two saved items claiming one name (album members without file
		// names, repeated basenames): the later one steps aside rather than
		// overwriting a sibling from the same run.
		base := fsmodel.BaseName(dest)
		parent := fsmodel.ParentPath(dest)
		if parent != "/" {
			parent += "/"
		}
		found := false
		for i := 1; i <= 1000; i++ {
			candidate, cerr := fsmodel.NormalizeCanonicalPath(parent + fsmodel.ConflictRenameCandidate(base, i))
			if cerr != nil {
				continue
			}
			if !used[candidate] {
				dest = candidate
				found = true
				break
			}
		}
		if !found {
			return "", false, nil, apperr.New(apperr.ErrPathExists, fmt.Sprintf("no free name for %q", dest))
		}
	}
	activeExists := false
	for _, ap := range active {
		if !ap.IsDir && ap.Canonical == dest {
			activeExists = true
			break
		}
	}
	remedies := "use --replace, --skip-existing, or --auto-rename"
	if albumMember {
		remedies = "use --skip-existing or --auto-rename (album members cannot --replace)"
	}
	if activeExists && deferDupe && policy == ConflictFail {
		return dest, true, apperr.New(apperr.ErrPathExists,
			fmt.Sprintf("remote file %q already exists with different content (%s)", dest, remedies)), nil
	}
	resolved, keep, err := applyUploadPolicy(dest, policy, activeExists, !albumMember, active, remedies)
	if err != nil {
		return "", false, nil, err
	}
	if !keep {
		return "", false, nil, nil
	}
	if err := fsmodel.CheckUploadConflict(resolved, active); err != nil {
		return "", false, nil, err
	}
	return resolved, true, nil, nil
}

// classifySavedMessage maps a saved message to an import kind, or to the
// reason it cannot be imported.
func classifySavedMessage(msg telegram.Message) (kind string, skipReason string) {
	switch {
	case msg.Kind == telegram.KindPhoto:
		return importKindPhoto, ""
	case msg.Kind == telegram.KindDocument && msg.Video != nil:
		return importKindVideo, ""
	case msg.Kind == telegram.KindDocument:
		return importKindDocument, ""
	case msg.Kind == telegram.KindText, msg.Kind == telegram.KindNone && strings.TrimSpace(msg.Text) != "":
		return importKindText, ""
	default:
		return "unsupported", "unsupported or service message"
	}
}

// importPresentation carries the source message's own presentation over to
// the republished one. Video attributes ride on the message, so td never
// probes media files; photos follow the explicit --photos-as choice.
func importPresentation(msg telegram.Message, kind, photosAs string) Presentation {
	switch kind {
	case importKindVideo:
		p := Presentation{Kind: telegram.KindVideo}
		if msg.Video != nil {
			p.DurationSeconds = msg.Video.DurationSeconds
			p.Width = msg.Video.Width
			p.Height = msg.Video.Height
			p.SupportsStreaming = msg.Video.SupportsStreaming
		}
		return p
	case importKindPhoto:
		if photosAs == PhotosAsPhoto {
			return Presentation{Kind: telegram.KindPhoto}
		}
		return Presentation{Kind: telegram.KindDocument}
	default:
		return Presentation{Kind: telegram.KindDocument}
	}
}

// savedCaption is the human text of a saved message.
func savedCaption(msg telegram.Message) string {
	body := msg.Caption
	if body == "" {
		body = msg.Text
	}
	return strings.TrimSpace(manifest.SplitHumanAndMachine(body))
}

// savedText is the body of a saved text note.
func savedText(msg telegram.Message) string {
	if strings.TrimSpace(msg.Text) != "" {
		return msg.Text
	}
	return msg.Caption
}

// savedSubChatName is the directory a Saved Messages 2.0 sub-chat maps to.
// A sub-chat with an unresolvable title still gets a stable directory from
// its peer id, so its items never spill into the import root.
func savedSubChatName(msg telegram.Message) string {
	if msg.SavedPeerID == 0 {
		return ""
	}
	if name := sanitizeAdoptName(msg.SavedPeerTitle); name != "" {
		return name
	}
	return fmt.Sprintf("chat-%d", msg.SavedPeerID)
}

// savedItemName derives the file name of an imported item. Telegram gives
// photos and notes no file name, so the source message id keeps them unique
// and traceable.
func savedItemName(msg telegram.Message, kind string) string {
	if name := sanitizeAdoptName(msg.FileName); name != "" {
		return name
	}
	switch kind {
	case importKindPhoto:
		return fmt.Sprintf("photo-%d.jpg", msg.ID)
	case importKindVideo:
		return fmt.Sprintf("video-%d.mp4", msg.ID)
	case importKindText:
		return fmt.Sprintf("note-%d.txt", msg.ID)
	default:
		return fmt.Sprintf("file-%d%s", msg.ID, extForMIME(msg.MIME))
	}
}

// executeImportSaved runs the planned units: stage bytes, dedupe, publish,
// annotate, and optionally delete the sources.
func (a *App) executeImportSaved(ctx context.Context, channelID, tgChID int64, manifestChat string, opts ImportSavedOptions, units []*importUnit, out *ImportSavedResult) error {
	targets, err := a.activeFilesByHash(ctx, channelID)
	if err != nil {
		return err
	}
	staging := a.importStagingDir()
	for _, unit := range units {
		if err := a.importUnit(ctx, channelID, tgChID, manifestChat, staging, opts, unit, targets); err != nil {
			if !opts.ContinueErr {
				return err
			}
		}
	}
	return nil
}

// stagedItem is one downloaded saved item waiting to be published.
type stagedItem struct {
	item     *ImportSavedItem
	msg      telegram.Message
	pres     Presentation
	local    string
	size     int64
	hash     string
	conflict error
}

// importUnit stages, dedupes and publishes one unit. A per-item failure is
// recorded on the item and returned so the caller can honor
// --continue-on-error; nothing published is ever rolled back by a later
// sibling's failure.
func (a *App) importUnit(ctx context.Context, channelID, tgChID int64, manifestChat, staging string, opts ImportSavedOptions, unit *importUnit, targets map[string]importTarget) error {
	var staged []*stagedItem
	var firstErr error
	fail := func(item *ImportSavedItem, err error) {
		item.Action = "fail"
		item.Error = err.Error()
		a.emitImportItem(opts, item)
		if firstErr == nil {
			firstErr = err
		}
	}
	for i, msg := range unit.msgs {
		item := unit.items[i]
		local, size, hash, err := a.stageSavedItem(ctx, staging, msg, item)
		if err != nil {
			fail(item, err)
			continue
		}
		item.Size = size
		item.Hash = hash
		staged = append(staged, &stagedItem{
			item: item, msg: msg, pres: unit.pres[i], local: local,
			size: size, hash: hash, conflict: unit.conflicts[i],
		})
	}

	// Dedupe before uploading: matching bytes are already durable in the
	// tree, so only the source's caption still needs saving.
	var publish []*stagedItem
	for _, st := range staged {
		target, dup := targets[st.hash]
		if !opts.NoDedupe && dup {
			a.recordDuplicate(ctx, channelID, tgChID, opts, st, target)
			a.removeStaged(ctx, st.local)
			continue
		}
		if st.conflict != nil {
			// The occupant of the destination holds different content after
			// all, so the deferred path conflict is real.
			fail(st.item, st.conflict)
			a.removeStaged(ctx, st.local)
			continue
		}
		publish = append(publish, st)
	}

	if len(publish) > 0 {
		if err := a.publishImported(ctx, channelID, tgChID, manifestChat, opts, unit, publish, targets); err != nil {
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// publishImported uploads the surviving members of a unit and writes their
// provenance record. Album units go through the media-group path so grouping
// survives the import; singles go through the ordinary single-upload path,
// which is the only one that can honor --replace.
func (a *App) publishImported(ctx context.Context, channelID, tgChID int64, manifestChat string, opts ImportSavedOptions, unit *importUnit, publish []*stagedItem, targets map[string]importTarget) error {
	if unit.groupedID != 0 && len(publish) > 1 {
		return a.publishImportedAlbum(ctx, channelID, tgChID, manifestChat, opts, unit, publish, targets)
	}
	var firstErr error
	for _, st := range publish {
		caption := savedCaption(st.msg)
		if caption == "" {
			caption = unit.caption
		}
		data, err := a.uploadFileWithCaption(ctx, st.local, st.item.Path, opts.Policy, false, st.pres, caption)
		if err != nil {
			st.item.Action = "fail"
			st.item.Error = err.Error()
			a.emitImportItem(opts, st.item)
			a.removeStagedIfNoPending(ctx, channelID, st)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if skipped, _ := data["skipped"].(bool); skipped {
			st.item.Action = "skip"
			st.item.Reason = "destination exists"
			a.emitImportItem(opts, st.item)
			a.removeStaged(ctx, st.local)
			continue
		}
		msgID, _ := data["message_id"].(int)
		st.item.NewMessageID = msgID
		if hash, ok := data["hash"].(string); ok && hash != "" {
			st.item.Hash = hash
		}
		a.afterImported(ctx, tgChID, manifestChat, opts, []*stagedItem{st}, msgID, 0, targets)
		a.removeStaged(ctx, st.local)
	}
	return firstErr
}

// publishImportedAlbum republishes one saved album as a td album: one human
// caption on the first member, one td-album:v1 inventory, groups split at
// Telegram's member limit.
func (a *App) publishImportedAlbum(ctx context.Context, channelID, tgChID int64, manifestChat string, opts ImportSavedOptions, unit *importUnit, publish []*stagedItem, targets map[string]importTarget) error {
	sources := make([]albumSource, 0, len(publish))
	for i, st := range publish {
		src := albumSource{localPath: st.local, dest: st.item.Path, pres: &publish[i].pres}
		if i == 0 {
			src.humanCaption = unit.caption
		}
		sources = append(sources, src)
	}
	batch, failures, err := a.planAlbumBatch(ctx, sources, opts.Policy, false, publish[0].pres, opts.ContinueErr)
	if err != nil {
		for _, st := range publish {
			st.item.Action = "fail"
			st.item.Error = err.Error()
			a.emitImportItem(opts, st.item)
			a.removeStagedIfNoPending(ctx, channelID, st)
		}
		return err
	}
	if len(failures) > 0 {
		// planAlbumBatch reports lenient failures by source path; map them
		// back onto their items so the JSON stays per-item.
		for _, st := range publish {
			for _, f := range failures {
				if strings.HasPrefix(f, st.local+":") {
					st.item.Action = "fail"
					st.item.Error = strings.TrimSpace(strings.TrimPrefix(f, st.local+":"))
					a.emitImportItem(opts, st.item)
					a.removeStagedIfNoPending(ctx, channelID, st)
				}
			}
		}
	}
	if len(batch.members) == 0 {
		for _, st := range publish {
			a.removeStagedIfNoPending(ctx, channelID, st)
		}
		return nil
	}
	_, tgIDStr, err := a.channelID(ctx)
	if err != nil {
		return err
	}
	data, err := a.runAlbumBatch(ctx, batch, channelID, tgChID, tgIDStr)
	if err != nil {
		for _, st := range publish {
			if st.item.Action == "import" {
				st.item.Action = "fail"
				st.item.Error = err.Error()
				a.emitImportItem(opts, st.item)
			}
			a.removeStagedIfNoPending(ctx, channelID, st)
		}
		return err
	}
	byPath := map[string]int{}
	groups, _ := data["albums"].([]AlbumGroup)
	for _, g := range groups {
		for i, p := range g.Paths {
			if i < len(g.MessageIDs) {
				byPath[p] = g.MessageIDs[i]
			}
		}
	}
	firstMsgID := 0
	var published []*stagedItem
	for _, st := range publish {
		if st.item.Action != "import" {
			continue
		}
		st.item.NewMessageID = byPath[st.item.Path]
		if firstMsgID == 0 {
			firstMsgID = st.item.NewMessageID
		}
		published = append(published, st)
	}
	if len(published) > 0 {
		a.afterImported(ctx, tgChID, manifestChat, opts, published, firstMsgID, unit.groupedID, targets)
	}
	for _, st := range publish {
		a.removeStaged(ctx, st.local)
	}
	return nil
}

// afterImported writes the unit's td-origin:v1 record, registers the new
// hashes for in-run dedupe, and deletes the verified sources when asked.
func (a *App) afterImported(ctx context.Context, tgChID int64, manifestChat string, opts ImportSavedOptions, published []*stagedItem, firstMsgID int, groupedID int64, targets map[string]importTarget) {
	now := time.Now().UTC().Format(time.RFC3339)
	if firstMsgID > 0 {
		record := manifest.OriginMeta{
			Origin:    savedOrigin(published[0].msg, now),
			GroupedID: groupedID,
		}
		if groupedID == 0 {
			record.CanonicalPath = published[0].item.Path
		}
		if id, err := a.manifestCarrier(manifestChat).Send(ctx, tgChID, firstMsgID, manifest.RenderOriginRecord(record)); err == nil {
			for _, st := range published {
				st.item.OriginRecordID = id
			}
		} else {
			// Provenance is an annotation, never a precondition: a file that
			// is published and indexed stays imported even if its origin
			// record could not be written.
			for _, st := range published {
				st.item.Error = "origin record not written: " + err.Error()
			}
		}
	}
	for _, st := range published {
		if st.hash != "" {
			targets[st.hash] = importTarget{path: st.item.Path, messageID: st.item.NewMessageID, manifestChat: manifestChat}
		}
		a.deleteImportedSource(ctx, tgChID, opts, st.item, st.item.NewMessageID)
		a.emitImportItem(opts, st.item)
	}
}

// recordDuplicate handles a saved item whose bytes already live in the tree:
// the item is skipped, and its caption — the only part with no copy in the
// drive — is written into the matched file's thread as a td-dupe:v1 record,
// and merged into the file's caption when asked.
func (a *App) recordDuplicate(ctx context.Context, channelID, tgChID int64, opts ImportSavedOptions, st *stagedItem, target importTarget) {
	st.item.Action = "skip"
	st.item.DuplicateOf = target.path
	st.item.Reason = "duplicate of " + target.path
	err := a.withLocks(ctx, []string{sqlitestore.LockKey(channelID, target.path)}, func(ctx context.Context) error {
		a.recordDuplicateLocked(ctx, tgChID, opts, st, target)
		return nil
	})
	if err != nil {
		st.item.Action = "fail"
		st.item.Error = "duplicate record not written: " + err.Error()
	}
	a.emitImportItem(opts, st.item)
}

func (a *App) recordDuplicateLocked(ctx context.Context, tgChID int64, opts ImportSavedOptions, st *stagedItem, target importTarget) {
	st.item.Action = "skip"
	st.item.DuplicateOf = target.path
	st.item.Reason = "duplicate of " + target.path
	caption := savedCaption(st.msg)

	existing := ""
	if target.messageID > 0 {
		if msg, err := a.TG.GetMessage(ctx, tgChID, target.messageID); err == nil {
			existing = msg.Caption
			if existing == "" {
				existing = msg.Text
			}
		}
	}
	if caption == "" || duplicateCaptionAlreadyPreserved(existing, caption) {
		// Nothing to preserve: an empty caption, or a caption the tree
		// already carries (a re-run of an item this import published).
		a.deleteImportedSource(ctx, tgChID, opts, st.item, target.messageID)
		return
	}
	record := manifest.DupeMeta{
		Origin:        savedOrigin(st.msg, time.Now().UTC().Format(time.RFC3339)),
		CanonicalPath: target.path,
		Hash:          st.hash,
		Caption:       caption,
		SourceDate:    formatSavedDate(st.msg.Date),
	}
	body, err := manifest.RenderDupeRecordFitting(record, manifest.DefaultTextBudget, a.Cfg.Caption.MarginUTF16Units)
	if err != nil {
		st.item.Error = "duplicate record not written: " + err.Error()
	} else if target.messageID > 0 {
		if id, err := a.manifestCarrier(target.manifestChat).Send(ctx, tgChID, target.messageID, body); err == nil {
			st.item.DupeRecordID = id
		} else {
			st.item.Error = "duplicate record not written: " + err.Error()
		}
	}
	if opts.MergeCaptions {
		a.mergeDuplicateCaption(ctx, tgChID, target, existing, caption, st.item)
	}
	// The bytes are already in the tree and the caption is recorded, so the
	// source is safe to drop — but only if the record actually landed.
	if st.item.DupeRecordID > 0 || st.item.Error == "" {
		a.deleteImportedSource(ctx, tgChID, opts, st.item, target.messageID)
	}
}

func duplicateCaptionAlreadyPreserved(existing, caption string) bool {
	caption = strings.TrimSpace(caption)
	if caption == "" {
		return true
	}
	existing = strings.TrimSpace(existing)
	if existing == caption || strings.HasPrefix(existing, caption+"\n\n") {
		return true
	}
	for _, block := range strings.Split(existing, "\n"+manifest.MergeCaptionSeparator+"\n") {
		if strings.TrimSpace(block) == caption {
			return true
		}
	}
	return false
}

// mergeDuplicateCaption appends a duplicate's caption to the matched file's
// caption. Merge failures are reported per item and never rewrite a carrier:
// the td-dupe:v1 record already preserved the text, so a failed merge costs
// convenience, not content.
func (a *App) mergeDuplicateCaption(ctx context.Context, tgChID int64, target importTarget, existing, addition string, item *ImportSavedItem) {
	if target.messageID == 0 {
		item.Error = "caption not merged: the matched file has no message"
		return
	}
	if manifest.HasMachineMeta(existing) {
		item.Error = "caption not merged: the matched file still carries a legacy td:v1 caption record"
		return
	}
	merged, changed := manifest.MergeCaptions(existing, addition)
	if !changed {
		return
	}
	if !manifest.FitsTelegramCaption(merged, a.Cfg.Caption.SafeMediaCaptionUTF16Units, a.Cfg.Caption.MarginUTF16Units) {
		item.Error = "caption not merged: the merged caption exceeds the caption budget"
		return
	}
	if err := a.TG.EditCaption(ctx, tgChID, target.messageID, merged); err != nil {
		item.Error = "caption not merged: " + err.Error()
		return
	}
	item.CaptionMerged = true
}

// deleteImportedSource deletes a saved original once its content is provably
// in the drive: a published message that reads back, or the existing file a
// duplicate matched. Failed items are never deleted, so a failed run is
// always safe to retry.
func (a *App) deleteImportedSource(ctx context.Context, tgChID int64, opts ImportSavedOptions, item *ImportSavedItem, verifyMsgID int) {
	if !opts.DeleteSource || item.Action == "fail" {
		return
	}
	if verifyMsgID <= 0 {
		item.Error = "source kept: nothing to verify the import against"
		return
	}
	if _, err := a.TG.GetMessage(ctx, tgChID, verifyMsgID); err != nil {
		item.Error = "source kept: could not verify the drive message: " + err.Error()
		return
	}
	if err := a.TG.DeleteSavedMessage(ctx, item.MessageID); err != nil {
		item.Error = "source kept: " + err.Error()
		return
	}
	item.SourceDeleted = true
}

// savedOrigin snapshots one saved message's provenance.
func savedOrigin(msg telegram.Message, importedAt string) manifest.Origin {
	o := manifest.Origin{
		Source:      manifest.SourceSaved,
		SourceMsgID: msg.ID,
		ImportedAt:  importedAt,
	}
	if msg.Forward != nil {
		o.OriginID = msg.Forward.FromID
		o.OriginTitle = msg.Forward.Title
		o.OriginPostID = msg.Forward.PostID
		o.ForwardDate = formatSavedDate(msg.Forward.Date)
	}
	return o
}

func formatSavedDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// activeFilesByHash indexes the tree by content hash. Only hash-bearing
// active files can be recognized as duplicates; files adopted without --hash
// have no byte identity until td repair --hash backfills one.
func (a *App) activeFilesByHash(ctx context.Context, channelID int64) (map[string]importTarget, error) {
	rows, err := a.DB.Raw().QueryContext(ctx, `
		select content_hash, canonical_path, coalesce(message_id,0), coalesce(manifest_chat_tg_id,'')
		from files
		where channel_id=? and status='active' and content_hash is not null and content_hash!=''`, channelID)
	if err != nil {
		return nil, apperr.Wrap(apperr.ErrDB, "index files by hash", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]importTarget{}
	for rows.Next() {
		var hash, path, chat string
		var msgID int
		if err := rows.Scan(&hash, &path, &msgID, &chat); err != nil {
			return nil, apperr.Wrap(apperr.ErrDB, "index files by hash", err)
		}
		if _, seen := out[hash]; seen {
			continue
		}
		out[hash] = importTarget{path: path, messageID: msgID, manifestChat: chat}
	}
	if err := rows.Err(); err != nil {
		return nil, apperr.Wrap(apperr.ErrDB, "index files by hash", err)
	}
	return out, nil
}

// importStagingDir is where saved bytes land on their way into the drive.
// Imports move whole files, so the drive's own local root is preferred over
// the system temp directory, which is often small.
func (a *App) importStagingDir() string {
	if len(a.Cfg.Roots) > 0 && a.Cfg.Roots[0].LocalPath != "" {
		return filepath.Join(a.Cfg.Roots[0].LocalPath, ".td-import")
	}
	if a.Cfg.Storage.DBPath != "" {
		return filepath.Join(filepath.Dir(a.Cfg.Storage.DBPath), "import-staging")
	}
	return filepath.Join(os.TempDir(), "td-import")
}

// stageSavedItem downloads one saved item into the staging directory and
// hashes it on the way through, so the bytes are read once.
func (a *App) stageSavedItem(ctx context.Context, staging string, msg telegram.Message, item *ImportSavedItem) (string, int64, string, error) {
	if err := a.files().MkdirAll(ctx, staging, 0o700); err != nil {
		return "", 0, "", apperr.Wrap(apperr.ErrLocalNotFound, "create import staging directory", err)
	}
	target := filepath.Join(staging, fmt.Sprintf("%d-%s", msg.ID, fsmodel.BaseName(item.Path)))
	local, w, err := a.files().CreateTemp(ctx, target)
	if err != nil {
		return "", 0, "", apperr.Wrap(apperr.ErrLocalNotFound, "stage saved message", err)
	}
	h := blake3.New(32, nil)
	dl := &hashingWriter{w: w, h: h}
	var dlErr error
	if item.Kind == importKindText {
		_, dlErr = io.Copy(dl, strings.NewReader(savedText(msg)))
	} else {
		dlErr = a.TG.DownloadSavedMedia(ctx, msg.ID, dl)
	}
	closeErr := w.Close()
	if dlErr != nil {
		a.removeStaged(ctx, local)
		return "", 0, "", telegram.MapError(dlErr)
	}
	if closeErr != nil {
		a.removeStaged(ctx, local)
		return "", 0, "", apperr.Wrap(apperr.ErrLocalNotFound, "stage saved message", closeErr)
	}
	return local, dl.n, "blake3:" + hex.EncodeToString(h.Sum(nil)), nil
}

// hashingWriter tees staged bytes into the content hasher and counts them.
type hashingWriter struct {
	w io.Writer
	h *blake3.Hasher
	n int64
}

func (w *hashingWriter) Write(p []byte) (int, error) {
	n, err := w.w.Write(p)
	if n > 0 {
		w.n += int64(n)
		_, _ = w.h.Write(p[:n])
	}
	return n, err
}

// removeStaged drops a staged file best-effort. Staged bytes of a failed
// upload are kept on purpose: the pending index row points at them, so
// td repair --pending can finish the upload without downloading again.
func (a *App) removeStaged(ctx context.Context, path string) {
	if path == "" {
		return
	}
	_ = a.files().Remove(ctx, path)
}

// removeStagedIfNoPending removes bytes that cannot be resumed. Big-file
// upload failures keep a pending row whose original_local_path points at the
// staging file; small-file failures delete that row and must not leak the
// downloaded source in .td-import.
func (a *App) removeStagedIfNoPending(ctx context.Context, channelID int64, st *stagedItem) {
	var pending int
	err := a.DB.Raw().QueryRowContext(ctx, `
		select count(*) from files
		where channel_id=? and canonical_path=? and status='pending'
			and original_local_path=?`, channelID, st.item.Path, st.local).Scan(&pending)
	if err != nil || pending > 0 {
		return
	}
	a.removeStaged(ctx, st.local)
}

func (a *App) emitImportItem(opts ImportSavedOptions, item *ImportSavedItem) {
	if opts.Emit == nil {
		return
	}
	opts.Emit("import.item", *item)
}
