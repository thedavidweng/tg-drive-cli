package sqlitestore

import (
	"context"
	"database/sql"

	"github.com/thedavidweng/tg-drive-cli/core/fsmodel"
	"github.com/thedavidweng/tg-drive-cli/core/ports"
)

var _ ports.FileIndex = (*DB)(nil)

// Index writes or updates a file record with its slug map, path tags, and
// derived directory nodes.
func (d *DB) Index(ctx context.Context, req ports.FileIndexRequest) (int64, error) {
	var fileID int64
	err := d.WithTx(ctx, func(tx *sql.Tx) error {
		var err error
		fileID, err = indexTx(ctx, tx, req)
		return err
	})
	return fileID, err
}

// IndexBatch applies several index requests in one transaction.
func (d *DB) IndexBatch(ctx context.Context, reqs []ports.FileIndexRequest) error {
	return d.WithTx(ctx, func(tx *sql.Tx) error {
		for _, req := range reqs {
			if _, err := indexTx(ctx, tx, req); err != nil {
				return err
			}
		}
		return nil
	})
}

func indexTx(ctx context.Context, tx *sql.Tx, req ports.FileIndexRequest) (int64, error) {
	if req.ReplaceFileID > 0 {
		if _, err := tx.ExecContext(ctx, `update files set status='superseded', node_id=null, updated_at=? where id=?`, req.Now, req.ReplaceFileID); err != nil {
			return 0, err
		}
	}

	mfID := sql.NullInt64{}
	if req.ManifestMsgID > 0 {
		mfID = sql.NullInt64{Int64: int64(req.ManifestMsgID), Valid: true}
	}
	mfChat := req.ManifestChatID

	var fileID int64
	if req.FileID > 0 {
		uploadedAtCase := 0
		if req.SetUploadedAt {
			uploadedAtCase = 1
		}
		_, err := tx.ExecContext(ctx, `
			update files set status='active', message_id=?, manifest_message_id=?, manifest_chat_tg_id=?, canonical_path=?, display_name=?, size=?, content_hash=?, mime=?, node_id=null, uploaded_at = case when ? then ? else uploaded_at end, updated_at=?
			where id=?`,
			req.MessageID, mfID, mfChat, req.Meta.CanonicalPath, req.Meta.DisplayName, req.Meta.Size, req.Meta.Hash, req.Meta.MIME, uploadedAtCase, req.Now, req.Now, req.FileID)
		if err != nil {
			return 0, err
		}
		fileID = req.FileID
	} else {
		var uploadedAt any
		if req.SetUploadedAt {
			uploadedAt = req.Now
		}
		res, err := tx.ExecContext(ctx, `
			insert into files(channel_id,message_id,manifest_message_id,manifest_chat_tg_id,canonical_path,display_name,size,content_hash,mime,status,uploaded_at,updated_at)
			values(?,?,?,?,?,?,?,?,?,'active',?,?)`,
			req.ChannelRowID, req.MessageID, mfID, mfChat, req.Meta.CanonicalPath, req.Meta.DisplayName, req.Meta.Size, req.Meta.Hash, req.Meta.MIME, uploadedAt, req.Now)
		if err != nil {
			return 0, err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return 0, err
		}
		fileID = id
	}

	if _, err := tx.ExecContext(ctx, `delete from path_tags where file_id=?`, fileID); err != nil {
		return 0, err
	}
	for i, tag := range req.Tags {
		if _, err := tx.ExecContext(ctx, `insert into path_tags(file_id,tag,depth) values(?,?,?)`, fileID, tag, i); err != nil {
			return 0, err
		}
	}
	for _, sm := range req.SlugMaps {
		if _, err := tx.ExecContext(ctx, `insert or ignore into path_segment_slugs(channel_id,parent_canonical_path,segment,slug,hash_len,created_at) values(?,?,?,?,?,?)`,
			req.ChannelRowID, sm.ParentCanonical, sm.Segment, sm.Slug, sm.HashLen, req.Now); err != nil {
			return 0, err
		}
	}
	for anc, name := range fsmodel.DeriveDirectoryNodes([]string{req.Meta.CanonicalPath}) {
		if _, err := tx.ExecContext(ctx, `insert or ignore into nodes(channel_id,canonical_path,parent_path,display_name,type,derived,created_at,updated_at) values(?,?,?,?,'dir',1,?,?)`,
			req.ChannelRowID, anc, fsmodel.ParentPath(anc), name, req.Now, req.Now); err != nil {
			return 0, err
		}
	}
	return fileID, nil
}
