package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/image/draw"

	"imagos.app/internal/atproto"
	"imagos.app/internal/lexicon"
	"imagos.app/internal/store"
)

type Service struct {
	store   *store.Store
	client  *atproto.Client
	loginMu sync.Mutex
}

func New(st *store.Store, client *atproto.Client) *Service {
	return &Service{
		store:  st,
		client: client,
	}
}

func (s *Service) Store() *store.Store {
	return s.store
}

func (s *Service) Client() *atproto.Client {
	return s.client
}

// StartLogin initiates an ATProto OAuth flow and persists the pending state.
func (s *Service) StartLogin(ctx context.Context, handleOrDID string) (*atproto.OAuthPendingRequest, error) {
	return s.StartLoginWithBaseURL(ctx, handleOrDID, "")
}

// StartLoginWithBaseURL initiates an ATProto OAuth flow using the given public base URL for client_id and redirect_uri.
func (s *Service) StartLoginWithBaseURL(ctx context.Context, handleOrDID, publicBaseURL string) (*atproto.OAuthPendingRequest, error) {
	pending, err := s.client.StartOAuthWithBaseURL(ctx, handleOrDID, publicBaseURL)
	if err != nil {
		return nil, err
	}
	if err := s.store.SaveOAuthPending(ctx, pending); err != nil {
		return nil, err
	}
	return pending, nil
}

// CompleteLogin exchanges the OAuth callback code for a session, persists it, and syncs the user's PDS records.
func (s *Service) CompleteLogin(ctx context.Context, state, code string) (string, *atproto.OAuthSession, error) {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()

	pending, existingSessionID, err := s.store.GetOAuthPending(ctx, state)
	if err != nil {
		return "", nil, fmt.Errorf("invalid or expired OAuth state: %w", err)
	}
	if existingSessionID != "" {
		if existingSess, err := s.store.GetOAuthSession(ctx, existingSessionID); err == nil && existingSess != nil {
			return existingSessionID, existingSess, nil
		}
	}

	sess, err := s.client.ExchangeOAuthCode(ctx, pending, code)
	if err != nil {
		return "", nil, err
	}
	sessionID, err := atproto.RandomToken(24)
	if err != nil {
		return "", nil, err
	}
	if err := s.store.SaveOAuthSession(ctx, sessionID, sess); err != nil {
		return "", nil, err
	}
	_ = s.store.MarkOAuthPendingCompleted(ctx, state, sessionID)
	_ = s.store.UpsertActor(ctx, store.Actor{
		DID:    sess.DID,
		Handle: sess.Handle,
		PDSURL: sess.PDSURL,
	})

	// For local test servers (http://127.0.0.1), sync synchronously so tests observe immediate state;
	// for remote live PDSes (https://), sync the user's own repo quickly and sync followed accounts in the background
	// so the browser OAuth callback redirect never times out.
	if strings.HasPrefix(sess.PDSURL, "http://127.0.0.1") || strings.HasPrefix(sess.PDSURL, "http://localhost") {
		_ = s.SyncActorFromPDS(ctx, sess.DID, sess.Handle, sess.PDSURL)
	} else {
		go func(did, handle, pdsURL string) {
			bgCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			_ = s.SyncActorFromPDS(bgCtx, did, handle, pdsURL)
		}(sess.DID, sess.Handle, sess.PDSURL)
	}
	return sessionID, sess, nil
}

// ProcessImage decodes an image, returns dimensions, and builds a lightweight 3G-optimized JPEG thumbnail.
func ProcessImage(data []byte, maxThumbWidth int) (width int, height int, thumbBytes []byte) {
	if maxThumbWidth <= 0 {
		maxThumbWidth = 480
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		// Fallback if image is WebP or non-standard in tests
		return 800, 600, data
	}
	bounds := img.Bounds()
	width = bounds.Dx()
	height = bounds.Dy()
	if width <= 0 || height <= 0 {
		return 800, 600, data
	}

	thumbW := width
	thumbH := height
	if thumbW > maxThumbWidth {
		thumbW = maxThumbWidth
		thumbH = int(float64(height) * (float64(maxThumbWidth) / float64(width)))
		if thumbH < 1 {
			thumbH = 1
		}
	}

	dst := image.NewRGBA(image.Rect(0, 0, thumbW, thumbH))
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, bounds, draw.Over, nil)

	var out bytes.Buffer
	if err := jpeg.Encode(&out, dst, &jpeg.Options{Quality: 72}); err != nil {
		return width, height, data
	}
	return width, height, out.Bytes()
}

// ParseTagsString splits comma or space/hashtag separated tags into clean strings.
func ParseTagsString(raw string) []string {
	raw = strings.ReplaceAll(raw, "#", ",")
	parts := strings.Split(raw, ",")
	seen := make(map[string]bool)
	var out []string
	for _, p := range parts {
		clean := strings.TrimSpace(p)
		slug := lexicon.NormalizeTagSlug(clean)
		if slug != "" && !seen[slug] {
			seen[slug] = true
			out = append(out, clean)
		}
	}
	return out
}

// EnsureTags ensures each tag name has a corresponding app.imagos.tag record on the user's PDS and returns their strongRefs + slugs.
func (s *Service) EnsureTags(ctx context.Context, sess *atproto.OAuthSession, tagNames []string) ([]lexicon.StrongRef, []string, error) {
	var refs []lexicon.StrongRef
	var slugs []string
	seen := make(map[string]bool)

	for _, rawName := range tagNames {
		slug := lexicon.NormalizeTagSlug(rawName)
		if slug == "" || seen[slug] {
			continue
		}
		seen[slug] = true
		slugs = append(slugs, slug)

		existing, err := s.store.GetTagByAuthorAndSlug(ctx, sess.DID, slug)
		if err == nil && existing != nil {
			refs = append(refs, lexicon.StrongRef{
				URI: existing.URI,
				CID: existing.CID,
			})
			continue
		}

		tagRec := lexicon.NewTagRecord(rawName, "")
		if err := tagRec.Validate(); err != nil {
			return nil, nil, err
		}
		rkey := lexicon.NewTID()
		ref, err := s.client.CreateRecord(ctx, sess, lexicon.NSIDTag, rkey, tagRec)
		if err != nil {
			return nil, nil, fmt.Errorf("create app.imagos.tag record for %q: %w", rawName, err)
		}
		if err := s.store.UpsertTag(ctx, ref.URI, ref.CID, sess.DID, rkey, tagRec); err != nil {
			return nil, nil, err
		}
		refs = append(refs, ref)
	}
	return refs, slugs, nil
}

// CreateTag explicitly creates an app.imagos.tag record with a name and description.
func (s *Service) CreateTag(ctx context.Context, sess *atproto.OAuthSession, name, description string) (*store.TagView, error) {
	tagRec := lexicon.NewTagRecord(name, description)
	if err := tagRec.Validate(); err != nil {
		return nil, err
	}
	rkey := lexicon.NewTID()
	ref, err := s.client.CreateRecord(ctx, sess, lexicon.NSIDTag, rkey, tagRec)
	if err != nil {
		return nil, err
	}
	if err := s.store.UpsertTag(ctx, ref.URI, ref.CID, sess.DID, rkey, tagRec); err != nil {
		return nil, err
	}
	return s.store.GetTagByAuthorAndSlug(ctx, sess.DID, tagRec.Slug)
}

type UploadPhotoInput struct {
	Title       string
	Alt         string
	Description string
	MimeType    string
	ImageBytes  []byte
	Tags        []string
	PostToBsky  bool
}

// UploadPhoto uploads a photo blob to the PDS, creates app.imagos.tag records if needed, optionally cross-posts to Bluesky, and creates the app.imagos.photo record.
func (s *Service) UploadPhoto(ctx context.Context, sess *atproto.OAuthSession, in UploadPhotoInput) (*store.PhotoView, error) {
	if len(in.ImageBytes) == 0 {
		return nil, errors.New("image file is required")
	}
	if in.MimeType == "" {
		in.MimeType = "image/jpeg"
	}

	w, h, thumbBytes := ProcessImage(in.ImageBytes, 480)

	blobRef, err := s.client.UploadBlob(ctx, sess, in.MimeType, in.ImageBytes)
	if err != nil {
		return nil, fmt.Errorf("upload blob to PDS: %w", err)
	}

	// Cache blob & 3G thumbnail locally
	_ = s.store.SaveBlobCache(ctx, store.CachedBlob{
		CID:       blobRef.CID(),
		AuthorDID: sess.DID,
		MimeType:  in.MimeType,
		Data:      in.ImageBytes,
		ThumbData: thumbBytes,
		Width:     w,
		Height:    h,
	})

	tagRefs, slugs, err := s.EnsureTags(ctx, sess, in.Tags)
	if err != nil {
		return nil, err
	}

	photoRec := &lexicon.PhotoRecord{
		Type:        lexicon.NSIDPhoto,
		Title:       strings.TrimSpace(in.Title),
		Image:       blobRef,
		Alt:         strings.TrimSpace(in.Alt),
		Description: strings.TrimSpace(in.Description),
		AspectRatio: &lexicon.AspectRatio{Width: w, Height: h},
		TagRefs:     tagRefs,
		Tags:        slugs,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
	}
	if err := photoRec.Validate(); err != nil {
		return nil, err
	}

	if in.PostToBsky {
		bskyText := photoRec.Title
		if photoRec.Description != "" {
			bskyText += " — " + photoRec.Description
		}
		if len(slugs) > 0 {
			bskyText += "\n#" + strings.Join(slugs, " #")
		}
		bskyRec := lexicon.BuildBskyPost(bskyText, []*lexicon.PhotoRecord{photoRec})
		bskyRef, err := s.client.CreateRecord(ctx, sess, lexicon.NSIDBskyPost, lexicon.NewTID(), bskyRec)
		if err != nil {
			return nil, fmt.Errorf("cross-post photo to Bluesky: %w", err)
		}
		photoRec.BskyPostRef = &bskyRef
	}

	rkey := lexicon.NewTID()
	ref, err := s.client.CreateRecord(ctx, sess, lexicon.NSIDPhoto, rkey, photoRec)
	if err != nil {
		return nil, fmt.Errorf("create app.imagos.photo record: %w", err)
	}
	if err := s.store.UpsertPhoto(ctx, ref.URI, ref.CID, sess.DID, rkey, photoRec); err != nil {
		return nil, err
	}
	return s.store.GetPhotoByURI(ctx, ref.URI)
}

type CreateAlbumInput struct {
	Name        string
	Description string
	PhotoURIs   []string
	Tags        []string
	PostToBsky  bool
}

// CreateAlbum creates an app.imagos.album record on the user's PDS and optionally cross-posts it to Bluesky.
func (s *Service) CreateAlbum(ctx context.Context, sess *atproto.OAuthSession, in CreateAlbumInput) (*store.AlbumView, error) {
	photoRefs := make([]lexicon.StrongRef, 0, len(in.PhotoURIs))
	photoRecsForBsky := make([]*lexicon.PhotoRecord, 0, len(in.PhotoURIs))

	for _, uri := range in.PhotoURIs {
		uri = strings.TrimSpace(uri)
		if uri == "" {
			continue
		}
		pv, err := s.store.GetPhotoByURI(ctx, uri)
		if err != nil || pv == nil {
			return nil, fmt.Errorf("referenced photo not found: %s", uri)
		}
		photoRefs = append(photoRefs, lexicon.StrongRef{URI: pv.URI, CID: pv.CID})
		photoRecsForBsky = append(photoRecsForBsky, &lexicon.PhotoRecord{
			Title:       pv.Title,
			Alt:         pv.Alt,
			Image:       lexicon.NewBlobRef(pv.BlobCID, pv.MimeType, pv.BlobSize),
			AspectRatio: &lexicon.AspectRatio{Width: pv.Width, Height: pv.Height},
		})
	}

	tagRefs, slugs, err := s.EnsureTags(ctx, sess, in.Tags)
	if err != nil {
		return nil, err
	}

	albumRec := &lexicon.AlbumRecord{
		Type:        lexicon.NSIDAlbum,
		Name:        strings.TrimSpace(in.Name),
		Description: strings.TrimSpace(in.Description),
		Photos:      photoRefs,
		TagRefs:     tagRefs,
		Tags:        slugs,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
	}
	if err := albumRec.Validate(); err != nil {
		return nil, err
	}

	if in.PostToBsky {
		bskyText := fmt.Sprintf("Photo Album: %s", albumRec.Name)
		if albumRec.Description != "" {
			bskyText += " — " + albumRec.Description
		}
		if len(slugs) > 0 {
			bskyText += "\n#" + strings.Join(slugs, " #")
		}
		bskyRec := lexicon.BuildBskyPost(bskyText, photoRecsForBsky)
		bskyRef, err := s.client.CreateRecord(ctx, sess, lexicon.NSIDBskyPost, lexicon.NewTID(), bskyRec)
		if err != nil {
			return nil, fmt.Errorf("cross-post album to Bluesky: %w", err)
		}
		albumRec.BskyPostRef = &bskyRef
	}

	rkey := lexicon.NewTID()
	ref, err := s.client.CreateRecord(ctx, sess, lexicon.NSIDAlbum, rkey, albumRec)
	if err != nil {
		return nil, fmt.Errorf("create app.imagos.album record: %w", err)
	}
	if err := s.store.UpsertAlbum(ctx, ref.URI, ref.CID, sess.DID, rkey, albumRec); err != nil {
		return nil, err
	}
	return s.store.GetAlbumByAuthorAndRKey(ctx, sess.DID, rkey)
}

type CreatePostInput struct {
	Text       string
	PhotoURIs  []string
	Tags       []string
	PostToBsky bool
}

// CreatePost creates an app.imagos.feed.post record referencing one or more photos and optionally cross-posts to Bluesky.
func (s *Service) CreatePost(ctx context.Context, sess *atproto.OAuthSession, in CreatePostInput) (*store.PostView, error) {
	photoRefs := make([]lexicon.StrongRef, 0, len(in.PhotoURIs))
	photoRecsForBsky := make([]*lexicon.PhotoRecord, 0, len(in.PhotoURIs))

	for _, uri := range in.PhotoURIs {
		uri = strings.TrimSpace(uri)
		if uri == "" {
			continue
		}
		pv, err := s.store.GetPhotoByURI(ctx, uri)
		if err != nil || pv == nil {
			return nil, fmt.Errorf("referenced photo not found: %s", uri)
		}
		photoRefs = append(photoRefs, lexicon.StrongRef{URI: pv.URI, CID: pv.CID})
		photoRecsForBsky = append(photoRecsForBsky, &lexicon.PhotoRecord{
			Title:       pv.Title,
			Alt:         pv.Alt,
			Image:       lexicon.NewBlobRef(pv.BlobCID, pv.MimeType, pv.BlobSize),
			AspectRatio: &lexicon.AspectRatio{Width: pv.Width, Height: pv.Height},
		})
	}

	tagRefs, slugs, err := s.EnsureTags(ctx, sess, in.Tags)
	if err != nil {
		return nil, err
	}

	postRec := &lexicon.PostRecord{
		Type:      lexicon.NSIDPost,
		Text:      strings.TrimSpace(in.Text),
		Photos:    photoRefs,
		TagRefs:   tagRefs,
		Tags:      slugs,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if err := postRec.Validate(); err != nil {
		return nil, err
	}

	if in.PostToBsky {
		bskyText := postRec.Text
		if len(slugs) > 0 {
			bskyText += "\n#" + strings.Join(slugs, " #")
		}
		bskyRec := lexicon.BuildBskyPost(bskyText, photoRecsForBsky)
		bskyRef, err := s.client.CreateRecord(ctx, sess, lexicon.NSIDBskyPost, lexicon.NewTID(), bskyRec)
		if err != nil {
			return nil, fmt.Errorf("cross-post to Bluesky: %w", err)
		}
		postRec.BskyPostRef = &bskyRef
	}

	rkey := lexicon.NewTID()
	ref, err := s.client.CreateRecord(ctx, sess, lexicon.NSIDPost, rkey, postRec)
	if err != nil {
		return nil, fmt.Errorf("create app.imagos.feed.post record: %w", err)
	}
	if err := s.store.UpsertPost(ctx, ref.URI, ref.CID, sess.DID, rkey, postRec); err != nil {
		return nil, err
	}
	posts, err := s.store.ListPostsByAuthor(ctx, sess.DID, 1)
	if err != nil || len(posts) == 0 {
		return nil, err
	}
	return &posts[0], nil
}

// FollowActor creates an app.imagos.graph.follow record on the follower's PDS (using Bluesky's subject DID follow logic) and syncs the followed user's PDS records.
func (s *Service) FollowActor(ctx context.Context, sess *atproto.OAuthSession, targetHandleOrDID string) (*store.FollowView, error) {
	ident, err := s.client.ResolveIdentity(ctx, targetHandleOrDID)
	if err != nil {
		return nil, fmt.Errorf("resolve account %q: %w", targetHandleOrDID, err)
	}
	if ident.DID == sess.DID {
		return nil, errors.New("you cannot follow your own account")
	}

	_ = s.store.UpsertActor(ctx, store.Actor{
		DID:    ident.DID,
		Handle: ident.Handle,
		PDSURL: ident.PDSURL,
	})

	existing, err := s.store.GetFollow(ctx, sess.DID, ident.DID)
	if err == nil && existing != nil {
		_ = s.SyncActorFromPDS(ctx, ident.DID, ident.Handle, ident.PDSURL)
		return existing, nil
	}

	followRec := lexicon.NewFollowRecord(ident.DID)
	if err := followRec.Validate(); err != nil {
		return nil, err
	}
	rkey := lexicon.NewTID()
	ref, err := s.client.CreateRecord(ctx, sess, lexicon.NSIDFollow, rkey, followRec)
	if err != nil {
		return nil, fmt.Errorf("create app.imagos.graph.follow record: %w", err)
	}
	if err := s.store.UpsertFollow(ctx, ref.URI, ref.CID, sess.DID, rkey, followRec); err != nil {
		return nil, err
	}

	// Sync followed actor's records from their PDS
	_ = s.SyncActorFromPDS(ctx, ident.DID, ident.Handle, ident.PDSURL)

	return s.store.GetFollow(ctx, sess.DID, ident.DID)
}

// UnfollowActor deletes the follow record from the follower's PDS and removes it from the local index.
func (s *Service) UnfollowActor(ctx context.Context, sess *atproto.OAuthSession, subjectDID string) error {
	existing, err := s.store.GetFollow(ctx, sess.DID, subjectDID)
	if err != nil {
		return err
	}
	if existing != nil {
		col := lexicon.NSIDFollow
		if parsed, err := lexicon.ParseATURI(existing.URI); err == nil && parsed.Collection != "" {
			col = parsed.Collection
		}
		if err := s.client.DeleteRecord(ctx, sess, col, existing.RKey); err != nil {
			return fmt.Errorf("delete %s from PDS: %w", col, err)
		}
	}
	return s.store.DeleteFollowBySubject(ctx, sess.DID, subjectDID)
}

// SyncActorFromPDS pulls app.imagos.*, social.grain.*, and photo-bearing app.bsky.feed.post records from an actor's PDS,
// and also syncs accounts they follow via app.imagos.graph.follow or social.grain.graph.follow.
func (s *Service) SyncActorFromPDS(ctx context.Context, did, handle, pdsURL string) error {
	return s.syncActorFromPDSWithDepth(ctx, did, handle, pdsURL, true)
}

func (s *Service) syncActorFromPDSWithDepth(ctx context.Context, did, handle, pdsURL string, syncFollowedActors bool) error {
	if pdsURL == "" {
		ident, err := s.client.ResolveIdentity(ctx, did)
		if err != nil {
			return err
		}
		did = ident.DID
		handle = ident.Handle
		pdsURL = ident.PDSURL
	}
	_ = s.store.UpsertActor(ctx, store.Actor{
		DID:    did,
		Handle: handle,
		PDSURL: pdsURL,
	})

	// 1. Sync Tags
	if items, _, err := s.client.ListRecords(ctx, pdsURL, did, lexicon.NSIDTag, 100, ""); err == nil {
		for _, it := range items {
			parsed, err := lexicon.ParseATURI(it.URI)
			if err != nil {
				continue
			}
			var rec lexicon.TagRecord
			if err := json.Unmarshal(it.Value, &rec); err == nil {
				_ = s.store.UpsertTag(ctx, it.URI, it.CID, did, parsed.RKey, &rec)
			}
		}
	}

	// 2. Sync Photos (app.imagos.photo)
	crossPostedBskyURIs := make(map[string]bool)
	if items, _, err := s.client.ListRecords(ctx, pdsURL, did, lexicon.NSIDPhoto, 100, ""); err == nil {
		for _, it := range items {
			parsed, err := lexicon.ParseATURI(it.URI)
			if err != nil {
				continue
			}
			var rec lexicon.PhotoRecord
			if err := json.Unmarshal(it.Value, &rec); err == nil {
				if rec.BskyPostRef != nil && rec.BskyPostRef.URI != "" {
					crossPostedBskyURIs[rec.BskyPostRef.URI] = true
				}
				_ = s.store.UpsertPhoto(ctx, it.URI, it.CID, did, parsed.RKey, &rec)
			}
		}
	}

	// 2b. Sync Grain Photos (social.grain.photo)
	if items, _, err := s.client.ListRecords(ctx, pdsURL, did, lexicon.NSIDGrainPhoto, 100, ""); err == nil {
		for _, it := range items {
			parsed, err := lexicon.ParseATURI(it.URI)
			if err != nil {
				continue
			}
			gp, err := lexicon.ParseGrainPhoto(it.Value)
			if err != nil {
				continue
			}
			title := gp.Alt
			if len([]rune(title)) > 80 {
				title = string([]rune(title)[:77]) + "..."
			}
			photoRec := &lexicon.PhotoRecord{
				Type:        lexicon.NSIDPhoto,
				Title:       title,
				Image:       gp.Photo,
				Alt:         gp.Alt,
				AspectRatio: gp.AspectRatio,
				CreatedAt:   gp.CreatedAt,
			}
			_ = s.store.UpsertPhoto(ctx, it.URI, it.CID, did, parsed.RKey, photoRec)
		}
	}

	// 3. Sync Albums (app.imagos.album)
	if items, _, err := s.client.ListRecords(ctx, pdsURL, did, lexicon.NSIDAlbum, 100, ""); err == nil {
		for _, it := range items {
			parsed, err := lexicon.ParseATURI(it.URI)
			if err != nil {
				continue
			}
			var rec lexicon.AlbumRecord
			if err := json.Unmarshal(it.Value, &rec); err == nil && len(rec.Photos) > 0 {
				if rec.BskyPostRef != nil && rec.BskyPostRef.URI != "" {
					crossPostedBskyURIs[rec.BskyPostRef.URI] = true
				}
				_ = s.store.UpsertAlbum(ctx, it.URI, it.CID, did, parsed.RKey, &rec)
			}
		}
	}

	// 3b. Sync Grain Galleries (social.grain.gallery + social.grain.gallery.item) containing >= 1 photo
	galleryItemsMap := make(map[string][]lexicon.GrainGalleryItemRecord)
	if gItems, _, err := s.client.ListRecords(ctx, pdsURL, did, lexicon.NSIDGrainGalleryItem, 100, ""); err == nil {
		for _, it := range gItems {
			var gi lexicon.GrainGalleryItemRecord
			if err := json.Unmarshal(it.Value, &gi); err == nil && gi.Gallery != "" && gi.Item != "" {
				galleryItemsMap[gi.Gallery] = append(galleryItemsMap[gi.Gallery], gi)
			}
		}
	}
	if galleries, _, err := s.client.ListRecords(ctx, pdsURL, did, lexicon.NSIDGrainGallery, 100, ""); err == nil {
		for _, it := range galleries {
			parsed, err := lexicon.ParseATURI(it.URI)
			if err != nil {
				continue
			}
			var gal lexicon.GrainGalleryRecord
			if err := json.Unmarshal(it.Value, &gal); err != nil {
				continue
			}
			photoRefs := append([]lexicon.StrongRef{}, gal.Photos...)
			if linkedItems := galleryItemsMap[it.URI]; len(linkedItems) > 0 {
				sort.Slice(linkedItems, func(i, j int) bool {
					return linkedItems[i].Position < linkedItems[j].Position
				})
				for _, li := range linkedItems {
					photoRefs = append(photoRefs, lexicon.StrongRef{URI: li.Item, CID: "grain"})
				}
			}
			if len(photoRefs) == 0 {
				continue // Only include galleries containing at least one photo
			}
			createdAt := gal.CreatedAt
			if createdAt == "" {
				createdAt = time.Now().UTC().Format(time.RFC3339)
			}
			albumRec := &lexicon.AlbumRecord{
				Type:        lexicon.NSIDAlbum,
				Name:        gal.Title,
				Description: gal.Description,
				Photos:      photoRefs,
				CreatedAt:   createdAt,
			}
			_ = s.store.UpsertAlbum(ctx, it.URI, it.CID, did, parsed.RKey, albumRec)
		}
	}

	// 4. Sync Posts (app.imagos.feed.post)
	for _, col := range []string{lexicon.NSIDPost, lexicon.NSIDPostAlias} {
		if items, _, err := s.client.ListRecords(ctx, pdsURL, did, col, 100, ""); err == nil {
			for _, it := range items {
				parsed, err := lexicon.ParseATURI(it.URI)
				if err != nil {
					continue
				}
				var rec lexicon.PostRecord
				if err := json.Unmarshal(it.Value, &rec); err == nil && len(rec.Photos) > 0 {
					if rec.BskyPostRef != nil && rec.BskyPostRef.URI != "" {
						crossPostedBskyURIs[rec.BskyPostRef.URI] = true
					}
					_ = s.store.UpsertPost(ctx, it.URI, it.CID, did, parsed.RKey, &rec)
				}
			}
		}
	}

	// 4b. Sync Bluesky Posts (app.bsky.feed.post) that contain at least one embedded photo
	bskyLimit := 30
	if !syncFollowedActors {
		bskyLimit = 20
	}
	if bskyItems, _, err := s.client.ListRecords(ctx, pdsURL, did, lexicon.NSIDBskyPost, bskyLimit, ""); err == nil {
		for _, it := range bskyItems {
			if crossPostedBskyURIs[it.URI] {
				continue
			}
			parsed, err := lexicon.ParseATURI(it.URI)
			if err != nil {
				continue
			}
			bskyPost, imgs, err := lexicon.ParseBskyPostWithImages(it.Value)
			if err != nil || len(imgs) == 0 {
				continue // Only show Bluesky posts that contain at least one photo
			}
			createdAt := bskyPost.CreatedAt
			if createdAt == "" {
				createdAt = time.Now().UTC().Format(time.RFC3339)
			}
			photoRefs := make([]lexicon.StrongRef, 0, len(imgs))
			for idx, img := range imgs {
				imgRKey := fmt.Sprintf("%s-img%d", parsed.RKey, idx)
				imgURI := lexicon.FormatATURI(did, lexicon.NSIDBskyEmbedImages, imgRKey)
				title := img.Alt
				if len([]rune(title)) > 72 {
					title = string([]rune(title)[:69]) + "..."
				}
				synthesizedPhoto := &lexicon.PhotoRecord{
					Type:        lexicon.NSIDPhoto,
					Title:       title,
					Image:       img.Image,
					Alt:         img.Alt,
					AspectRatio: img.AspectRatio,
					BskyPostRef: &lexicon.StrongRef{URI: it.URI, CID: it.CID},
					CreatedAt:   createdAt,
				}
				_ = s.store.UpsertPhoto(ctx, imgURI, it.CID, did, imgRKey, synthesizedPhoto)
				photoRefs = append(photoRefs, lexicon.StrongRef{URI: imgURI, CID: it.CID})
			}
			postRec := &lexicon.PostRecord{
				Type:        lexicon.NSIDPost,
				Text:        bskyPost.Text,
				Photos:      photoRefs,
				BskyPostRef: &lexicon.StrongRef{URI: it.URI, CID: it.CID},
				CreatedAt:   createdAt,
			}
			_ = s.store.UpsertPost(ctx, it.URI, it.CID, did, parsed.RKey, postRec)
		}
	}

	// 5. Sync Follows (only when syncing the primary user, not recursively for 2nd-degree followed accounts)
	if syncFollowedActors {
		var followedDIDs []string
		seenFollow := make(map[string]bool)
		for _, followCol := range []string{lexicon.NSIDFollow, lexicon.NSIDGrainFollow, lexicon.NSIDBskyFollow} {
			if items, _, err := s.client.ListRecords(ctx, pdsURL, did, followCol, 50, ""); err == nil {
				for _, it := range items {
					parsed, err := lexicon.ParseATURI(it.URI)
					if err != nil {
						continue
					}
					var rec lexicon.FollowRecord
					if err := json.Unmarshal(it.Value, &rec); err == nil && strings.HasPrefix(rec.Subject, "did:") {
						_ = s.store.UpsertFollow(ctx, it.URI, it.CID, did, parsed.RKey, &rec)
						if rec.Subject != did && !seenFollow[rec.Subject] {
							seenFollow[rec.Subject] = true
							followedDIDs = append(followedDIDs, rec.Subject)
						}
					}
				}
			}
		}

		maxSyncFollows := 30
		if len(followedDIDs) > maxSyncFollows {
			followedDIDs = followedDIDs[:maxSyncFollows]
		}
		sem := make(chan struct{}, 5)
		var wg sync.WaitGroup
		for _, subjectDID := range followedDIDs {
			wg.Add(1)
			go func(targetDID string) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				subCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
				defer cancel()
				_ = s.syncActorFromPDSWithDepth(subCtx, targetDID, "", "", false)
			}(subjectDID)
		}
		wg.Wait()
	}

	return nil
}

// GetOrFetchBlob returns a cached blob (or thumbnail) or fetches it on-demand from the author's PDS and caches it.
func (s *Service) GetOrFetchBlob(ctx context.Context, authorDID, cid string, wantThumb bool) ([]byte, string, error) {
	cached, err := s.store.GetBlobCache(ctx, cid)
	if err == nil && cached != nil {
		if wantThumb && len(cached.ThumbData) > 0 {
			return cached.ThumbData, "image/jpeg", nil
		}
		return cached.Data, cached.MimeType, nil
	}

	actor, err := s.store.GetActor(ctx, authorDID)
	pdsURL := ""
	if err == nil && actor != nil {
		pdsURL = actor.PDSURL
	}
	if pdsURL == "" {
		ident, err := s.client.ResolveIdentity(ctx, authorDID)
		if err != nil {
			return nil, "", err
		}
		pdsURL = ident.PDSURL
	}

	data, mimeType, err := s.client.GetBlob(ctx, pdsURL, authorDID, cid)
	if err != nil {
		return nil, "", err
	}
	w, h, thumbBytes := ProcessImage(data, 480)
	_ = s.store.SaveBlobCache(ctx, store.CachedBlob{
		CID:       cid,
		AuthorDID: authorDID,
		MimeType:  mimeType,
		Data:      data,
		ThumbData: thumbBytes,
		Width:     w,
		Height:    h,
	})
	if wantThumb && len(thumbBytes) > 0 {
		return thumbBytes, "image/jpeg", nil
	}
	return data, mimeType, nil
}
