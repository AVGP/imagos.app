package service_test

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"net/url"
	"testing"

	"imagos.app/internal/atproto"
	"imagos.app/internal/lexicon"
	"imagos.app/internal/service"
	"imagos.app/internal/store"
)

func createTestPNG(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: 40, G: 110, B: 180, A: 255})
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func loginTestActor(t *testing.T, mockPDS *atproto.MockAtmosphereServer, svc *service.Service, handle, did string) *atproto.OAuthSession {
	t.Helper()
	ctx := context.Background()
	mockPDS.RegisterActor(did, handle)
	pending, err := svc.StartLogin(ctx, handle)
	if err != nil {
		t.Fatalf("StartLogin(%s) failed: %v", handle, err)
	}
	u, _ := url.Parse(pending.RedirectURL)
	code, err := mockPDS.IssueCodeForRequestURI(u.Query().Get("request_uri"), did)
	if err != nil {
		t.Fatalf("IssueCodeForRequestURI failed: %v", err)
	}
	_, sess, err := svc.CompleteLogin(ctx, pending.State, code)
	if err != nil {
		t.Fatalf("CompleteLogin failed: %v", err)
	}
	return sess
}

func TestEndToEndServicePhotoAlbumPostCrossPostAndFollow(t *testing.T) {
	ctx := context.Background()
	mockPDS := atproto.NewMockAtmosphereServer()
	defer mockPDS.Close()

	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()

	atClient := atproto.NewClient(atproto.ClientConfig{
		ClientID:    "http://localhost/oauth/client-metadata.json",
		RedirectURI: "http://localhost/oauth/callback",
		PLCURL:      mockPDS.Server.URL,
		HTTPClient:  mockPDS.Server.Client(),
	})

	svc := service.New(db, atClient)

	aliceDID := "did:plc:alice999"
	bobDID := "did:plc:bob888"

	aliceSess := loginTestActor(t, mockPDS, svc, "alice.imagos.test", aliceDID)
	bobSess := loginTestActor(t, mockPDS, svc, "bob.imagos.test", bobDID)

	// 1. Bob uploads two photos (with tags and cross-posting to Bluesky on the first photo)
	pngBytes := createTestPNG(800, 600)
	photo1, err := svc.UploadPhoto(ctx, bobSess, service.UploadPhotoInput{
		Title:       "Coastal Cliffs",
		Alt:         "Dramatic basalt cliffs along the northern coast at dusk",
		Description: "Long exposure with ND filter.",
		MimeType:    "image/png",
		ImageBytes:  pngBytes,
		Tags:        []string{"landscape", "ocean"},
		PostToBsky:  true,
	})
	if err != nil {
		t.Fatalf("UploadPhoto 1 failed: %v", err)
	}
	if photo1.BskyPostURI == "" {
		t.Errorf("expected photo1 to be cross-posted to Bluesky (app.bsky.feed.post)")
	}
	if len(photo1.TagRefs) != 2 {
		t.Errorf("expected 2 app.imagos.tag strongRefs on photo1, got %d", len(photo1.TagRefs))
	}

	photo2, err := svc.UploadPhoto(ctx, bobSess, service.UploadPhotoInput{
		Title:       "Tide Pool",
		Alt:         "Starfish and anemones in a clear rocky tide pool",
		Description: "Low tide exploration.",
		MimeType:    "image/png",
		ImageBytes:  pngBytes,
		Tags:        []string{"ocean", "macro"},
		PostToBsky:  false,
	})
	if err != nil {
		t.Fatalf("UploadPhoto 2 failed: %v", err)
	}

	// 2. Bob creates an album referencing both photos and cross-posts it to Bluesky
	album, err := svc.CreateAlbum(ctx, bobSess, service.CreateAlbumInput{
		Name:        "Northern Coastline",
		Description: "A weekend studying the Pacific shore.",
		PhotoURIs:   []string{photo1.URI, photo2.URI},
		Tags:        []string{"coast", "ocean"},
		PostToBsky:  true,
	})
	if err != nil {
		t.Fatalf("CreateAlbum failed: %v", err)
	}
	if len(album.Photos) != 2 {
		t.Errorf("expected 2 hydrated photos in album, got %d", len(album.Photos))
	}
	if album.BskyPostURI == "" {
		t.Errorf("expected album to have BskyPostURI when PostToBsky=true")
	}

	// 3. Bob creates a post with both images and cross-posts it to Bluesky
	post, err := svc.CreatePost(ctx, bobSess, service.CreatePostInput{
		Text:       "Two favorites from this weekend's coastal trip.",
		PhotoURIs:  []string{photo1.URI, photo2.URI},
		Tags:       []string{"ocean", "weekend"},
		PostToBsky: true,
	})
	if err != nil {
		t.Fatalf("CreatePost failed: %v", err)
	}
	if len(post.Photos) != 2 {
		t.Errorf("expected 2 photos in post, got %d", len(post.Photos))
	}
	if post.BskyPostURI == "" {
		t.Errorf("expected post to have BskyPostURI when PostToBsky=true")
	}

	// Verify Bluesky posts were actually written to Bob's PDS under app.bsky.feed.post!
	bskyRecords, _, err := atClient.ListRecords(ctx, bobSess.PDSURL, bobDID, lexicon.NSIDBskyPost, 50, "")
	if err != nil {
		t.Fatalf("ListRecords app.bsky.feed.post failed: %v", err)
	}
	if len(bskyRecords) != 3 {
		t.Errorf("expected 3 app.bsky.feed.post records on Bob's PDS (photo1 + album + post), got %d", len(bskyRecords))
	}

	// 4. Alice follows Bob using app.imagos.graph.follow
	followView, err := svc.FollowActor(ctx, aliceSess, "bob.imagos.test")
	if err != nil {
		t.Fatalf("FollowActor failed: %v", err)
	}
	if followView.SubjectDID != bobDID {
		t.Errorf("expected follow subject %s, got %s", bobDID, followView.SubjectDID)
	}

	// Verify app.imagos.graph.follow exists on Alice's PDS
	followRecords, _, err := atClient.ListRecords(ctx, aliceSess.PDSURL, aliceDID, lexicon.NSIDFollow, 50, "")
	if err != nil || len(followRecords) != 1 {
		t.Fatalf("expected 1 app.imagos.graph.follow record on Alice's PDS, got %d (err=%v)", len(followRecords), err)
	}

	// Verify Alice's home feed now contains Bob's post, album, and photos
	feed, err := db.GetFollowingFeed(ctx, aliceDID, 20)
	if err != nil {
		t.Fatalf("GetFollowingFeed failed: %v", err)
	}
	if len(feed) < 3 {
		t.Errorf("expected at least 3 items in Alice's home feed, got %d", len(feed))
	}

	// 5. Alice unfollows Bob
	if err := svc.UnfollowActor(ctx, aliceSess, bobDID); err != nil {
		t.Fatalf("UnfollowActor failed: %v", err)
	}
	followRecordsAfter, _, err := atClient.ListRecords(ctx, aliceSess.PDSURL, aliceDID, lexicon.NSIDFollow, 50, "")
	if err != nil || len(followRecordsAfter) != 0 {
		t.Errorf("expected 0 follow records on Alice's PDS after unfollow, got %d", len(followRecordsAfter))
	}
}

func TestSyncGrainSocialAndBlueskyPhotoPostsFromFollowedAccounts(t *testing.T) {
	ctx := context.Background()
	mockPDS := atproto.NewMockAtmosphereServer()
	defer mockPDS.Close()

	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()

	atClient := atproto.NewClient(atproto.ClientConfig{
		ClientID:    "http://localhost/oauth/client-metadata.json",
		RedirectURI: "http://localhost/oauth/callback",
		PLCURL:      mockPDS.Server.URL,
		HTTPClient:  mockPDS.Server.Client(),
	})
	svc := service.New(db, atClient)

	aliceDID := "did:plc:alicegrain1"
	caraDID := "did:plc:caragrain2"

	aliceSess := loginTestActor(t, mockPDS, svc, "alice.imagos.test", aliceDID)
	mockPDS.RegisterActor(caraDID, "cara.grain.test")

	pngBytes := createTestPNG(900, 600)
	grainBlob := mockPDS.SeedBlob(caraDID, "image/png", pngBytes)

	// 1. Seed a social.grain.photo on Cara's PDS
	grainPhoto := lexicon.GrainPhotoRecord{
		Type:        lexicon.NSIDGrainPhoto,
		Photo:       grainBlob,
		Alt:         "Nordic cabin under aurora borealis",
		AspectRatio: &lexicon.AspectRatio{Width: 900, Height: 600},
		CreatedAt:   "2026-10-31T14:00:00Z",
	}
	grainPhotoRef := mockPDS.SeedRecord(caraDID, lexicon.NSIDGrainPhoto, "3kgrainphoto1", grainPhoto)

	// 2. Seed a social.grain.gallery WITH a photo via social.grain.gallery.item
	grainGallery := lexicon.GrainGalleryRecord{
		Type:        lexicon.NSIDGrainGallery,
		Title:       "Arctic Winter on Grain",
		Description: "Captured above the Arctic Circle.",
		CreatedAt:   "2026-10-31T14:10:00Z",
	}
	grainGalleryRef := mockPDS.SeedRecord(caraDID, lexicon.NSIDGrainGallery, "3kgraingal1", grainGallery)
	mockPDS.SeedRecord(caraDID, lexicon.NSIDGrainGalleryItem, "3kgrainitem1", lexicon.GrainGalleryItemRecord{
		Type:      lexicon.NSIDGrainGalleryItem,
		Gallery:   grainGalleryRef.URI,
		Item:      grainPhotoRef.URI,
		Position:  0,
		CreatedAt: "2026-10-31T14:11:00Z",
	})

	// 3. Seed an EMPTY social.grain.gallery (0 photos) -> must NOT be indexed
	mockPDS.SeedRecord(caraDID, lexicon.NSIDGrainGallery, "3kgraingalempty", lexicon.GrainGalleryRecord{
		Type:      lexicon.NSIDGrainGallery,
		Title:     "Empty Gallery Without Photos",
		CreatedAt: "2026-10-31T14:15:00Z",
	})

	// 4. Seed a native Bluesky post WITH a photo -> MUST be indexed
	bskyImgBlob := mockPDS.SeedBlob(caraDID, "image/png", pngBytes)
	bskyWithPhoto := lexicon.BuildBskyPost("Golden hour shot posted directly on Bluesky!", []*lexicon.PhotoRecord{
		{
			Title:       "Bluesky Sunset",
			Alt:         "Warm sunset clouds over harbor",
			Image:       bskyImgBlob,
			AspectRatio: &lexicon.AspectRatio{Width: 900, Height: 600},
		},
	})
	bskyWithPhoto.CreatedAt = "2026-10-31T14:20:00Z"
	mockPDS.SeedRecord(caraDID, lexicon.NSIDBskyPost, "3kbskyimg1", bskyWithPhoto)

	// 5. Seed a text-only Bluesky post WITHOUT any photo -> must NOT be indexed
	mockPDS.SeedRecord(caraDID, lexicon.NSIDBskyPost, "3kbskytextonly", map[string]any{
		"$type":     lexicon.NSIDBskyPost,
		"text":      "Just a text thought with zero photos.",
		"createdAt": "2026-10-31T14:25:00Z",
	})

	// 6. Alice follows Cara
	if _, err := svc.FollowActor(ctx, aliceSess, "cara.grain.test"); err != nil {
		t.Fatalf("FollowActor(cara) failed: %v", err)
	}

	feed, err := db.GetFollowingFeed(ctx, aliceDID, 30)
	if err != nil {
		t.Fatalf("GetFollowingFeed failed: %v", err)
	}

	foundGrainPhoto := false
	foundGrainGallery := false
	foundEmptyGallery := false
	foundBskyPhotoPost := false
	foundBskyTextOnly := false

	for _, item := range feed {
		switch item.Kind {
		case "photo":
			if item.Photo != nil && item.Photo.Alt == "Nordic cabin under aurora borealis" {
				foundGrainPhoto = true
			}
		case "album":
			if item.Album != nil {
				if item.Album.Name == "Arctic Winter on Grain" && len(item.Album.Photos) == 1 {
					foundGrainGallery = true
				}
				if item.Album.Name == "Empty Gallery Without Photos" {
					foundEmptyGallery = true
				}
			}
		case "post":
			if item.Post != nil {
				if item.Post.Text == "Golden hour shot posted directly on Bluesky!" && len(item.Post.Photos) == 1 {
					foundBskyPhotoPost = true
				}
				if item.Post.Text == "Just a text thought with zero photos." {
					foundBskyTextOnly = true
				}
			}
		}
	}

	if !foundGrainPhoto {
		t.Errorf("expected social.grain.photo to appear in following feed")
	}
	if !foundGrainGallery {
		t.Errorf("expected social.grain.gallery (with >=1 photo) to appear in following feed")
	}
	if foundEmptyGallery {
		t.Errorf("expected empty social.grain.gallery (0 photos) to be excluded from feed")
	}
	if !foundBskyPhotoPost {
		t.Errorf("expected app.bsky.feed.post with image to appear in following feed")
	}
	if foundBskyTextOnly {
		t.Errorf("expected text-only app.bsky.feed.post (0 photos) to be excluded from feed")
	}

	// 7. Test existing social.grain.graph.follow on the signed-in user's PDS:
	// Suppose Alice already follows Dave on grain.social (social.grain.graph.follow on Alice's PDS)
	daveDID := "did:plc:davegrain777"
	mockPDS.RegisterActor(daveDID, "dave.grain.test")
	daveBlob := mockPDS.SeedBlob(daveDID, "image/png", pngBytes)
	davePhotoRef := mockPDS.SeedRecord(daveDID, lexicon.NSIDGrainPhoto, "3kgraindavep1", lexicon.GrainPhotoRecord{
		Type:        lexicon.NSIDGrainPhoto,
		Photo:       daveBlob,
		Alt:         "Patagonia glaciers shot on Grain",
		AspectRatio: &lexicon.AspectRatio{Width: 900, Height: 600},
		CreatedAt:   "2026-10-31T15:00:00Z",
	})
	daveGalRef := mockPDS.SeedRecord(daveDID, lexicon.NSIDGrainGallery, "3kgraindaveg1", lexicon.GrainGalleryRecord{
		Type:        lexicon.NSIDGrainGallery,
		Title:       "Patagonia Expedition",
		Description: "Southern ice fields on grain.social",
		CreatedAt:   "2026-10-31T15:05:00Z",
	})
	mockPDS.SeedRecord(daveDID, lexicon.NSIDGrainGalleryItem, "3kgraindavegi1", lexicon.GrainGalleryItemRecord{
		Type:      lexicon.NSIDGrainGalleryItem,
		Gallery:   daveGalRef.URI,
		Item:      davePhotoRef.URI,
		Position:  0,
		CreatedAt: "2026-10-31T15:06:00Z",
	})

	// Seed social.grain.graph.follow on Alice's PDS pointing to Dave
	mockPDS.SeedRecord(aliceDID, lexicon.NSIDGrainFollow, "3kgrainfollowdave", map[string]any{
		"$type":     lexicon.NSIDGrainFollow,
		"subject":   daveDID,
		"createdAt": "2026-10-31T15:10:00Z",
	})

	// Re-sync Alice from her PDS (as happens on login or profile sync)
	if err := svc.SyncActorFromPDS(ctx, aliceDID, "alice.imagos.test", aliceSess.PDSURL); err != nil {
		t.Fatalf("SyncActorFromPDS(alice) failed: %v", err)
	}

	feedAfterGrainFollow, err := db.GetFollowingFeed(ctx, aliceDID, 50)
	if err != nil {
		t.Fatalf("GetFollowingFeed after grain follow sync failed: %v", err)
	}
	foundDaveGrainGallery := false
	foundDaveGrainPhoto := false
	for _, item := range feedAfterGrainFollow {
		if item.Kind == "album" && item.Album != nil && item.Album.Name == "Patagonia Expedition" {
			foundDaveGrainGallery = true
		}
		if item.Kind == "photo" && item.Photo != nil && item.Photo.Alt == "Patagonia glaciers shot on Grain" {
			foundDaveGrainPhoto = true
		}
	}
	if !foundDaveGrainGallery || !foundDaveGrainPhoto {
		t.Errorf("expected Dave's grain.social gallery (%v) and photo (%v) to appear in Alice's feed via social.grain.graph.follow",
			foundDaveGrainGallery, foundDaveGrainPhoto)
	}

	// 8. Test existing app.bsky.graph.follow on the signed-in user's PDS:
	// Suppose Alice follows Erin on Bluesky (app.bsky.graph.follow on Alice's PDS)
	erinDID := "did:plc:erinbsky888"
	mockPDS.RegisterActor(erinDID, "erin.bsky.test")
	erinBlob := mockPDS.SeedBlob(erinDID, "image/png", pngBytes)

	// Erin has a Bluesky post WITH a photo
	erinPhotoPost := lexicon.BuildBskyPost("Street photography in Lisbon via Bluesky!", []*lexicon.PhotoRecord{
		{
			Title:       "Lisbon Tram",
			Alt:         "Yellow vintage tram climbing a steep cobbled street in Lisbon",
			Image:       erinBlob,
			AspectRatio: &lexicon.AspectRatio{Width: 900, Height: 600},
		},
	})
	erinPhotoPost.CreatedAt = "2026-10-31T16:00:00Z"
	mockPDS.SeedRecord(erinDID, lexicon.NSIDBskyPost, "3kbskyerinp1", erinPhotoPost)

	// Erin also has a text-only Bluesky post (0 photos) -> must NOT appear
	mockPDS.SeedRecord(erinDID, lexicon.NSIDBskyPost, "3kbskyerintext", map[string]any{
		"$type":     lexicon.NSIDBskyPost,
		"text":      "Erin text-only status update on Bluesky",
		"createdAt": "2026-10-31T16:05:00Z",
	})

	// Seed app.bsky.graph.follow on Alice's PDS pointing to Erin
	mockPDS.SeedRecord(aliceDID, lexicon.NSIDBskyFollow, "3kbskyfollowering", map[string]any{
		"$type":     lexicon.NSIDBskyFollow,
		"subject":   erinDID,
		"createdAt": "2026-10-31T16:10:00Z",
	})

	if err := svc.SyncActorFromPDS(ctx, aliceDID, "alice.imagos.test", aliceSess.PDSURL); err != nil {
		t.Fatalf("SyncActorFromPDS(alice) after bsky follow failed: %v", err)
	}

	feedAfterBskyFollow, err := db.GetFollowingFeed(ctx, aliceDID, 50)
	if err != nil {
		t.Fatalf("GetFollowingFeed after bsky follow failed: %v", err)
	}
	foundErinPhotoPost := false
	foundErinTextOnly := false
	for _, item := range feedAfterBskyFollow {
		if item.Kind == "post" && item.Post != nil {
			if item.Post.Text == "Street photography in Lisbon via Bluesky!" && len(item.Post.Photos) == 1 {
				foundErinPhotoPost = true
			}
			if item.Post.Text == "Erin text-only status update on Bluesky" {
				foundErinTextOnly = true
			}
		}
	}
	if !foundErinPhotoPost {
		t.Errorf("expected Erin's photo-bearing Bluesky post to appear in Alice's feed via app.bsky.graph.follow")
	}
	if foundErinTextOnly {
		t.Errorf("expected Erin's text-only Bluesky post to be excluded from Alice's feed")
	}
}

