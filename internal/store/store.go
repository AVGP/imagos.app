package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"imagos.app/internal/atproto"
	"imagos.app/internal/lexicon"
)

type Actor struct {
	DID      string `json:"did"`
	Handle   string `json:"handle"`
	PDSURL   string `json:"pdsUrl"`
	SyncedAt string `json:"syncedAt"`
}

type TagView struct {
	URI         string
	CID         string
	AuthorDID   string
	AuthorHandle string
	RKey        string
	Name        string
	Slug        string
	Description string
	CreatedAt   string
}

type PhotoView struct {
	URI          string
	CID          string
	AuthorDID    string
	AuthorHandle string
	RKey         string
	Title        string
	Description  string
	Alt          string
	BlobCID      string
	MimeType     string
	BlobSize     int64
	Width        int
	Height       int
	TagRefs      []lexicon.StrongRef
	Tags         []string
	BskyPostURI  string
	CreatedAt    string
}

type AlbumView struct {
	URI          string
	CID          string
	AuthorDID    string
	AuthorHandle string
	RKey         string
	Name         string
	Description  string
	PhotoRefs    []lexicon.StrongRef
	Photos       []PhotoView
	TagRefs      []lexicon.StrongRef
	Tags         []string
	BskyPostURI  string
	CreatedAt    string
}

type PostView struct {
	URI          string
	CID          string
	AuthorDID    string
	AuthorHandle string
	RKey         string
	Text         string
	PhotoRefs    []lexicon.StrongRef
	Photos       []PhotoView
	TagRefs      []lexicon.StrongRef
	Tags         []string
	BskyPostURI  string
	CreatedAt    string
}

type FollowView struct {
	URI           string
	CID           string
	FollowerDID   string
	SubjectDID    string
	SubjectHandle string
	RKey          string
	CreatedAt     string
}

type FeedItem struct {
	Kind      string // "post", "album", "photo"
	CreatedAt string
	Post      *PostView
	Album     *AlbumView
	Photo     *PhotoView
}

type TagSearchResults struct {
	Slug   string
	Tags   []TagView
	Photos []PhotoView
	Albums []AlbumView
	Posts  []PostView
}

type CachedBlob struct {
	CID       string
	AuthorDID string
	MimeType  string
	Data      []byte
	ThumbData []byte
	Width     int
	Height    int
}

type Store struct {
	db *sql.DB
}

func Open(dsn string) (*Store, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate() error {
	schema := `
	PRAGMA journal_mode = WAL;
	PRAGMA foreign_keys = ON;

	CREATE TABLE IF NOT EXISTS actors (
		did TEXT PRIMARY KEY,
		handle TEXT NOT NULL,
		pds_url TEXT NOT NULL,
		synced_at TEXT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_actors_handle ON actors(handle);

	CREATE TABLE IF NOT EXISTS oauth_pending (
		state TEXT PRIMARY KEY,
		payload_json TEXT NOT NULL,
		session_id TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL
	);

	CREATE TABLE IF NOT EXISTS oauth_sessions (
		session_id TEXT PRIMARY KEY,
		did TEXT NOT NULL,
		payload_json TEXT NOT NULL,
		expires_at INTEGER NOT NULL,
		created_at INTEGER NOT NULL
	);

	CREATE TABLE IF NOT EXISTS tags (
		uri TEXT PRIMARY KEY,
		cid TEXT NOT NULL,
		author_did TEXT NOT NULL,
		rkey TEXT NOT NULL,
		name TEXT NOT NULL,
		slug TEXT NOT NULL,
		description TEXT NOT NULL,
		created_at TEXT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_tags_author ON tags(author_did);
	CREATE INDEX IF NOT EXISTS idx_tags_slug ON tags(slug);

	CREATE TABLE IF NOT EXISTS photos (
		uri TEXT PRIMARY KEY,
		cid TEXT NOT NULL,
		author_did TEXT NOT NULL,
		rkey TEXT NOT NULL,
		title TEXT NOT NULL,
		description TEXT NOT NULL,
		alt TEXT NOT NULL,
		blob_cid TEXT NOT NULL,
		mime_type TEXT NOT NULL,
		blob_size INTEGER NOT NULL,
		width INTEGER NOT NULL,
		height INTEGER NOT NULL,
		tag_refs_json TEXT NOT NULL,
		tags_json TEXT NOT NULL,
		bsky_post_uri TEXT NOT NULL,
		created_at TEXT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_photos_author_created ON photos(author_did, created_at DESC);

	CREATE TABLE IF NOT EXISTS albums (
		uri TEXT PRIMARY KEY,
		cid TEXT NOT NULL,
		author_did TEXT NOT NULL,
		rkey TEXT NOT NULL,
		name TEXT NOT NULL,
		description TEXT NOT NULL,
		photo_refs_json TEXT NOT NULL,
		tag_refs_json TEXT NOT NULL,
		tags_json TEXT NOT NULL,
		bsky_post_uri TEXT NOT NULL,
		created_at TEXT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_albums_author_created ON albums(author_did, created_at DESC);

	CREATE TABLE IF NOT EXISTS posts (
		uri TEXT PRIMARY KEY,
		cid TEXT NOT NULL,
		author_did TEXT NOT NULL,
		rkey TEXT NOT NULL,
		text TEXT NOT NULL,
		photo_refs_json TEXT NOT NULL,
		tag_refs_json TEXT NOT NULL,
		tags_json TEXT NOT NULL,
		bsky_post_uri TEXT NOT NULL,
		created_at TEXT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_posts_author_created ON posts(author_did, created_at DESC);

	CREATE TABLE IF NOT EXISTS follows (
		uri TEXT PRIMARY KEY,
		cid TEXT NOT NULL,
		follower_did TEXT NOT NULL,
		subject_did TEXT NOT NULL,
		rkey TEXT NOT NULL,
		created_at TEXT NOT NULL
	);
	CREATE UNIQUE INDEX IF NOT EXISTS idx_follows_pair ON follows(follower_did, subject_did);

	CREATE TABLE IF NOT EXISTS blobs_cache (
		cid TEXT PRIMARY KEY,
		author_did TEXT NOT NULL,
		mime_type TEXT NOT NULL,
		data BLOB NOT NULL,
		thumb_data BLOB NOT NULL,
		width INTEGER NOT NULL,
		height INTEGER NOT NULL,
		created_at TEXT NOT NULL
	);
	`
	_, err := s.db.Exec(schema)
	return err
}

func (s *Store) UpsertActor(ctx context.Context, a Actor) error {
	if a.SyncedAt == "" {
		a.SyncedAt = time.Now().UTC().Format(time.RFC3339)
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO actors (did, handle, pds_url, synced_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(did) DO UPDATE SET
			handle = excluded.handle,
			pds_url = CASE WHEN excluded.pds_url != '' THEN excluded.pds_url ELSE actors.pds_url END,
			synced_at = excluded.synced_at
	`, a.DID, strings.ToLower(a.Handle), a.PDSURL, a.SyncedAt)
	return err
}

func (s *Store) GetActor(ctx context.Context, didOrHandle string) (*Actor, error) {
	q := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(didOrHandle), "@"))
	row := s.db.QueryRowContext(ctx, `
		SELECT did, handle, pds_url, synced_at FROM actors
		WHERE did = ? OR LOWER(handle) = LOWER(?)
		LIMIT 1
	`, q, q)
	var a Actor
	if err := row.Scan(&a.DID, &a.Handle, &a.PDSURL, &a.SyncedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &a, nil
}

func (s *Store) ListActors(ctx context.Context) ([]Actor, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT did, handle, pds_url, synced_at FROM actors ORDER BY handle ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Actor
	for rows.Next() {
		var a Actor
		if err := rows.Scan(&a.DID, &a.Handle, &a.PDSURL, &a.SyncedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

func (s *Store) SaveOAuthPending(ctx context.Context, req *atproto.OAuthPendingRequest) error {
	raw, err := json.Marshal(req)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO oauth_pending (state, payload_json, session_id, created_at)
		VALUES (?, ?, '', ?)
	`, req.State, string(raw), time.Now().Unix())
	return err
}

func (s *Store) GetOAuthPending(ctx context.Context, state string) (*atproto.OAuthPendingRequest, string, error) {
	var raw, sessionID string
	err := s.db.QueryRowContext(ctx, `SELECT payload_json, session_id FROM oauth_pending WHERE state = ?`, state).Scan(&raw, &sessionID)
	if err != nil {
		return nil, "", err
	}
	var req atproto.OAuthPendingRequest
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		return nil, "", err
	}
	return &req, sessionID, nil
}

func (s *Store) MarkOAuthPendingCompleted(ctx context.Context, state, sessionID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE oauth_pending SET session_id = ? WHERE state = ?`, sessionID, state)
	return err
}

func (s *Store) PopOAuthPending(ctx context.Context, state string) (*atproto.OAuthPendingRequest, error) {
	req, _, err := s.GetOAuthPending(ctx, state)
	if err != nil {
		return nil, err
	}
	_, _ = s.db.ExecContext(ctx, `DELETE FROM oauth_pending WHERE state = ?`, state)
	return req, nil
}

func (s *Store) SaveOAuthSession(ctx context.Context, sessionID string, sess *atproto.OAuthSession) error {
	raw, err := json.Marshal(sess)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO oauth_sessions (session_id, did, payload_json, expires_at, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, sessionID, sess.DID, string(raw), sess.ExpiresAt.Unix(), time.Now().Unix())
	return err
}

func (s *Store) GetOAuthSession(ctx context.Context, sessionID string) (*atproto.OAuthSession, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT payload_json FROM oauth_sessions WHERE session_id = ?`, sessionID).Scan(&raw)
	if err != nil {
		return nil, err
	}
	var sess atproto.OAuthSession
	if err := json.Unmarshal([]byte(raw), &sess); err != nil {
		return nil, err
	}
	return &sess, nil
}

func (s *Store) DeleteOAuthSession(ctx context.Context, sessionID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM oauth_sessions WHERE session_id = ?`, sessionID)
	return err
}

func (s *Store) UpsertTag(ctx context.Context, uri, cid, authorDID, rkey string, rec *lexicon.TagRecord) error {
	slug := rec.Slug
	if slug == "" {
		slug = lexicon.NormalizeTagSlug(rec.Name)
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO tags (uri, cid, author_did, rkey, name, slug, description, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(uri) DO UPDATE SET
			cid = excluded.cid,
			name = excluded.name,
			slug = excluded.slug,
			description = excluded.description
	`, uri, cid, authorDID, rkey, rec.Name, slug, rec.Description, rec.CreatedAt)
	return err
}

func (s *Store) ListTagsByAuthor(ctx context.Context, authorDID string) ([]TagView, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT t.uri, t.cid, t.author_did, COALESCE(a.handle, t.author_did), t.rkey, t.name, t.slug, t.description, t.created_at
		FROM tags t
		LEFT JOIN actors a ON a.did = t.author_did
		WHERE t.author_did = ?
		ORDER BY t.name ASC
	`, authorDID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TagView
	for rows.Next() {
		var tv TagView
		if err := rows.Scan(&tv.URI, &tv.CID, &tv.AuthorDID, &tv.AuthorHandle, &tv.RKey, &tv.Name, &tv.Slug, &tv.Description, &tv.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, tv)
	}
	return out, nil
}

func (s *Store) ListAllTags(ctx context.Context) ([]TagView, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT t.uri, t.cid, t.author_did, COALESCE(a.handle, t.author_did), t.rkey, t.name, t.slug, t.description, t.created_at
		FROM tags t
		LEFT JOIN actors a ON a.did = t.author_did
		ORDER BY t.name ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TagView
	for rows.Next() {
		var tv TagView
		if err := rows.Scan(&tv.URI, &tv.CID, &tv.AuthorDID, &tv.AuthorHandle, &tv.RKey, &tv.Name, &tv.Slug, &tv.Description, &tv.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, tv)
	}
	return out, nil
}

func (s *Store) GetTagByAuthorAndSlug(ctx context.Context, authorDID, slug string) (*TagView, error) {
	var tv TagView
	err := s.db.QueryRowContext(ctx, `
		SELECT t.uri, t.cid, t.author_did, COALESCE(a.handle, t.author_did), t.rkey, t.name, t.slug, t.description, t.created_at
		FROM tags t
		LEFT JOIN actors a ON a.did = t.author_did
		WHERE t.author_did = ? AND t.slug = ?
		LIMIT 1
	`, authorDID, slug).Scan(&tv.URI, &tv.CID, &tv.AuthorDID, &tv.AuthorHandle, &tv.RKey, &tv.Name, &tv.Slug, &tv.Description, &tv.CreatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &tv, nil
}

func normalizeSlugs(tags []string) []string {
	seen := make(map[string]bool)
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		slug := lexicon.NormalizeTagSlug(t)
		if slug != "" && !seen[slug] {
			seen[slug] = true
			out = append(out, slug)
		}
	}
	return out
}

func (s *Store) UpsertPhoto(ctx context.Context, uri, cid, authorDID, rkey string, rec *lexicon.PhotoRecord) error {
	w, h := 800, 600
	if rec.AspectRatio != nil && rec.AspectRatio.Width > 0 && rec.AspectRatio.Height > 0 {
		w, h = rec.AspectRatio.Width, rec.AspectRatio.Height
	}
	tagRefsJSON, _ := json.Marshal(rec.TagRefs)
	tagsJSON, _ := json.Marshal(normalizeSlugs(rec.Tags))
	bskyURI := ""
	if rec.BskyPostRef != nil {
		bskyURI = rec.BskyPostRef.URI
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO photos (uri, cid, author_did, rkey, title, description, alt, blob_cid, mime_type, blob_size, width, height, tag_refs_json, tags_json, bsky_post_uri, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(uri) DO UPDATE SET
			cid = excluded.cid,
			title = excluded.title,
			description = excluded.description,
			alt = excluded.alt,
			blob_cid = excluded.blob_cid,
			tag_refs_json = excluded.tag_refs_json,
			tags_json = excluded.tags_json,
			bsky_post_uri = CASE WHEN excluded.bsky_post_uri != '' THEN excluded.bsky_post_uri ELSE photos.bsky_post_uri END
	`, uri, cid, authorDID, rkey, rec.Title, rec.Description, rec.Alt, rec.Image.CID(), rec.Image.MimeType, rec.Image.Size, w, h, string(tagRefsJSON), string(tagsJSON), bskyURI, rec.CreatedAt)
	return err
}

func scanPhotoRow(scanner interface{ Scan(...any) error }) (PhotoView, error) {
	var pv PhotoView
	var tagRefsRaw, tagsRaw string
	err := scanner.Scan(
		&pv.URI, &pv.CID, &pv.AuthorDID, &pv.AuthorHandle, &pv.RKey,
		&pv.Title, &pv.Description, &pv.Alt, &pv.BlobCID, &pv.MimeType, &pv.BlobSize,
		&pv.Width, &pv.Height, &tagRefsRaw, &tagsRaw, &pv.BskyPostURI, &pv.CreatedAt,
	)
	if err != nil {
		return pv, err
	}
	_ = json.Unmarshal([]byte(tagRefsRaw), &pv.TagRefs)
	_ = json.Unmarshal([]byte(tagsRaw), &pv.Tags)
	return pv, nil
}

func (s *Store) GetPhotoByURI(ctx context.Context, uri string) (*PhotoView, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT p.uri, p.cid, p.author_did, COALESCE(a.handle, p.author_did), p.rkey,
		       p.title, p.description, p.alt, p.blob_cid, p.mime_type, p.blob_size,
		       p.width, p.height, p.tag_refs_json, p.tags_json, p.bsky_post_uri, p.created_at
		FROM photos p
		LEFT JOIN actors a ON a.did = p.author_did
		WHERE p.uri = ?
	`, uri)
	pv, err := scanPhotoRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &pv, nil
}

func (s *Store) GetPhotoByAuthorAndRKey(ctx context.Context, authorDID, rkey string) (*PhotoView, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT p.uri, p.cid, p.author_did, COALESCE(a.handle, p.author_did), p.rkey,
		       p.title, p.description, p.alt, p.blob_cid, p.mime_type, p.blob_size,
		       p.width, p.height, p.tag_refs_json, p.tags_json, p.bsky_post_uri, p.created_at
		FROM photos p
		LEFT JOIN actors a ON a.did = p.author_did
		WHERE p.author_did = ? AND p.rkey = ?
		LIMIT 1
	`, authorDID, rkey)
	pv, err := scanPhotoRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &pv, nil
}

func (s *Store) ListPhotosByAuthor(ctx context.Context, authorDID string, limit int) ([]PhotoView, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT p.uri, p.cid, p.author_did, COALESCE(a.handle, p.author_did), p.rkey,
		       p.title, p.description, p.alt, p.blob_cid, p.mime_type, p.blob_size,
		       p.width, p.height, p.tag_refs_json, p.tags_json, p.bsky_post_uri, p.created_at
		FROM photos p
		LEFT JOIN actors a ON a.did = p.author_did
		WHERE p.author_did = ?
		ORDER BY p.created_at DESC
		LIMIT ?
	`, authorDID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PhotoView
	for rows.Next() {
		pv, err := scanPhotoRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, pv)
	}
	return out, nil
}

func (s *Store) ListRecentPhotos(ctx context.Context, limit int) ([]PhotoView, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT p.uri, p.cid, p.author_did, COALESCE(a.handle, p.author_did), p.rkey,
		       p.title, p.description, p.alt, p.blob_cid, p.mime_type, p.blob_size,
		       p.width, p.height, p.tag_refs_json, p.tags_json, p.bsky_post_uri, p.created_at
		FROM photos p
		LEFT JOIN actors a ON a.did = p.author_did
		ORDER BY p.created_at DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PhotoView
	for rows.Next() {
		pv, err := scanPhotoRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, pv)
	}
	return out, nil
}

func (s *Store) hydratePhotoRefs(ctx context.Context, refs []lexicon.StrongRef) []PhotoView {
	out := make([]PhotoView, 0, len(refs))
	for _, ref := range refs {
		pv, err := s.GetPhotoByURI(ctx, ref.URI)
		if err == nil && pv != nil {
			out = append(out, *pv)
		}
	}
	return out
}

func (s *Store) UpsertAlbum(ctx context.Context, uri, cid, authorDID, rkey string, rec *lexicon.AlbumRecord) error {
	photoRefsJSON, _ := json.Marshal(rec.Photos)
	tagRefsJSON, _ := json.Marshal(rec.TagRefs)
	tagsJSON, _ := json.Marshal(normalizeSlugs(rec.Tags))
	bskyURI := ""
	if rec.BskyPostRef != nil {
		bskyURI = rec.BskyPostRef.URI
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO albums (uri, cid, author_did, rkey, name, description, photo_refs_json, tag_refs_json, tags_json, bsky_post_uri, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(uri) DO UPDATE SET
			cid = excluded.cid,
			name = excluded.name,
			description = excluded.description,
			photo_refs_json = excluded.photo_refs_json,
			tag_refs_json = excluded.tag_refs_json,
			tags_json = excluded.tags_json,
			bsky_post_uri = CASE WHEN excluded.bsky_post_uri != '' THEN excluded.bsky_post_uri ELSE albums.bsky_post_uri END
	`, uri, cid, authorDID, rkey, rec.Name, rec.Description, string(photoRefsJSON), string(tagRefsJSON), string(tagsJSON), bskyURI, rec.CreatedAt)
	return err
}

func (s *Store) scanAndHydrateAlbum(ctx context.Context, scanner interface{ Scan(...any) error }) (AlbumView, error) {
	var av AlbumView
	var photoRefsRaw, tagRefsRaw, tagsRaw string
	err := scanner.Scan(
		&av.URI, &av.CID, &av.AuthorDID, &av.AuthorHandle, &av.RKey,
		&av.Name, &av.Description, &photoRefsRaw, &tagRefsRaw, &tagsRaw, &av.BskyPostURI, &av.CreatedAt,
	)
	if err != nil {
		return av, err
	}
	_ = json.Unmarshal([]byte(photoRefsRaw), &av.PhotoRefs)
	_ = json.Unmarshal([]byte(tagRefsRaw), &av.TagRefs)
	_ = json.Unmarshal([]byte(tagsRaw), &av.Tags)
	av.Photos = s.hydratePhotoRefs(ctx, av.PhotoRefs)
	return av, nil
}

func (s *Store) GetAlbumByAuthorAndRKey(ctx context.Context, authorDID, rkey string) (*AlbumView, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT al.uri, al.cid, al.author_did, COALESCE(a.handle, al.author_did), al.rkey,
		       al.name, al.description, al.photo_refs_json, al.tag_refs_json, al.tags_json, al.bsky_post_uri, al.created_at
		FROM albums al
		LEFT JOIN actors a ON a.did = al.author_did
		WHERE al.author_did = ? AND al.rkey = ?
		LIMIT 1
	`, authorDID, rkey)
	av, err := s.scanAndHydrateAlbum(ctx, row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &av, nil
}

func (s *Store) ListAlbumsByAuthor(ctx context.Context, authorDID string, limit int) ([]AlbumView, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT al.uri, al.cid, al.author_did, COALESCE(a.handle, al.author_did), al.rkey,
		       al.name, al.description, al.photo_refs_json, al.tag_refs_json, al.tags_json, al.bsky_post_uri, al.created_at
		FROM albums al
		LEFT JOIN actors a ON a.did = al.author_did
		WHERE al.author_did = ?
		ORDER BY al.created_at DESC
		LIMIT ?
	`, authorDID, limit)
	if err != nil {
		return nil, err
	}
	var rawAlbums []AlbumView
	for rows.Next() {
		var av AlbumView
		var photoRefsRaw, tagRefsRaw, tagsRaw string
		if err := rows.Scan(
			&av.URI, &av.CID, &av.AuthorDID, &av.AuthorHandle, &av.RKey,
			&av.Name, &av.Description, &photoRefsRaw, &tagRefsRaw, &tagsRaw, &av.BskyPostURI, &av.CreatedAt,
		); err != nil {
			rows.Close()
			return nil, err
		}
		_ = json.Unmarshal([]byte(photoRefsRaw), &av.PhotoRefs)
		_ = json.Unmarshal([]byte(tagRefsRaw), &av.TagRefs)
		_ = json.Unmarshal([]byte(tagsRaw), &av.Tags)
		rawAlbums = append(rawAlbums, av)
	}
	rows.Close()
	for i := range rawAlbums {
		rawAlbums[i].Photos = s.hydratePhotoRefs(ctx, rawAlbums[i].PhotoRefs)
	}
	return rawAlbums, nil
}

func (s *Store) UpsertPost(ctx context.Context, uri, cid, authorDID, rkey string, rec *lexicon.PostRecord) error {
	photoRefsJSON, _ := json.Marshal(rec.Photos)
	tagRefsJSON, _ := json.Marshal(rec.TagRefs)
	tagsJSON, _ := json.Marshal(normalizeSlugs(rec.Tags))
	bskyURI := ""
	if rec.BskyPostRef != nil {
		bskyURI = rec.BskyPostRef.URI
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO posts (uri, cid, author_did, rkey, text, photo_refs_json, tag_refs_json, tags_json, bsky_post_uri, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(uri) DO UPDATE SET
			cid = excluded.cid,
			text = excluded.text,
			photo_refs_json = excluded.photo_refs_json,
			tag_refs_json = excluded.tag_refs_json,
			tags_json = excluded.tags_json,
			bsky_post_uri = CASE WHEN excluded.bsky_post_uri != '' THEN excluded.bsky_post_uri ELSE posts.bsky_post_uri END
	`, uri, cid, authorDID, rkey, rec.Text, string(photoRefsJSON), string(tagRefsJSON), string(tagsJSON), bskyURI, rec.CreatedAt)
	return err
}

func (s *Store) ListPostsByAuthor(ctx context.Context, authorDID string, limit int) ([]PostView, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT p.uri, p.cid, p.author_did, COALESCE(a.handle, p.author_did), p.rkey,
		       p.text, p.photo_refs_json, p.tag_refs_json, p.tags_json, p.bsky_post_uri, p.created_at
		FROM posts p
		LEFT JOIN actors a ON a.did = p.author_did
		WHERE p.author_did = ?
		ORDER BY p.created_at DESC
		LIMIT ?
	`, authorDID, limit)
	if err != nil {
		return nil, err
	}
	var out []PostView
	for rows.Next() {
		var pv PostView
		var photoRefsRaw, tagRefsRaw, tagsRaw string
		if err := rows.Scan(
			&pv.URI, &pv.CID, &pv.AuthorDID, &pv.AuthorHandle, &pv.RKey,
			&pv.Text, &photoRefsRaw, &tagRefsRaw, &tagsRaw, &pv.BskyPostURI, &pv.CreatedAt,
		); err != nil {
			rows.Close()
			return nil, err
		}
		_ = json.Unmarshal([]byte(photoRefsRaw), &pv.PhotoRefs)
		_ = json.Unmarshal([]byte(tagRefsRaw), &pv.TagRefs)
		_ = json.Unmarshal([]byte(tagsRaw), &pv.Tags)
		out = append(out, pv)
	}
	rows.Close()
	for i := range out {
		out[i].Photos = s.hydratePhotoRefs(ctx, out[i].PhotoRefs)
	}
	return out, nil
}

func (s *Store) UpsertFollow(ctx context.Context, uri, cid, followerDID, rkey string, rec *lexicon.FollowRecord) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO follows (uri, cid, follower_did, subject_did, rkey, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(follower_did, subject_did) DO UPDATE SET
			uri = excluded.uri,
			cid = excluded.cid,
			rkey = excluded.rkey,
			created_at = excluded.created_at
	`, uri, cid, followerDID, rec.Subject, rkey, rec.CreatedAt)
	return err
}

func (s *Store) GetFollow(ctx context.Context, followerDID, subjectDID string) (*FollowView, error) {
	var fv FollowView
	err := s.db.QueryRowContext(ctx, `
		SELECT f.uri, f.cid, f.follower_did, f.subject_did, COALESCE(a.handle, f.subject_did), f.rkey, f.created_at
		FROM follows f
		LEFT JOIN actors a ON a.did = f.subject_did
		WHERE f.follower_did = ? AND f.subject_did = ?
	`, followerDID, subjectDID).Scan(&fv.URI, &fv.CID, &fv.FollowerDID, &fv.SubjectDID, &fv.SubjectHandle, &fv.RKey, &fv.CreatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &fv, nil
}

func (s *Store) DeleteFollowBySubject(ctx context.Context, followerDID, subjectDID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM follows WHERE follower_did = ? AND subject_did = ?`, followerDID, subjectDID)
	return err
}

func (s *Store) ListFollows(ctx context.Context, followerDID string) ([]FollowView, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT f.uri, f.cid, f.follower_did, f.subject_did, COALESCE(a.handle, f.subject_did), f.rkey, f.created_at
		FROM follows f
		LEFT JOIN actors a ON a.did = f.subject_did
		WHERE f.follower_did = ?
		ORDER BY f.created_at DESC
	`, followerDID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FollowView
	for rows.Next() {
		var fv FollowView
		if err := rows.Scan(&fv.URI, &fv.CID, &fv.FollowerDID, &fv.SubjectDID, &fv.SubjectHandle, &fv.RKey, &fv.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, fv)
	}
	return out, nil
}

func (s *Store) CountFollowers(ctx context.Context, subjectDID string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM follows WHERE subject_did = ?`, subjectDID).Scan(&count)
	return count, err
}

// GetFollowingFeed returns a unified chronological feed of posts, albums, and standalone photos from accounts followed by viewerDID (plus viewerDID themselves).
func (s *Store) GetFollowingFeed(ctx context.Context, viewerDID string, limit int) ([]FeedItem, error) {
	if limit <= 0 {
		limit = 30
	}
	follows, err := s.ListFollows(ctx, viewerDID)
	if err != nil {
		return nil, err
	}
	dids := []string{viewerDID}
	for _, f := range follows {
		dids = append(dids, f.SubjectDID)
	}
	return s.getFeedForDIDs(ctx, dids, limit)
}

// GetExploreFeed returns a unified chronological feed across all known accounts.
func (s *Store) GetExploreFeed(ctx context.Context, limit int) ([]FeedItem, error) {
	if limit <= 0 {
		limit = 30
	}
	return s.getFeedForDIDs(ctx, nil, limit)
}

func (s *Store) getFeedForDIDs(ctx context.Context, dids []string, limit int) ([]FeedItem, error) {
	whereClause := ""
	args := []any{}
	if len(dids) > 0 {
		placeholders := make([]string, len(dids))
		for i, d := range dids {
			placeholders[i] = "?"
			args = append(args, d)
		}
		whereClause = fmt.Sprintf("WHERE author_did IN (%s)", strings.Join(placeholders, ","))
	}

	var items []FeedItem

	// 1. Fetch Posts
	postRows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT p.uri, p.cid, p.author_did, COALESCE(a.handle, p.author_did), p.rkey,
		       p.text, p.photo_refs_json, p.tag_refs_json, p.tags_json, p.bsky_post_uri, p.created_at
		FROM posts p
		LEFT JOIN actors a ON a.did = p.author_did
		%s
		ORDER BY p.created_at DESC
		LIMIT ?
	`, strings.ReplaceAll(whereClause, "author_did", "p.author_did")), append(args, limit)...)
	if err != nil {
		return nil, err
	}
	var posts []PostView
	for postRows.Next() {
		var pv PostView
		var photoRefsRaw, tagRefsRaw, tagsRaw string
		if err := postRows.Scan(
			&pv.URI, &pv.CID, &pv.AuthorDID, &pv.AuthorHandle, &pv.RKey,
			&pv.Text, &photoRefsRaw, &tagRefsRaw, &tagsRaw, &pv.BskyPostURI, &pv.CreatedAt,
		); err != nil {
			postRows.Close()
			return nil, err
		}
		_ = json.Unmarshal([]byte(photoRefsRaw), &pv.PhotoRefs)
		_ = json.Unmarshal([]byte(tagRefsRaw), &pv.TagRefs)
		_ = json.Unmarshal([]byte(tagsRaw), &pv.Tags)
		posts = append(posts, pv)
	}
	postRows.Close()
	for i := range posts {
		posts[i].Photos = s.hydratePhotoRefs(ctx, posts[i].PhotoRefs)
		if len(posts[i].Photos) == 0 {
			continue
		}
		pCopy := posts[i]
		items = append(items, FeedItem{
			Kind:      "post",
			CreatedAt: pCopy.CreatedAt,
			Post:      &pCopy,
		})
	}

	// 2. Fetch Albums
	albumRows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT al.uri, al.cid, al.author_did, COALESCE(a.handle, al.author_did), al.rkey,
		       al.name, al.description, al.photo_refs_json, al.tag_refs_json, al.tags_json, al.bsky_post_uri, al.created_at
		FROM albums al
		LEFT JOIN actors a ON a.did = al.author_did
		%s
		ORDER BY al.created_at DESC
		LIMIT ?
	`, strings.ReplaceAll(whereClause, "author_did", "al.author_did")), append(args, limit)...)
	if err != nil {
		return nil, err
	}
	var albums []AlbumView
	for albumRows.Next() {
		var av AlbumView
		var photoRefsRaw, tagRefsRaw, tagsRaw string
		if err := albumRows.Scan(
			&av.URI, &av.CID, &av.AuthorDID, &av.AuthorHandle, &av.RKey,
			&av.Name, &av.Description, &photoRefsRaw, &tagRefsRaw, &tagsRaw, &av.BskyPostURI, &av.CreatedAt,
		); err != nil {
			albumRows.Close()
			return nil, err
		}
		_ = json.Unmarshal([]byte(photoRefsRaw), &av.PhotoRefs)
		_ = json.Unmarshal([]byte(tagRefsRaw), &av.TagRefs)
		_ = json.Unmarshal([]byte(tagsRaw), &av.Tags)
		albums = append(albums, av)
	}
	albumRows.Close()
	for i := range albums {
		albums[i].Photos = s.hydratePhotoRefs(ctx, albums[i].PhotoRefs)
		if len(albums[i].Photos) == 0 {
			continue
		}
		aCopy := albums[i]
		items = append(items, FeedItem{
			Kind:      "album",
			CreatedAt: aCopy.CreatedAt,
			Album:     &aCopy,
		})
	}

	// 3. Fetch Photos (excluding synthetic app.bsky.embed.images helper rows)
	photoRows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT p.uri, p.cid, p.author_did, COALESCE(a.handle, p.author_did), p.rkey,
		       p.title, p.description, p.alt, p.blob_cid, p.mime_type, p.blob_size,
		       p.width, p.height, p.tag_refs_json, p.tags_json, p.bsky_post_uri, p.created_at
		FROM photos p
		LEFT JOIN actors a ON a.did = p.author_did
		%s
		ORDER BY p.created_at DESC
		LIMIT ?
	`, strings.ReplaceAll(whereClause, "author_did", "p.author_did")), append(args, limit)...)
	if err != nil {
		return nil, err
	}
	for photoRows.Next() {
		pv, err := scanPhotoRow(photoRows)
		if err != nil {
			photoRows.Close()
			return nil, err
		}
		if strings.Contains(pv.URI, "/app.bsky.embed.images/") {
			continue
		}
		pCopy := pv
		items = append(items, FeedItem{
			Kind:      "photo",
			CreatedAt: pCopy.CreatedAt,
			Photo:     &pCopy,
		})
	}
	photoRows.Close()

	sort.Slice(items, func(i, j int) bool {
		return items[i].CreatedAt > items[j].CreatedAt
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func (s *Store) GetItemsByTagSlug(ctx context.Context, slug string) (*TagSearchResults, error) {
	slug = lexicon.NormalizeTagSlug(slug)
	res := &TagSearchResults{Slug: slug}

	// Find matching tag records
	tagRows, err := s.db.QueryContext(ctx, `
		SELECT t.uri, t.cid, t.author_did, COALESCE(a.handle, t.author_did), t.rkey, t.name, t.slug, t.description, t.created_at
		FROM tags t
		LEFT JOIN actors a ON a.did = t.author_did
		WHERE t.slug = ?
	`, slug)
	if err != nil {
		return nil, err
	}
	tagURIs := make(map[string]bool)
	for tagRows.Next() {
		var tv TagView
		if err := tagRows.Scan(&tv.URI, &tv.CID, &tv.AuthorDID, &tv.AuthorHandle, &tv.RKey, &tv.Name, &tv.Slug, &tv.Description, &tv.CreatedAt); err != nil {
			tagRows.Close()
			return nil, err
		}
		res.Tags = append(res.Tags, tv)
		tagURIs[tv.URI] = true
	}
	tagRows.Close()

	matchesTag := func(inlineTags []string, refs []lexicon.StrongRef) bool {
		for _, t := range inlineTags {
			if lexicon.NormalizeTagSlug(t) == slug {
				return true
			}
		}
		for _, r := range refs {
			if tagURIs[r.URI] {
				return true
			}
		}
		return false
	}

	photos, err := s.ListRecentPhotos(ctx, 100)
	if err != nil {
		return nil, err
	}
	for _, p := range photos {
		if matchesTag(p.Tags, p.TagRefs) {
			res.Photos = append(res.Photos, p)
		}
	}

	exploreItems, err := s.GetExploreFeed(ctx, 100)
	if err != nil {
		return nil, err
	}
	for _, it := range exploreItems {
		if it.Kind == "album" && it.Album != nil {
			if matchesTag(it.Album.Tags, it.Album.TagRefs) {
				res.Albums = append(res.Albums, *it.Album)
			}
		}
		if it.Kind == "post" && it.Post != nil {
			if matchesTag(it.Post.Tags, it.Post.TagRefs) {
				res.Posts = append(res.Posts, *it.Post)
			}
		}
	}

	return res, nil
}

func (s *Store) SaveBlobCache(ctx context.Context, b CachedBlob) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO blobs_cache (cid, author_did, mime_type, data, thumb_data, width, height, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, b.CID, b.AuthorDID, b.MimeType, b.Data, b.ThumbData, b.Width, b.Height, time.Now().UTC().Format(time.RFC3339))
	return err
}

func (s *Store) GetBlobCache(ctx context.Context, cid string) (*CachedBlob, error) {
	var b CachedBlob
	err := s.db.QueryRowContext(ctx, `
		SELECT cid, author_did, mime_type, data, thumb_data, width, height
		FROM blobs_cache WHERE cid = ?
	`, cid).Scan(&b.CID, &b.AuthorDID, &b.MimeType, &b.Data, &b.ThumbData, &b.Width, &b.Height)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &b, nil
}
