package service

import (
	"context"
	"encoding/hex"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	apperr "github.com/thedavidweng/tg-drive-cli/core/errors"
	"github.com/thedavidweng/tg-drive-cli/core/fsmodel"
	"github.com/thedavidweng/tg-drive-cli/core/manifest"
	"github.com/thedavidweng/tg-drive-cli/core/publisher"
	"github.com/thedavidweng/tg-drive-cli/core/telegram"
	"golang.org/x/text/unicode/norm"
	"lukechampine.com/blake3"
)

// AdoptOptions controls td adopt.
type AdoptOptions struct {
	MessageID       int
	Dest            string
	Into            string
	Unmanaged       bool
	NoHash          bool
	DryRun          bool
	ContinueErr     bool
	RewriteCaptions bool
}

// AdoptPlanItem is one message that would be adopted or restored.
type AdoptPlanItem struct {
	MessageID int    `json:"message_id"`
	Kind      string `json:"kind"`
	MIME      string `json:"mime,omitempty"`
	Size      int64  `json:"size,omitempty"`
	FileName  string `json:"file_name,omitempty"`
	Path      string `json:"path,omitempty"`
	Action    string `json:"action"`
	Reason    string `json:"reason,omitempty"`
	GroupedID int64  `json:"grouped_id,omitempty"`
	Caption   string `json:"caption,omitempty"`
}

// AdoptResult is the dry-run or execute summary.
type AdoptResult struct {
	DryRun  bool            `json:"dry_run"`
	Adopted int             `json:"adopted"`
	Skipped int             `json:"skipped"`
	Failed  int             `json:"failed"`
	Deleted int             `json:"deleted"`
	Items   []AdoptPlanItem `json:"items"`
}

func (a *App) Adopt(ctx context.Context, opts AdoptOptions) (*AdoptResult, error) {
	if opts.RewriteCaptions {
		return a.rewriteAdoptCaptions(ctx, opts)
	}
	if !opts.Unmanaged && opts.MessageID <= 0 {
		return nil, apperr.New(apperr.ErrUsage, "adopt requires a message id or --unmanaged")
	}
	channelID, _, err := a.channelID(ctx)
	if err != nil {
		return nil, err
	}
	// New machine records are comment threads (ADR 0018): adopt needs the
	// linked discussion group.
	manifestChat, err := a.discussionChatID(ctx, channelID)
	if err != nil {
		return nil, err
	}
	tgChID, err := a.tgChannelID(ctx)
	if err != nil {
		return nil, err
	}
	into := opts.Into
	if into == "" {
		into = "/"
	}
	into, err = fsmodel.NormalizeCanonicalPath(into)
	if err != nil {
		return nil, err
	}

	var msgs []telegram.Message
	if opts.MessageID > 0 {
		msg, err := a.TG.GetMessage(ctx, tgChID, opts.MessageID)
		if err != nil {
			return nil, telegram.MapError(err)
		}
		msgs = []telegram.Message{msg}
	} else {
		all, err := a.TG.History(ctx, tgChID, 0, 0)
		if err != nil {
			return nil, telegram.MapError(err)
		}
		msgs = all
	}

	indexed, err := a.indexedMessageIDs(ctx, channelID)
	if err != nil {
		return nil, err
	}
	active, err := a.activePaths(ctx, channelID)
	if err != nil {
		return nil, err
	}

	out := &AdoptResult{DryRun: opts.DryRun, Items: []AdoptPlanItem{}}
	used := map[string]bool{}
	for _, ap := range active {
		if !ap.IsDir {
			used[ap.Canonical] = true
		}
	}

	for _, msg := range msgs {
		item, skip := classifyAdopt(msg, opts.Dest, into, used)
		if skip {
			out.Skipped++
			out.Items = append(out.Items, item)
			continue
		}
		if indexed[msg.ID] {
			item.Action = "skip"
			item.Reason = "already indexed"
			out.Skipped++
			out.Items = append(out.Items, item)
			continue
		}
		if used[item.Path] {
			base := fsmodel.BaseName(item.Path)
			prefix := fsmodel.ParentPath(item.Path)
			if prefix != "/" {
				prefix += "/"
			}
			renamed := false
			for i := 1; i < 1000; i++ {
				candidate, err := fsmodel.NormalizeCanonicalPath(prefix + fsmodel.ConflictRenameCandidate(base, i))
				if err != nil {
					continue
				}
				if !used[candidate] {
					item.Path = candidate
					item.Reason = "auto-renamed to avoid collision"
					renamed = true
					break
				}
			}
			if !renamed {
				item.Action = "skip"
				item.Reason = "path collision"
				out.Skipped++
				out.Items = append(out.Items, item)
				continue
			}
		}
		if err := fsmodel.CheckUploadConflict(item.Path, active); err != nil {
			item.Action = "skip"
			item.Reason = err.Error()
			out.Skipped++
			out.Items = append(out.Items, item)
			continue
		}
		item.Action = "adopt"
		if opts.DryRun {
			out.Adopted++
			out.Items = append(out.Items, item)
			used[item.Path] = true
			active = append(active, fsmodel.ActivePath{Canonical: item.Path, IsDir: false})
			continue
		}
		if err := a.adoptOne(ctx, channelID, tgChID, msg, item.Path, opts); err != nil {
			item.Action = "fail"
			item.Reason = err.Error()
			out.Failed++
			out.Items = append(out.Items, item)
			if !opts.ContinueErr {
				return out, err
			}
			continue
		}
		out.Adopted++
		out.Items = append(out.Items, item)
		used[item.Path] = true
		active = append(active, fsmodel.ActivePath{Canonical: item.Path, IsDir: false})
		indexed[msg.ID] = true
	}
	if opts.DryRun {
		previewNewManifests(msgs, out)
		return out, nil
	}
	if err := a.ensureTelegramManifests(ctx, channelID, tgChID, manifestChat, msgs, opts, out); err != nil {
		return out, err
	}
	return out, nil
}

func (a *App) indexedMessageIDs(ctx context.Context, channelID int64) (map[int]bool, error) {
	rows, err := a.DB.Raw().QueryContext(ctx, `select message_id from files where channel_id=? and message_id is not null and status in ('active','pending','orphaned')`, channelID)
	if err != nil {
		return nil, apperr.Wrap(apperr.ErrDB, "list indexed messages", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[int]bool{}
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, nil
}

func classifyAdopt(msg telegram.Message, dest, into string, used map[string]bool) (AdoptPlanItem, bool) {
	item := AdoptPlanItem{
		MessageID: msg.ID,
		Kind:      adoptKind(msg),
		MIME:      msg.MIME,
		Size:      msg.FileSize,
		FileName:  msg.FileName,
	}
	body := msg.Caption
	if body == "" {
		body = msg.Text
	}
	if dest == "" && (msg.Kind == telegram.KindNone && strings.TrimSpace(body) == "" && msg.FileName == "" && msg.FileSize == 0) {
		item.Action = "skip"
		item.Reason = "empty or service message"
		return item, true
	}
	if strings.HasPrefix(strings.TrimSpace(body), "td-manifest:v1") || strings.HasPrefix(strings.TrimSpace(body), manifest.AlbumMagic) {
		item.Action = "skip"
		item.Reason = "manifest reply"
		return item, true
	}
	if manifest.HasMachineMeta(body) {
		item.Action = "skip"
		item.Reason = "already has td:v1 metadata"
		return item, true
	}
	if dest != "" {
		p, err := fsmodel.NormalizeCanonicalPath(dest)
		if err != nil {
			item.Action = "skip"
			item.Reason = err.Error()
			return item, true
		}
		item.Path = p
		return item, false
	}
	item.Path = proposeAdoptPath(msg, into)
	_ = used
	return item, false
}

func adoptKind(msg telegram.Message) string {
	if msg.Kind == telegram.KindPhoto || strings.HasPrefix(msg.MIME, "image/") {
		return "photo"
	}
	if strings.HasPrefix(msg.MIME, "video/") {
		return "video"
	}
	if strings.HasPrefix(msg.MIME, "audio/") {
		return "audio"
	}
	if msg.Kind == telegram.KindText || (msg.Kind == telegram.KindNone && msg.Text != "") {
		return "text"
	}
	if msg.Kind == telegram.KindDocument || msg.MIME != "" || msg.FileName != "" {
		return "file"
	}
	return "unknown"
}

func proposeAdoptPath(msg telegram.Message, into string) string {
	kind := adoptKind(msg)
	folder := "files"
	switch kind {
	case "video":
		folder = "videos"
	case "photo":
		folder = "photos"
	case "audio":
		folder = "audio"
	case "text":
		folder = "notes"
	}
	name := sanitizeAdoptName(msg.FileName)
	if name == "" && kind == "photo" {
		name = captionFileName(msg.Caption, msg.ID, ".jpg")
	}
	if name == "" && kind == "text" {
		name = noteName(msg.Text, msg.ID)
	}
	if name == "" {
		name = fmt.Sprintf("message-%d%s", msg.ID, extForMIME(msg.MIME))
	}
	base := into
	if base == "/" {
		base = "/" + folder
	} else {
		base = strings.TrimSuffix(base, "/") + "/" + folder
	}
	p, err := fsmodel.NormalizeCanonicalPath(base + "/" + name)
	if err != nil {
		p = "/" + folder + "/" + fmt.Sprintf("message-%d", msg.ID)
	}
	return p
}

func sanitizeAdoptName(name string) string {
	name = strings.TrimSpace(norm.NFC.String(name))
	name = strings.ReplaceAll(name, "\\", "_")
	name = strings.ReplaceAll(name, "/", "_")
	if name == "." || name == ".." || name == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range name {
		if unicode.IsControl(r) {
			continue
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

func captionFileName(caption string, id int, ext string) string {
	human := sanitizeAdoptName(manifest.SplitHumanAndMachine(caption))
	line := strings.TrimSpace(strings.Split(human, "\n")[0])
	if line == "" || strings.HasPrefix(strings.ToLower(line), "http://") || strings.HasPrefix(strings.ToLower(line), "https://") {
		return fmt.Sprintf("photo-%d%s", id, ext)
	}
	runes := []rune(line)
	if len(runes) > 48 {
		line = string(runes[:48])
	}
	line = strings.TrimRight(strings.TrimSpace(line), ".")
	if path.Ext(line) == "" {
		line += ext
	}
	return line
}

func noteName(text string, id int) string {
	human := manifest.SplitHumanAndMachine(text)
	line := strings.TrimSpace(strings.Split(human, "\n")[0])
	line = strings.TrimLeft(line, "#")
	line = sanitizeAdoptName(line)
	if line == "" || strings.HasPrefix(strings.ToLower(line), "http://") || strings.HasPrefix(strings.ToLower(line), "https://") {
		return fmt.Sprintf("note-%d.txt", id)
	}
	runes := []rune(line)
	if len(runes) > 48 {
		line = string(runes[:48])
	}
	line = strings.TrimSpace(line)
	if path.Ext(line) == "" {
		line += ".txt"
	}
	return line
}

func extForMIME(mime string) string {
	switch {
	case strings.HasPrefix(mime, "video/"):
		return ".mp4"
	case strings.HasPrefix(mime, "image/png"):
		return ".png"
	case strings.HasPrefix(mime, "image/"):
		return ".jpg"
	case strings.HasPrefix(mime, "audio/"):
		return ".ogg"
	case mime == "text/plain":
		return ".txt"
	default:
		return ""
	}
}

func (a *App) adoptOne(ctx context.Context, channelID, tgChID int64, msg telegram.Message, dest string, opts AdoptOptions) error {
	// Adopt only claims the message in the local index. Telegram captions
	// stay untouched so media albums keep their single human caption.
	display := fsmodel.BaseName(dest)
	size := msg.FileSize
	mimeType := msg.MIME
	if mimeType == "" && adoptKind(msg) == "text" {
		mimeType = "text/plain"
	}
	body := manifest.SplitHumanAndMachine(msg.Text)
	if adoptKind(msg) == "text" && size == 0 {
		size = int64(len([]byte(body)))
	}

	// --hash is opt-in because it downloads every adopted file's media; the
	// stored hash makes post-rebuild downloads verify content.
	contentHash := ""
	if !opts.NoHash {
		h := blake3.New(32, nil)
		if adoptKind(msg) == "text" {
			_, _ = h.Write([]byte(body))
		} else {
			if err := a.TG.DownloadMedia(ctx, tgChID, msg.ID, h); err != nil {
				return telegram.MapError(err)
			}
		}
		contentHash = "blake3:" + hex.EncodeToString(h.Sum(nil))
	}

	// The adoption writes the index under the destination's path lock, so it
	// cannot race a concurrent move or delete of the same path.
	return a.withLocks(ctx, lockKeysForPaths(channelID, dest), func(ctx context.Context) error {
		existingSlugs, err := a.loadExistingSlugs(ctx, channelID)
		if err != nil {
			return err
		}
		if _, err := a.publisher().Reindex(ctx, publisher.ReindexRequest{
			ChannelRowID: channelID,
			MessageID:    msg.ID,
			Meta: manifest.ParsedMeta{
				CanonicalPath: dest,
				DisplayName:   display,
				Size:          size,
				Hash:          contentHash,
				MIME:          mimeType,
			},
			ExistingSlugs: existingSlugs,
		}); err != nil {
			return err
		}
		return nil
	})
}

func (a *App) rewriteAdoptCaptions(ctx context.Context, opts AdoptOptions) (*AdoptResult, error) {
	channelID, _, err := a.channelID(ctx)
	if err != nil {
		return nil, err
	}
	// Conversion posts comment records (ADR 0018): the discussion group
	// must be linked.
	manifestChat, err := a.discussionChatID(ctx, channelID)
	if err != nil {
		return nil, err
	}
	tgChID, err := a.tgChannelID(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := a.DB.Raw().QueryContext(ctx, `
		select id, message_id, coalesce(manifest_message_id,0), canonical_path, display_name
		from files where channel_id=? and status='active' and message_id is not null`, channelID)
	if err != nil {
		return nil, apperr.Wrap(apperr.ErrDB, "list adopted files", err)
	}
	filesByMsg := map[int]fileRowLookup{}
	for rows.Next() {
		var r fileRowLookup
		if err := rows.Scan(&r.fileID, &r.msgID, &r.manID, &r.path, &r.name); err != nil {
			_ = rows.Close()
			return nil, err
		}
		filesByMsg[r.msgID] = r
	}
	_ = rows.Close()

	history, err := a.TG.History(ctx, tgChID, 0, 0)
	if err != nil {
		return nil, telegram.MapError(err)
	}

	out := &AdoptResult{DryRun: opts.DryRun, Items: []AdoptPlanItem{}}
	now := time.Now().UTC().Format(time.RFC3339)

	for _, msg := range history {
		if !isPerFileManifestReply(msg) {
			continue
		}
		item := AdoptPlanItem{MessageID: msg.ID, Kind: "reply", Action: "delete", Reason: "per-file td-manifest:v1 reply"}
		if opts.DryRun {
			out.Deleted++
			out.Items = append(out.Items, item)
			continue
		}
		if err := a.TG.DeleteMessage(ctx, tgChID, msg.ID); err != nil {
			item.Action = "fail"
			item.Reason = err.Error()
			out.Failed++
			out.Items = append(out.Items, item)
			if !opts.ContinueErr {
				return out, telegram.MapError(err)
			}
			continue
		}
		_, _ = a.DB.Raw().ExecContext(ctx, `update files set manifest_message_id=null, updated_at=? where channel_id=? and manifest_message_id=?`, now, channelID, msg.ID)
		out.Deleted++
		out.Items = append(out.Items, item)
	}

	var media []telegram.Message
	for _, msg := range history {
		if isPerFileManifestReply(msg) || manifest.IsAlbumReply(msg.Text) {
			continue
		}
		if msg.Kind == telegram.KindNone && strings.TrimSpace(msg.Caption+msg.Text) == "" && msg.FileName == "" {
			continue
		}
		media = append(media, msg)
	}

	albums, singles := groupAlbums(media)
	albumIDs := make([]int64, 0, len(albums))
	for id := range albums {
		albumIDs = append(albumIDs, id)
	}
	sort.Slice(albumIDs, func(i, j int) bool { return albumIDs[i] < albumIDs[j] })

	for _, gid := range albumIDs {
		members := albums[gid]
		if err := a.restoreAlbumCaptions(ctx, tgChID, members, filesByMsg, opts, out); err != nil {
			return out, err
		}
	}
	for _, msg := range singles {
		if err := a.restoreSingleCaption(ctx, tgChID, msg, filesByMsg, opts, out); err != nil {
			return out, err
		}
	}
	if !opts.DryRun {
		history, err = a.TG.History(ctx, tgChID, 0, 0)
		if err != nil {
			return out, telegram.MapError(err)
		}
	}
	if err := a.ensureTelegramManifests(ctx, channelID, tgChID, manifestChat, history, opts, out); err != nil {
		return out, err
	}
	return out, nil
}

func (a *App) restoreAlbumCaptions(ctx context.Context, tgChID int64, members []telegram.Message, files map[int]fileRowLookup, opts AdoptOptions, out *AdoptResult) error {
	sort.Slice(members, func(i, j int) bool { return members[i].ID < members[j].ID })
	want := pickAlbumCaption(members, files)
	for i, msg := range members {
		target := ""
		action := "clear"
		if i == 0 {
			target = want
			action = "keep"
		}
		current := messageBody(msg)
		if current == target {
			out.Skipped++
			out.Items = append(out.Items, AdoptPlanItem{
				MessageID: msg.ID, Kind: adoptKind(msg), FileName: msg.FileName,
				GroupedID: msg.GroupedID, Action: "skip", Reason: "album caption already correct",
				Caption: target, Path: files[msg.ID].path,
			})
			continue
		}
		item := AdoptPlanItem{
			MessageID: msg.ID, Kind: adoptKind(msg), FileName: msg.FileName,
			GroupedID: msg.GroupedID, Action: action, Caption: target,
			Path: files[msg.ID].path, Reason: albumActionReason(action, target),
		}
		if opts.DryRun {
			out.Adopted++
			out.Items = append(out.Items, item)
			continue
		}
		if err := a.TG.EditCaption(ctx, tgChID, msg.ID, target); err != nil {
			item.Action = "fail"
			item.Reason = err.Error()
			out.Failed++
			out.Items = append(out.Items, item)
			if !opts.ContinueErr {
				return telegram.MapError(err)
			}
			continue
		}
		out.Adopted++
		out.Items = append(out.Items, item)
	}
	return nil
}

func (a *App) restoreSingleCaption(ctx context.Context, tgChID int64, msg telegram.Message, files map[int]fileRowLookup, opts AdoptOptions, out *AdoptResult) error {
	body := messageBody(msg)
	if !manifest.HasMachineMeta(body) {
		out.Skipped++
		out.Items = append(out.Items, AdoptPlanItem{
			MessageID: msg.ID, Kind: adoptKind(msg), FileName: msg.FileName,
			Action: "skip", Reason: "ungrouped caption already human", Path: files[msg.ID].path,
		})
		return nil
	}
	display := msg.FileName
	parent := ""
	if f, ok := files[msg.ID]; ok {
		display = f.name
		parent = fsmodel.HumanParent(f.path)
	}
	visible := manifest.HumanVisibleCaption(body, display, parent)
	item := AdoptPlanItem{
		MessageID: msg.ID, Kind: adoptKind(msg), FileName: msg.FileName,
		Action: "keep", Caption: visible, Path: files[msg.ID].path,
		Reason: "strip td:v1 from ungrouped caption",
	}
	if opts.DryRun {
		out.Adopted++
		out.Items = append(out.Items, item)
		return nil
	}
	if err := a.TG.EditCaption(ctx, tgChID, msg.ID, visible); err != nil {
		item.Action = "fail"
		item.Reason = err.Error()
		out.Failed++
		out.Items = append(out.Items, item)
		if !opts.ContinueErr {
			return telegram.MapError(err)
		}
		return nil
	}
	out.Adopted++
	out.Items = append(out.Items, item)
	return nil
}

type fileRowLookup struct {
	fileID, msgID, manID int
	path, name           string
}

func groupAlbums(msgs []telegram.Message) (map[int64][]telegram.Message, []telegram.Message) {
	albums := map[int64][]telegram.Message{}
	var singles []telegram.Message
	for _, msg := range msgs {
		if msg.GroupedID != 0 {
			albums[msg.GroupedID] = append(albums[msg.GroupedID], msg)
			continue
		}
		singles = append(singles, msg)
	}
	return albums, singles
}

func pickAlbumCaption(members []telegram.Message, files map[int]fileRowLookup) string {
	best := ""
	for _, msg := range members {
		display := msg.FileName
		if f, ok := files[msg.ID]; ok && f.name != "" {
			display = f.name
		}
		cand := albumCaptionCandidate(messageBody(msg), msg.FileName, display)
		if betterAlbumCaption(cand, best) {
			best = cand
		}
	}
	return best
}

func albumCaptionCandidate(caption, fileName, display string) string {
	human := strings.TrimSpace(manifest.SplitHumanAndMachine(caption))
	if human == "" {
		return ""
	}
	if !isInventedCaption(human, fileName, display) {
		return human
	}
	// Adopt used to turn the album caption into a filename like
	// "foo #bar.jpg". Strip the extension and keep the human text.
	stripped := stripMediaExt(human)
	if stripped != human && strings.Contains(stripped, "#") {
		return stripped
	}
	return ""
}

func isInventedCaption(caption, fileName, display string) bool {
	c := strings.TrimSpace(caption)
	if c == "" {
		return true
	}
	for _, cand := range []string{fileName, display} {
		if cand != "" && c == cand {
			return true
		}
	}
	lower := strings.ToLower(c)
	if (strings.HasPrefix(lower, "photo-") || strings.HasPrefix(lower, "message-")) && looksLikeFileName(c) {
		return true
	}
	if strings.Contains(c, "#") || strings.Contains(c, "\n") || strings.ContainsAny(c, " \t") {
		return false
	}
	return looksLikeFileName(c)
}

func looksLikeFileName(s string) bool {
	switch strings.ToLower(path.Ext(s)) {
	case ".mp4", ".mov", ".mkv", ".webm", ".avi", ".m4v",
		".jpg", ".jpeg", ".png", ".gif", ".webp", ".heic",
		".mp3", ".ogg", ".wav", ".m4a", ".flac",
		".pdf", ".zip", ".rar", ".7z":
		return true
	default:
		return false
	}
}

func stripMediaExt(s string) string {
	if !looksLikeFileName(s) {
		return s
	}
	return strings.TrimSpace(strings.TrimSuffix(s, path.Ext(s)))
}

func betterAlbumCaption(candidate, best string) bool {
	if candidate == "" {
		return false
	}
	if best == "" {
		return true
	}
	if sc, sb := albumCaptionScore(candidate), albumCaptionScore(best); sc != sb {
		return sc > sb
	}
	return utf8.RuneCountInString(candidate) > utf8.RuneCountInString(best)
}

func albumCaptionScore(s string) int {
	n := 0
	if strings.Contains(s, "#") {
		n += 10
	}
	if strings.Contains(s, "\n") {
		n += 3
	}
	if strings.Contains(s, "http://") || strings.Contains(s, "https://") {
		n += 2
	}
	n += utf8.RuneCountInString(s) / 20
	return n
}

func messageBody(msg telegram.Message) string {
	if strings.TrimSpace(msg.Caption) != "" {
		return msg.Caption
	}
	return msg.Text
}

func previewNewManifests(msgs []telegram.Message, out *AdoptResult) {
	planned := map[int]bool{}
	for _, it := range out.Items {
		if it.Action == "adopt" {
			planned[it.MessageID] = true
		}
	}
	var media []telegram.Message
	for _, msg := range msgs {
		if planned[msg.ID] {
			media = append(media, msg)
		}
	}
	albums, singles := groupAlbums(media)
	gids := make([]int64, 0, len(albums))
	for gid := range albums {
		gids = append(gids, gid)
	}
	sort.Slice(gids, func(i, j int) bool { return gids[i] < gids[j] })
	for _, gid := range gids {
		members := albums[gid]
		out.Items = append(out.Items, AdoptPlanItem{
			MessageID: members[0].ID, Kind: "album", GroupedID: gid,
			Action: "album-manifest", Reason: fmt.Sprintf("one inventory reply for %d files", len(members)),
		})
		out.Adopted++
	}
	for _, msg := range singles {
		out.Items = append(out.Items, AdoptPlanItem{
			MessageID: msg.ID, Kind: adoptKind(msg),
			Action: "manifest", Reason: "one reconstructable reply for ungrouped file",
		})
		out.Adopted++
	}
}

func albumActionReason(action, caption string) string {
	if action == "keep" {
		if caption == "" {
			return "album has no recoverable human caption; first item stays empty"
		}
		return "restore album caption on first item"
	}
	return "clear per-item caption so the album stays one block"
}
