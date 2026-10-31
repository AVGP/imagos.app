package lexicon_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"imagos.app/internal/lexicon"
)

func TestGenerateTID(t *testing.T) {
	tid1 := lexicon.NewTID()
	tid2 := lexicon.NewTID()
	if len(tid1) != 13 {
		t.Fatalf("expected 13-char TID, got %q (len %d)", tid1, len(tid1))
	}
	if tid1 >= tid2 {
		t.Fatalf("expected monotonic TIDs: %q < %q", tid1, tid2)
	}
}

func TestParseATURI(t *testing.T) {
	uri := "at://did:plc:z72i7hdynmk6r22z27h6tvur/app.imagos.photo/3lb5abc123xyz"
	parsed, err := lexicon.ParseATURI(uri)
	if err != nil {
		t.Fatalf("unexpected error parsing AT-URI: %v", err)
	}
	if parsed.Repo != "did:plc:z72i7hdynmk6r22z27h6tvur" {
		t.Errorf("expected repo did:plc:z72i7hdynmk6r22z27h6tvur, got %s", parsed.Repo)
	}
	if parsed.Collection != lexicon.NSIDPhoto {
		t.Errorf("expected collection %s, got %s", lexicon.NSIDPhoto, parsed.Collection)
	}
	if parsed.RKey != "3lb5abc123xyz" {
		t.Errorf("expected rkey 3lb5abc123xyz, got %s", parsed.RKey)
	}

	if _, err := lexicon.ParseATURI("http://invalid/uri"); err == nil {
		t.Errorf("expected error for non-at:// URI")
	}
}

func TestTagRecordValidationAndJSON(t *testing.T) {
	tag := lexicon.NewTagRecord("Street Photography", "Candid urban life")
	if err := tag.Validate(); err != nil {
		t.Fatalf("expected valid tag record, got error: %v", err)
	}
	if tag.Type != lexicon.NSIDTag {
		t.Errorf("expected $type %s, got %s", lexicon.NSIDTag, tag.Type)
	}
	if tag.Slug != "street-photography" {
		t.Errorf("expected slug street-photography, got %s", tag.Slug)
	}

	emptyTag := &lexicon.TagRecord{Name: ""}
	if err := emptyTag.Validate(); err == nil {
		t.Errorf("expected error for empty tag name")
	}
}

func TestPhotoRecordValidationAndJSON(t *testing.T) {
	blob := lexicon.NewBlobRef("bafkreihdwdcefgh4dqkjv67uzcmw7ojee6xedzdetojuzjevtenxquvyku", "image/jpeg", 204800)
	tagRef := lexicon.StrongRef{
		URI: "at://did:plc:alice123/app.imagos.tag/3ktag1",
		CID: "bafyreitag1",
	}
	photo := &lexicon.PhotoRecord{
		Type:        lexicon.NSIDPhoto,
		Title:       "Golden Gate in Fog",
		Image:       blob,
		Alt:         "Red suspension bridge towers emerging from thick morning fog over the bay",
		Description: "Taken at 6am from Hawk Hill.",
		AspectRatio: &lexicon.AspectRatio{Width: 1600, Height: 1067},
		TagRefs:     []lexicon.StrongRef{tagRef},
		Tags:        []string{"fog", "san-francisco"},
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
	}

	if err := photo.Validate(); err != nil {
		t.Fatalf("expected valid photo record, got: %v", err)
	}

	raw, err := json.Marshal(photo)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	if !strings.Contains(string(raw), `"$type":"app.imagos.photo"`) {
		t.Errorf("expected $type app.imagos.photo in JSON: %s", string(raw))
	}
	if !strings.Contains(string(raw), `"$link":"bafkreihdwdcefgh4dqkjv67uzcmw7ojee6xedzdetojuzjevtenxquvyku"`) {
		t.Errorf("expected blob $link in JSON: %s", string(raw))
	}

	// Missing alt tag should still validate or default? Wait: accessibility requires alt or title fallback, let's check validation rules
	missingTitle := *photo
	missingTitle.Title = ""
	if err := missingTitle.Validate(); err == nil {
		t.Errorf("expected error when photo title is empty")
	}

	missingAlt := *photo
	missingAlt.Alt = ""
	if err := missingAlt.Validate(); err == nil {
		t.Errorf("expected error when photo alt text is empty (accessibility requirement)")
	}
}

func TestAlbumRecordValidation(t *testing.T) {
	photoRef := lexicon.StrongRef{
		URI: "at://did:plc:alice123/app.imagos.photo/3kphoto1",
		CID: "bafyreiphoto1",
	}
	album := &lexicon.AlbumRecord{
		Type:        lexicon.NSIDAlbum,
		Name:        "Pacific Northwest Autumn",
		Description: "Film and digital captures from Oregon and Washington.",
		Photos:      []lexicon.StrongRef{photoRef},
		Tags:        []string{"autumn", "pnw"},
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
	}

	if err := album.Validate(); err != nil {
		t.Fatalf("expected valid album, got: %v", err)
	}

	invalidAlbum := *album
	invalidAlbum.Name = ""
	if err := invalidAlbum.Validate(); err == nil {
		t.Errorf("expected error for album without name")
	}
}

func TestPostRecordValidation(t *testing.T) {
	photoRef := lexicon.StrongRef{
		URI: "at://did:plc:alice123/app.imagos.photo/3kphoto1",
		CID: "bafyreiphoto1",
	}
	post := &lexicon.PostRecord{
		Type:      lexicon.NSIDPost,
		Text:      "Morning light through the redwoods.",
		Photos:    []lexicon.StrongRef{photoRef},
		Tags:      []string{"nature"},
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}

	if err := post.Validate(); err != nil {
		t.Fatalf("expected valid post, got: %v", err)
	}

	noPhotosPost := *post
	noPhotosPost.Photos = nil
	if err := noPhotosPost.Validate(); err == nil {
		t.Errorf("expected error when post has zero photos")
	}
}

func TestFollowRecordValidation(t *testing.T) {
	follow := lexicon.NewFollowRecord("did:plc:bob456789abc")
	if err := follow.Validate(); err != nil {
		t.Fatalf("expected valid follow record, got: %v", err)
	}
	if follow.Type != lexicon.NSIDFollow {
		t.Errorf("expected $type %s, got %s", lexicon.NSIDFollow, follow.Type)
	}

	badFollow := lexicon.NewFollowRecord("not-a-did")
	if err := badFollow.Validate(); err == nil {
		t.Errorf("expected error for invalid subject DID in follow record")
	}
}

func TestBuildBskyPostFromPhotos(t *testing.T) {
	p1 := &lexicon.PhotoRecord{
		Title: "Sunrise",
		Alt:   "Orange sunrise over mountains",
		Image: lexicon.NewBlobRef("bafkrei1", "image/jpeg", 12345),
		AspectRatio: &lexicon.AspectRatio{
			Width:  1200,
			Height: 800,
		},
	}
	bskyPost := lexicon.BuildBskyPost("Check out my new shot!", []*lexicon.PhotoRecord{p1})
	if bskyPost.Type != lexicon.NSIDBskyPost {
		t.Errorf("expected $type %s, got %s", lexicon.NSIDBskyPost, bskyPost.Type)
	}
	if bskyPost.Embed == nil || bskyPost.Embed.Type != lexicon.NSIDBskyEmbedImages {
		t.Fatalf("expected embed $type %s", lexicon.NSIDBskyEmbedImages)
	}
	if len(bskyPost.Embed.Images) != 1 {
		t.Fatalf("expected 1 embedded image, got %d", len(bskyPost.Embed.Images))
	}
	if bskyPost.Embed.Images[0].Alt != "Orange sunrise over mountains" {
		t.Errorf("expected alt text preserved in Bluesky embed, got %q", bskyPost.Embed.Images[0].Alt)
	}
}

func TestParseGrainAndBskyRecords(t *testing.T) {
	grainPhotoJSON := `{
		"$type": "social.grain.photo",
		"photo": {
			"$type": "blob",
			"ref": {"$link": "bafkreigrain1"},
			"mimeType": "image/jpeg",
			"size": 98765
		},
		"alt": "Misty fjord at dawn",
		"aspectRatio": {"width": 1500, "height": 1000},
		"createdAt": "2026-10-31T10:00:00Z"
	}`
	gp, err := lexicon.ParseGrainPhoto([]byte(grainPhotoJSON))
	if err != nil {
		t.Fatalf("ParseGrainPhoto failed: %v", err)
	}
	if gp.Photo.CID() != "bafkreigrain1" || gp.Alt != "Misty fjord at dawn" {
		t.Errorf("unexpected parsed GrainPhoto: %+v", gp)
	}

	// Bluesky post without images should return 0 images
	textOnlyBsky := `{"$type":"app.bsky.feed.post","text":"Just text, no photo","createdAt":"2026-10-31T11:00:00Z"}`
	_, imgs, err := lexicon.ParseBskyPostWithImages([]byte(textOnlyBsky))
	if err != nil {
		t.Fatalf("ParseBskyPostWithImages failed on text post: %v", err)
	}
	if len(imgs) != 0 {
		t.Errorf("expected 0 images for text-only Bluesky post, got %d", len(imgs))
	}

	// Bluesky post with images should extract images
	imgBsky := `{
		"$type": "app.bsky.feed.post",
		"text": "Autumn leaves in Berlin",
		"embed": {
			"$type": "app.bsky.embed.images",
			"images": [
				{
					"image": {"$type": "blob", "ref": {"$link": "bafkreibskyimg1"}, "mimeType": "image/jpeg", "size": 45678},
					"alt": "Golden maple leaves on cobblestones",
					"aspectRatio": {"width": 1200, "height": 900}
				}
			]
		},
		"createdAt": "2026-10-31T11:30:00Z"
	}`
	post, imgs, err := lexicon.ParseBskyPostWithImages([]byte(imgBsky))
	if err != nil {
		t.Fatalf("ParseBskyPostWithImages failed: %v", err)
	}
	if post.Text != "Autumn leaves in Berlin" || len(imgs) != 1 {
		t.Fatalf("expected 1 extracted image from Bluesky post, got %d", len(imgs))
	}
	if imgs[0].Image.CID() != "bafkreibskyimg1" {
		t.Errorf("expected CID bafkreibskyimg1, got %s", imgs[0].Image.CID())
	}
}

