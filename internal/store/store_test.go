package store_test

import (
	"context"
	"testing"
	"time"

	"imagos.app/internal/lexicon"
	"imagos.app/internal/store"
)

func TestStoreCRUDAndFeedQueries(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("failed to open in-memory sqlite store: %v", err)
	}
	defer db.Close()

	aliceDID := "did:plc:alice123"
	bobDID := "did:plc:bob456"

	if err := db.UpsertActor(ctx, store.Actor{
		DID:    aliceDID,
		Handle: "alice.bsky.social",
		PDSURL: "https://pds.alice.test",
	}); err != nil {
		t.Fatalf("UpsertActor alice failed: %v", err)
	}
	if err := db.UpsertActor(ctx, store.Actor{
		DID:    bobDID,
		Handle: "bob.bsky.social",
		PDSURL: "https://pds.bob.test",
	}); err != nil {
		t.Fatalf("UpsertActor bob failed: %v", err)
	}

	// 1. Tag indexing
	tagRec := lexicon.NewTagRecord("Film", "35mm and medium format analog photography")
	tagURI := lexicon.FormatATURI(bobDID, lexicon.NSIDTag, "3ktag1")
	if err := db.UpsertTag(ctx, tagURI, "bafyreitag1", bobDID, "3ktag1", tagRec); err != nil {
		t.Fatalf("UpsertTag failed: %v", err)
	}

	tags, err := db.ListTagsByAuthor(ctx, bobDID)
	if err != nil || len(tags) != 1 {
		t.Fatalf("expected 1 tag for bob, got %d (err=%v)", len(tags), err)
	}

	// 2. Photo indexing
	photoRec := &lexicon.PhotoRecord{
		Type:        lexicon.NSIDPhoto,
		Title:       "Leica M6 Street",
		Image:       lexicon.NewBlobRef("bafkreibobimg1", "image/jpeg", 54321),
		Alt:         "Pedestrians crossing a rainy Tokyo intersection at night",
		Description: "Shot on CineStill 800T",
		AspectRatio: &lexicon.AspectRatio{Width: 1200, Height: 800},
		TagRefs:     []lexicon.StrongRef{{URI: tagURI, CID: "bafyreitag1"}},
		Tags:        []string{"film", "tokyo"},
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
	}
	photoURI := lexicon.FormatATURI(bobDID, lexicon.NSIDPhoto, "3kphoto1")
	if err := db.UpsertPhoto(ctx, photoURI, "bafyreiphoto1", bobDID, "3kphoto1", photoRec); err != nil {
		t.Fatalf("UpsertPhoto failed: %v", err)
	}

	// 3. Album indexing
	albumRec := &lexicon.AlbumRecord{
		Type:        lexicon.NSIDAlbum,
		Name:        "Tokyo Nights",
		Description: "Neon reflections on wet pavement.",
		Photos:      []lexicon.StrongRef{{URI: photoURI, CID: "bafyreiphoto1"}},
		TagRefs:     []lexicon.StrongRef{{URI: tagURI, CID: "bafyreitag1"}},
		Tags:        []string{"film", "tokyo"},
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
	}
	albumURI := lexicon.FormatATURI(bobDID, lexicon.NSIDAlbum, "3kalbum1")
	if err := db.UpsertAlbum(ctx, albumURI, "bafyreialbum1", bobDID, "3kalbum1", albumRec); err != nil {
		t.Fatalf("UpsertAlbum failed: %v", err)
	}

	// 4. Post indexing
	postRec := &lexicon.PostRecord{
		Type:      lexicon.NSIDPost,
		Text:      "First roll back from the lab!",
		Photos:    []lexicon.StrongRef{{URI: photoURI, CID: "bafyreiphoto1"}},
		TagRefs:   []lexicon.StrongRef{{URI: tagURI, CID: "bafyreitag1"}},
		Tags:      []string{"film", "tokyo"},
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	postURI := lexicon.FormatATURI(bobDID, lexicon.NSIDPost, "3kpost1")
	if err := db.UpsertPost(ctx, postURI, "bafyreipost1", bobDID, "3kpost1", postRec); err != nil {
		t.Fatalf("UpsertPost failed: %v", err)
	}

	// 5. Follow indexing: Alice follows Bob
	followRec := lexicon.NewFollowRecord(bobDID)
	followURI := lexicon.FormatATURI(aliceDID, lexicon.NSIDFollow, "3kfollow1")
	if err := db.UpsertFollow(ctx, followURI, "bafyreifollow1", aliceDID, "3kfollow1", followRec); err != nil {
		t.Fatalf("UpsertFollow failed: %v", err)
	}

	// Verify Alice's following feed includes Bob's post, album, and photo!
	feedItems, err := db.GetFollowingFeed(ctx, aliceDID, 20)
	if err != nil {
		t.Fatalf("GetFollowingFeed failed: %v", err)
	}
	if len(feedItems) < 2 {
		t.Fatalf("expected following feed to include Bob's post and album/photo, got %d items", len(feedItems))
	}

	// Verify Tag search returns Bob's photo, album, and post
	byTag, err := db.GetItemsByTagSlug(ctx, "film")
	if err != nil {
		t.Fatalf("GetItemsByTagSlug failed: %v", err)
	}
	if len(byTag.Photos) != 1 || len(byTag.Albums) != 1 || len(byTag.Posts) != 1 {
		t.Errorf("expected 1 photo, 1 album, 1 post for tag 'film', got photos=%d albums=%d posts=%d",
			len(byTag.Photos), len(byTag.Albums), len(byTag.Posts))
	}

	// Unfollow Bob and verify following feed is empty
	if err := db.DeleteFollowBySubject(ctx, aliceDID, bobDID); err != nil {
		t.Fatalf("DeleteFollowBySubject failed: %v", err)
	}
	feedAfterUnfollow, err := db.GetFollowingFeed(ctx, aliceDID, 20)
	if err != nil {
		t.Fatalf("GetFollowingFeed after unfollow failed: %v", err)
	}
	if len(feedAfterUnfollow) != 0 {
		t.Errorf("expected 0 items in Alice's following feed after unfollowing Bob, got %d", len(feedAfterUnfollow))
	}
}
