package publisher

import (
	"context"
	"errors"
	"time"

	apperr "github.com/thedavidweng/tg-drive-cli/core/errors"
	"github.com/thedavidweng/tg-drive-cli/core/fsmodel"
	"github.com/thedavidweng/tg-drive-cli/core/manifest"
	"github.com/thedavidweng/tg-drive-cli/core/pathcodec"
	"github.com/thedavidweng/tg-drive-cli/core/ports"
	"github.com/thedavidweng/tg-drive-cli/core/telegram"
)

// Config controls caption and manifest rendering.
type Config struct {
	SafeMediaCaptionUTF16Units int
	MarginUTF16Units           int
}

// Publisher publishes a file to the virtual file tree and keeps its Telegram
// message and the local index consistent.
type Publisher struct {
	tg        telegram.Client
	fileIndex ports.FileIndex
	cfg       Config
}

// New creates a publisher.
func New(tg telegram.Client, fileIndex ports.FileIndex, cfg Config) *Publisher {
	return &Publisher{tg: tg, fileIndex: fileIndex, cfg: cfg}
}

// PublishRequest is a full publication request.
type PublishRequest struct {
	// ChannelRowID is the local SQLite channel id.
	ChannelRowID int64
	// ChannelID is the Telegram channel id.
	ChannelID int64
	// FileID is the local SQLite files.id; 0 for a new row.
	FileID int64
	// MessageID is the Telegram media message id.
	MessageID int
	// Meta is the file metadata. Tags are generated inside the publisher.
	Meta manifest.FileMeta
	// ExistingSlugs is the current parent|segment -> slug map.
	ExistingSlugs map[string]string

	// EditCaption tells the publisher to edit the media caption on Telegram.
	EditCaption bool
	// IgnoreNotEditable tells the publisher to ignore MessageNotEditableError
	// when editing the caption. Useful for repair operations.
	IgnoreNotEditable bool
	// SkipManifestReply suppresses sending or editing a per-file manifest.
	// Album members use it: their reconstructable record is the one
	// td-album:v1 inventory of the group (ADR 0013), posted by the caller,
	// whose message id arrives as ManifestMsgID.
	SkipManifestReply bool
	// ManifestChatID selects the manifest carrier: empty for the legacy
	// in-channel reply, the linked discussion group's Telegram channel id
	// for the comment carrier (ADR 0018).
	ManifestChatID string
	// ManifestMsgID is the existing manifest message id — a reply or a
	// comment, per ManifestChatID — or 0 if none.
	ManifestMsgID int
	// OldMeta is the previous metadata, used to restore the manifest record
	// if a caption edit fails. Leave nil if no rollback is needed.
	OldMeta *manifest.FileMeta

	// ReplaceFileID is an active file to mark as superseded.
	ReplaceFileID int64
	// SetUploadedAt sets uploaded_at on the file row.
	SetUploadedAt bool
	// Now is the RFC3339 timestamp to use; empty uses time.Now().
	Now string

	// Rendered, when non-nil, is the caption the caller already rendered for
	// this publication (the upload path renders once and threads it here so
	// the sent caption and the indexed tags cannot diverge). Tags and
	// SlugMaps must be the outputs of the same rendering pass.
	Rendered *manifest.CaptionResult
	Tags     []string
	SlugMaps []pathcodec.SlugMapping
}

// PublishResult is the result of a publication.
type PublishResult struct {
	FileID        int64
	ManifestMsgID int
}

// ReindexRequest is a read-only re-indexing request for scan.
type ReindexRequest struct {
	// ChannelRowID is the local SQLite channel id.
	ChannelRowID int64
	// FileID is the local SQLite files.id; 0 for a new row.
	FileID int64
	// MessageID is the Telegram media message id.
	MessageID int
	// ManifestMsgID is the existing manifest reply message id, if any.
	ManifestMsgID *int
	// Meta is the parsed metadata from a caption or manifest.
	Meta manifest.ParsedMeta
	// ManifestChatID is the carrier peer of ManifestMsgID: empty for the
	// legacy in-channel reply, the discussion group id for comments
	// (ADR 0018).
	ManifestChatID string
	// ExistingSlugs is the current parent|segment -> slug map.
	ExistingSlugs map[string]string
	// Now is the RFC3339 timestamp to use; empty uses time.Now().
	Now string
}

// ReindexResult is the result of a re-index.
type ReindexResult struct {
	FileID        int64
	ManifestMsgID int
}

func (p *Publisher) now(now string) string {
	if now != "" {
		return now
	}
	return time.Now().UTC().Format(time.RFC3339)
}

// Publish renders the caption and manifest, sends or edits the manifest
// record (a comment thread reply for the ADR 0018 carrier, an in-channel
// reply for legacy rows), optionally edits the media caption, and commits
// the file record.
func (p *Publisher) Publish(ctx context.Context, req PublishRequest) (*PublishResult, error) {
	if req.ExistingSlugs == nil {
		req.ExistingSlugs = map[string]string{}
	}
	p.fillMeta(&req.Meta)
	now := p.now(req.Now)

	var tags []string
	var slugMaps []pathcodec.SlugMapping
	var capRes manifest.CaptionResult
	var legacyReply string
	var legacyNeedsReply bool
	if req.Rendered != nil {
		// The caller already rendered this caption once; reuse its outputs
		// verbatim instead of re-rendering.
		capRes = *req.Rendered
		tags = req.Tags
		slugMaps = req.SlugMaps
	} else {
		var err error
		tags, slugMaps, err = pathcodec.GenerateChain(req.Meta.CanonicalPath, req.ExistingSlugs)
		if err != nil {
			return nil, err
		}
		req.Meta.Tags = tags
		if req.ManifestChatID == "" {
			// Legacy rows keep their td:v1 caption line so the in-channel
			// record stays parseable; the comment carrier never puts
			// machine text on captions (ADR 0018).
			capRes.Caption, legacyReply, legacyNeedsReply, err = manifest.RenderLegacyCaption(req.Meta, p.cfg.SafeMediaCaptionUTF16Units, p.cfg.MarginUTF16Units)
		} else {
			var cerr error
			capRes, cerr = manifest.RenderCaption(req.Meta, p.cfg.SafeMediaCaptionUTF16Units, p.cfg.MarginUTF16Units)
			err = cerr
		}
		if err != nil {
			return nil, err
		}
	}

	manifestMsgID := req.ManifestMsgID
	manifestChanged := false
	newManifest := false

	fullMeta := req.Meta
	fullMeta.Tags = tags

	carrier := telegram.NewManifestCarrier(p.tg, req.ManifestChatID)
	switch {
	case req.SkipManifestReply:
		// The caller owns the group inventory; keep only the provided id.
	case carrier.Comment(), legacyNeedsReply:
		// Comment carrier (ADR 0018): the full manifest record always goes
		// to the post's comment thread. Legacy rows keep their td:v1
		// caption line so the in-channel record stays parseable. The two
		// carriers differ only in the record text; routing is the
		// carrier's job.
		text := legacyReply
		if carrier.Comment() {
			text = manifest.RenderManifestReplyFitting(fullMeta, manifest.DefaultTextBudget, p.cfg.MarginUTF16Units)
		}
		if req.ManifestMsgID > 0 {
			manifestMsgID = req.ManifestMsgID
			manifestChanged = true
			if err := carrier.Edit(ctx, req.ChannelID, manifestMsgID, text); err != nil {
				if err := p.rollbackManifest(ctx, req, manifestMsgID, manifestChanged, newManifest); err != nil {
					return nil, err
				}
				return nil, telegram.MapError(err)
			}
		} else {
			id, err := carrier.Send(ctx, req.ChannelID, req.MessageID, text)
			if err != nil {
				return nil, telegram.MapError(err)
			}
			manifestMsgID = id
			manifestChanged = true
			newManifest = true
		}
	case req.ManifestMsgID > 0:
		// Caption is self-contained; keep the manifest consistent with the full tag set.
		manifestMsgID = req.ManifestMsgID
		manifestChanged = true
		if err := carrier.Edit(ctx, req.ChannelID, manifestMsgID, manifest.RenderManifestReplyFitting(fullMeta, manifest.DefaultTextBudget, p.cfg.MarginUTF16Units)); err != nil {
			if err := p.rollbackManifest(ctx, req, manifestMsgID, manifestChanged, newManifest); err != nil {
				return nil, err
			}
			return nil, telegram.MapError(err)
		}
	}

	if req.EditCaption {
		if err := p.tg.EditCaption(ctx, req.ChannelID, req.MessageID, capRes.Caption); err != nil {
			if req.IgnoreNotEditable && isNotEditable(err) {
				// continue
			} else {
				if err := p.rollbackManifest(ctx, req, manifestMsgID, manifestChanged, newManifest); err != nil {
					return nil, err
				}
				return nil, telegram.MapError(err)
			}
		}
	}

	fileID, err := p.fileIndex.Index(ctx, ports.FileIndexRequest{
		ChannelRowID:   req.ChannelRowID,
		FileID:         req.FileID,
		MessageID:      req.MessageID,
		ManifestMsgID:  manifestMsgID,
		ManifestChatID: req.ManifestChatID,
		Meta:           req.Meta,
		SlugMaps:       slugMaps,
		Tags:           tags,
		ReplaceFileID:  req.ReplaceFileID,
		SetUploadedAt:  req.SetUploadedAt,
		Now:            now,
	})
	if err != nil {
		return nil, apperr.Wrap(apperr.ErrDB, "publish file record", err)
	}
	return &PublishResult{FileID: fileID, ManifestMsgID: manifestMsgID}, nil
}

// Reindex writes a scanned message into the index without sending anything to
// Telegram. It is the scan-only path of the publisher.
func (p *Publisher) Reindex(ctx context.Context, req ReindexRequest) (*ReindexResult, error) {
	if req.ExistingSlugs == nil {
		req.ExistingSlugs = map[string]string{}
	}
	now := p.now(req.Now)

	meta := manifest.FileMeta{
		CanonicalPath: req.Meta.CanonicalPath,
		DisplayName:   req.Meta.DisplayName,
		ParentHuman:   fsmodel.HumanParent(req.Meta.CanonicalPath),
		Size:          req.Meta.Size,
		Hash:          req.Meta.Hash,
		MIME:          req.Meta.MIME,
		Created:       req.Meta.Created,
	}
	if meta.DisplayName == "" {
		meta.DisplayName = fsmodel.BaseName(meta.CanonicalPath)
	}

	tags, slugMaps, err := pathcodec.GenerateChain(meta.CanonicalPath, req.ExistingSlugs)
	if err != nil {
		return nil, err
	}
	meta.Tags = tags

	manifestMsgID := 0
	if req.ManifestMsgID != nil {
		manifestMsgID = *req.ManifestMsgID
	}

	fileID, err := p.fileIndex.Index(ctx, ports.FileIndexRequest{
		ChannelRowID:   req.ChannelRowID,
		FileID:         req.FileID,
		MessageID:      req.MessageID,
		ManifestMsgID:  manifestMsgID,
		ManifestChatID: req.ManifestChatID,
		Meta:           meta,
		SlugMaps:       slugMaps,
		Tags:           tags,
		SetUploadedAt:  req.FileID == 0,
		Now:            now,
	})
	if err != nil {
		return nil, apperr.Wrap(apperr.ErrDB, "reindex file record", err)
	}
	return &ReindexResult{FileID: fileID, ManifestMsgID: manifestMsgID}, nil
}

func (p *Publisher) fillMeta(m *manifest.FileMeta) {
	if m.DisplayName == "" {
		m.DisplayName = fsmodel.BaseName(m.CanonicalPath)
	}
	if m.ParentHuman == "" {
		m.ParentHuman = fsmodel.HumanParent(m.CanonicalPath)
	}
}

func (p *Publisher) rollbackManifest(ctx context.Context, req PublishRequest, manifestMsgID int, manifestChanged, newManifest bool) error {
	if !manifestChanged || req.OldMeta == nil {
		return nil
	}

	oldMeta := *req.OldMeta
	oldTags, _, err := pathcodec.GenerateChain(oldMeta.CanonicalPath, req.ExistingSlugs)
	if err != nil {
		return err
	}
	oldMeta.Tags = oldTags
	p.fillMeta(&oldMeta)

	carrier := telegram.NewManifestCarrier(p.tg, req.ManifestChatID)
	if newManifest {
		// We sent a new manifest; delete it so it does not dangle.
		if err := carrier.Delete(ctx, req.ChannelID, manifestMsgID); err != nil {
			if !isNotFound(err) {
				return telegram.MapError(err)
			}
		}
		return nil
	}

	// We edited an existing manifest and want the old one back.
	if req.ManifestMsgID == 0 {
		return nil
	}
	fullMeta := oldMeta
	fullMeta.Tags = oldTags
	text := manifest.RenderManifestReplyFitting(fullMeta, manifest.DefaultTextBudget, p.cfg.MarginUTF16Units)
	if err := carrier.Edit(ctx, req.ChannelID, req.ManifestMsgID, text); err != nil {
		if !isNotEditable(err) && !isNotFound(err) {
			return telegram.MapError(err)
		}
	}
	return nil
}

func isNotEditable(err error) bool {
	var e *telegram.MessageNotEditableError
	return errors.As(err, &e)
}

func isNotFound(err error) bool {
	var e *telegram.MessageNotFoundError
	return errors.As(err, &e)
}
