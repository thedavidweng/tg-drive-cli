package service

import (
	"fmt"

	apperr "github.com/thedavidweng/tg-drive-cli/core/errors"
	"github.com/thedavidweng/tg-drive-cli/core/telegram"
)

// Presentation describes how native Telegram clients render an upload.
// The zero value reproduces the plain document send exactly: no media-kind
// override, no attribute block, no thumbnail.
type Presentation struct {
	// Kind selects the message form: "" or "document" sends a plain file
	// card, "photo" a native photo message (Telegram recompresses the
	// bytes), "video" a document carrying a video attribute block (bytes
	// stay untouched).
	Kind string
	// DurationSeconds is the video length in seconds (Kind video only).
	DurationSeconds float64
	// Width and Height are the video pixel dimensions (Kind video only).
	// Callers supply the values; td never probes media files itself.
	Width  int
	Height int
	// SupportsStreaming marks the video as streamable (Kind video only).
	SupportsStreaming bool
	// ThumbPath names a local JPEG file attached as the preview thumbnail
	// so previews appear immediately. Photos take no thumbnail.
	ThumbPath string
}

// Validate rejects combinations the Telegram port cannot honor. Errors use
// the usage code so scripts get exit code 2.
func (p Presentation) Validate() error {
	switch p.Kind {
	case telegram.KindNone, telegram.KindDocument, telegram.KindPhoto, telegram.KindVideo:
	default:
		return apperr.New(apperr.ErrUsage,
			fmt.Sprintf("unknown media kind %q (want document, photo, or video)", p.Kind))
	}
	if p.DurationSeconds < 0 || p.Width < 0 || p.Height < 0 {
		return apperr.New(apperr.ErrUsage, "presentation attributes must not be negative")
	}
	if p.Kind != telegram.KindVideo && (p.DurationSeconds > 0 || p.Width > 0 || p.Height > 0 || p.SupportsStreaming) {
		return apperr.New(apperr.ErrUsage, "video attributes require media kind video")
	}
	if p.Kind == telegram.KindPhoto && p.ThumbPath != "" {
		return apperr.New(apperr.ErrUsage, "thumbnails do not apply to photo uploads")
	}
	return nil
}

// apply threads the presentation into a Telegram upload request. Video kind
// always carries an attribute block (possibly zero-valued), matching the
// caller's explicit request for video presentation.
func (p Presentation) apply(req *telegram.UploadRequest) {
	req.Kind = p.Kind
	if p.Kind == telegram.KindVideo {
		req.Video = &telegram.VideoAttributes{
			DurationSeconds:   p.DurationSeconds,
			Width:             p.Width,
			Height:            p.Height,
			SupportsStreaming: p.SupportsStreaming,
		}
	}
}
