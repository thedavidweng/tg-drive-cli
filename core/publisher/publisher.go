package publisher

import (
	"context"
	"errors"
	"fmt"
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
//
// It is a deep module: the public interface is small (Publish and Reindex),
// but the implementation hides slug-chain generation, UTF-16 caption budgets,
// manifest reply sending, and the index transaction for File rows, Nodes,
// Slug mappings, and Hashtag tags.
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
	// ManifestMsgID is the existing manifest reply message id, or 0 if none.
	ManifestMsgID int
	// OldMeta is the previous metadata, used to restore the manifest reply if
	// a caption edit fails. Leave nil if no rollback is needed.
	OldMeta *manifest.FileMeta

	// ReplaceFileID is an active file to mark as superseded.
	ReplaceFileID int64
	// SetUploadedAt sets uploaded_at on the file row.
	SetUploadedAt bool
	// Now is the RFC3339 timestamp to use; empty uses time.Now().
	Now string
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

// Publish renders the caption and manifest, sends or edits the manifest reply,
// optionally edits the media caption, and commits the file record.
func (p *Publisher) Publish(ctx context.Context, req PublishRequest) (*PublishResult, error) {
	if req.ExistingSlugs == nil {
		req.ExistingSlugs = map[string]string{}
	}
	p.fillMeta(&req.Meta)
	now := p.now(req.Now)

	tags, slugMaps, err := pathcodec.GenerateChain(req.Meta.CanonicalPath, req.ExistingSlugs)
	if err != nil {
		return nil, err
	}
	req.Meta.Tags = tags

	capRes, err := manifest.RenderCaption(req.Meta, p.cfg.SafeMediaCaptionUTF16Units, p.cfg.MarginUTF16Units)
	if err != nil {
		return nil, err
	}

	manifestMsgID := req.ManifestMsgID
	manifestChanged := false
	newManifest := false

	if capRes.NeedsManifestReply {
		if req.ManifestMsgID > 0 {
			manifestMsgID = req.ManifestMsgID
			manifestChanged = true
			if err := p.tg.EditText(ctx, req.ChannelID, manifestMsgID, capRes.ManifestReply); err != nil {
				if err := p.rollbackManifest(ctx, req, manifestMsgID, manifestChanged, newManifest); err != nil {
					return nil, err
				}
				return nil, mapTGErr(err)
			}
		} else {
			id, err := p.tg.SendTextReply(ctx, req.ChannelID, req.MessageID, capRes.ManifestReply)
			if err != nil {
				return nil, mapTGErr(err)
			}
			manifestMsgID = id
			manifestChanged = true
			newManifest = true
		}
	} else if req.ManifestMsgID > 0 {
		// Caption is self-contained; keep the manifest consistent with the full tag set.
		manifestMsgID = req.ManifestMsgID
		manifestChanged = true
		fullMeta := req.Meta
		fullMeta.Tags = tags
		if err := p.tg.EditText(ctx, req.ChannelID, manifestMsgID, manifest.RenderManifestReply(fullMeta)); err != nil {
			if err := p.rollbackManifest(ctx, req, manifestMsgID, manifestChanged, newManifest); err != nil {
				return nil, err
			}
			return nil, mapTGErr(err)
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
				return nil, mapTGErr(err)
			}
		}
	}

	fileID, err := p.fileIndex.Index(ctx, ports.FileIndexRequest{
		ChannelRowID:  req.ChannelRowID,
		FileID:        req.FileID,
		MessageID:     req.MessageID,
		ManifestMsgID: manifestMsgID,
		Meta:          req.Meta,
		SlugMaps:      slugMaps,
		Tags:          tags,
		ReplaceFileID: req.ReplaceFileID,
		SetUploadedAt: req.SetUploadedAt,
		Now:           now,
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
		ChannelRowID:  req.ChannelRowID,
		FileID:        req.FileID,
		MessageID:     req.MessageID,
		ManifestMsgID: manifestMsgID,
		Meta:          meta,
		SlugMaps:      slugMaps,
		Tags:          tags,
		SetUploadedAt: req.FileID == 0,
		Now:           now,
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

	if newManifest {
		// We sent a new manifest; delete it so it does not dangle.
		if err := p.tg.DeleteMessage(ctx, req.ChannelID, manifestMsgID); err != nil {
			if !isNotFound(err) {
				return mapTGErr(err)
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
	if err := p.tg.EditText(ctx, req.ChannelID, req.ManifestMsgID, manifest.RenderManifestReply(fullMeta)); err != nil {
		if !isNotEditable(err) && !isNotFound(err) {
			return mapTGErr(err)
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

func mapTGErr(err error) error {
	if err == nil {
		return nil
	}
	var fw *telegram.FloodWaitError
	if errors.As(err, &fw) {
		wait := time.Duration(fw.Seconds) * time.Second
		retryAt := time.Now().Add(wait)
		return apperr.New(apperr.ErrTelegramRateLimited,
			fmt.Sprintf("telegram rate limited this account: retry after %s (at %s)",
				wait, retryAt.Format("2006-01-02 15:04 MST"))).
			WithDetails(map[string]any{
				"retry_after_seconds": fw.Seconds,
				"retry_at":            retryAt.UTC().Format(time.RFC3339),
			})
	}

	var auth *telegram.AuthRequiredError
	var code *telegram.CodeInvalidError
	var pass *telegram.PasswordInvalidError
	var exp *telegram.CodeExpiredError
	var phone *telegram.PhoneInvalidError
	var large *telegram.FileTooLargeError
	var perm *telegram.PermissionDeniedError
	var notEditable *telegram.MessageNotEditableError
	var notFound *telegram.MessageNotFoundError

	switch {
	case errors.As(err, &auth):
		return apperr.New(apperr.ErrAuthRequired, "not logged in; run: td auth login")
	case errors.As(err, &code), errors.As(err, &pass), errors.As(err, &exp):
		return apperr.New(apperr.ErrAuthFailed, err.Error())
	case errors.As(err, &phone):
		return apperr.New(apperr.ErrConfigInvalid, err.Error())
	case errors.As(err, &large):
		return apperr.New(apperr.ErrFileTooLarge, err.Error())
	case errors.As(err, &perm):
		return apperr.New(apperr.ErrChannelPermission, err.Error())
	case errors.As(err, &notEditable):
		return apperr.New(apperr.ErrMessageNotEditable, err.Error())
	case errors.As(err, &notFound):
		return apperr.New(apperr.ErrRemoteNotFound, err.Error())
	}
	return apperr.Wrap(apperr.ErrTelegramRPC, "telegram", err)
}
