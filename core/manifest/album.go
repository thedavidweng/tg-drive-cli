package manifest

import (
	"fmt"
	"strconv"
	"strings"

	apperr "github.com/thedavidweng/tg-drive-cli/core/errors"
)

const AlbumMagic = "td-album:v1"

// AlbumFile is one member of a Telegram media group.
type AlbumFile struct {
	MessageID     int
	CanonicalPath string
	DisplayName   string
	Size          int64
	Hash          string
	MIME          string
}

// AlbumMeta is the reconstructable inventory of one media group.
// Telegram already stores bytes, filename, size, MIME, and grouped_id;
// this reply binds each member to a virtual-tree path.
type AlbumMeta struct {
	GroupedID int64
	Files     []AlbumFile
}

// IsAlbumReply reports whether text is a td-album:v1 inventory.
func IsAlbumReply(text string) bool {
	return strings.HasPrefix(strings.TrimSpace(text), AlbumMagic)
}

// RenderAlbumReply renders a single reply that covers every member of a group.
func RenderAlbumReply(m AlbumMeta) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\ng=%d", AlbumMagic, m.GroupedID)
	for _, f := range m.Files {
		fmt.Fprintf(&b, "\n%d p=%s n=%s s=%d h=%s m=%s",
			f.MessageID, b64(f.CanonicalPath), b64(f.DisplayName), f.Size, dashOr(f.Hash), dashOr(f.MIME))
	}
	return b.String()
}

// RenderAlbumReplyFitting drops optional hashes if the inventory exceeds the
// text budget. Paths are never dropped — an album that cannot name its
// members is not reconstructable.
func RenderAlbumReplyFitting(m AlbumMeta, budget, margin int) (string, error) {
	if budget <= 0 {
		budget = DefaultTextBudget
	}
	if margin <= 0 {
		margin = DefaultMargin
	}
	reply := RenderAlbumReply(m)
	if FitsTelegramText(reply, budget, margin) {
		return reply, nil
	}
	stripped := m
	stripped.Files = append([]AlbumFile(nil), m.Files...)
	for i := range stripped.Files {
		stripped.Files[i].Hash = ""
	}
	reply = RenderAlbumReply(stripped)
	if FitsTelegramText(reply, budget, margin) {
		return reply, nil
	}
	return "", apperr.New(apperr.ErrCaptionTooLong, "album manifest exceeds Telegram text budget")
}

// ParseAlbumReply parses a td-album:v1 inventory.
func ParseAlbumReply(text string) (AlbumMeta, error) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, AlbumMagic) {
		return AlbumMeta{}, apperr.New(apperr.ErrManifestInvalid, "missing td-album:v1")
	}
	out := AlbumMeta{}
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == AlbumMagic {
			continue
		}
		if strings.HasPrefix(line, "g=") {
			id, err := strconv.ParseInt(strings.TrimPrefix(line, "g="), 10, 64)
			if err != nil {
				return AlbumMeta{}, apperr.New(apperr.ErrManifestInvalid, "invalid grouped id")
			}
			out.GroupedID = id
			continue
		}
		idStr, rest, ok := strings.Cut(line, " ")
		if !ok {
			return AlbumMeta{}, apperr.New(apperr.ErrManifestInvalid, fmt.Sprintf("invalid album file line %d", i+1))
		}
		msgID, err := strconv.Atoi(idStr)
		if err != nil || msgID <= 0 {
			return AlbumMeta{}, apperr.New(apperr.ErrManifestInvalid, "invalid album member id")
		}
		kvs := map[string]string{}
		for _, kv := range kvRe.FindAllStringSubmatch(rest, -1) {
			kvs[kv[1]] = kv[2]
		}
		p, okP := kvs["p"]
		n, okN := kvs["n"]
		if !okP || !okN {
			return AlbumMeta{}, apperr.New(apperr.ErrManifestInvalid, "album member missing p or n")
		}
		cp, err := decodeB64(p)
		if err != nil {
			return AlbumMeta{}, apperr.New(apperr.ErrManifestInvalid, "invalid album path")
		}
		dn, err := decodeB64(n)
		if err != nil {
			return AlbumMeta{}, apperr.New(apperr.ErrManifestInvalid, "invalid album name")
		}
		f := AlbumFile{MessageID: msgID, CanonicalPath: cp, DisplayName: dn}
		if v, ok := kvs["s"]; ok {
			_, _ = fmt.Sscanf(v, "%d", &f.Size)
		}
		if v, ok := kvs["h"]; ok && v != "-" {
			f.Hash = v
		}
		if v, ok := kvs["m"]; ok && v != "-" {
			f.MIME = v
		}
		out.Files = append(out.Files, f)
	}
	if out.GroupedID == 0 || len(out.Files) == 0 {
		return AlbumMeta{}, apperr.New(apperr.ErrManifestInvalid, "album manifest missing grouped id or files")
	}
	return out, nil
}

// AlbumFileFromMeta builds an inventory row from indexed metadata.
func AlbumFileFromMeta(messageID int, m FileMeta) AlbumFile {
	name := m.DisplayName
	if name == "" {
		name = m.CanonicalPath
	}
	return AlbumFile{
		MessageID:     messageID,
		CanonicalPath: m.CanonicalPath,
		DisplayName:   name,
		Size:          m.Size,
		Hash:          m.Hash,
		MIME:          m.MIME,
	}
}
