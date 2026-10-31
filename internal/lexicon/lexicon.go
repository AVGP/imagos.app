package lexicon

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	NSIDPhoto           = "app.imagos.photo"
	NSIDAlbum           = "app.imagos.album"
	NSIDTag             = "app.imagos.tag"
	NSIDFollow          = "app.imagos.graph.follow"
	NSIDPost            = "app.imagos.feed.post"
	NSIDPostAlias       = "app.imagos.post"
	NSIDBskyPost         = "app.bsky.feed.post"
	NSIDBskyEmbedImages  = "app.bsky.embed.images"
	NSIDBskyFollow       = "app.bsky.graph.follow"
	NSIDGrainPhoto       = "social.grain.photo"
	NSIDGrainGallery     = "social.grain.gallery"
	NSIDGrainGalleryItem = "social.grain.gallery.item"
	NSIDGrainFollow      = "social.grain.graph.follow"
)

const s32Chars = "234567abcdefghijklmnopqrstuvwxyz"

var (
	tidMu      sync.Mutex
	lastTIDMicro int64
	clockID    uint64 = 17
)

// NewTID generates a standard 13-character ATProto base32-sortable TID.
func NewTID() string {
	tidMu.Lock()
	defer tidMu.Unlock()

	micro := time.Now().UnixMicro()
	if micro <= lastTIDMicro {
		micro = lastTIDMicro + 1
	}
	lastTIDMicro = micro

	val := (uint64(micro)&0x1FFFFFFFFFFFFF)<<10 | (clockID & 0x3FF)
	var out [13]byte
	for i := 12; i >= 0; i-- {
		out[i] = s32Chars[val&0x1F]
		val >>= 5
	}
	return string(out[:])
}

// ATURI represents a parsed at://repo/collection/rkey URI.
type ATURI struct {
	Repo       string
	Collection string
	RKey       string
}

func (a ATURI) String() string {
	return fmt.Sprintf("at://%s/%s/%s", a.Repo, a.Collection, a.RKey)
}

func FormatATURI(repo, collection, rkey string) string {
	return fmt.Sprintf("at://%s/%s/%s", repo, collection, rkey)
}

func ParseATURI(uri string) (ATURI, error) {
	if !strings.HasPrefix(uri, "at://") {
		return ATURI{}, fmt.Errorf("invalid AT-URI (must start with at://): %s", uri)
	}
	rest := strings.TrimPrefix(uri, "at://")
	parts := strings.Split(rest, "/")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return ATURI{}, fmt.Errorf("invalid AT-URI structure: %s", uri)
	}
	return ATURI{
		Repo:       parts[0],
		Collection: parts[1],
		RKey:       parts[2],
	}, nil
}

// StrongRef corresponds to com.atproto.repo.strongRef.
type StrongRef struct {
	URI string `json:"uri"`
	CID string `json:"cid"`
}

func (sr StrongRef) Validate() error {
	if _, err := ParseATURI(sr.URI); err != nil {
		return fmt.Errorf("invalid strongRef URI: %w", err)
	}
	if strings.TrimSpace(sr.CID) == "" {
		return errors.New("strongRef CID is required")
	}
	return nil
}

// IPLDLink represents { "$link": "bafkrei..." } inside an ATProto blob.
type IPLDLink struct {
	Link string `json:"$link"`
}

// BlobRef represents an ATProto blob reference.
type BlobRef struct {
	Type     string   `json:"$type"`
	Ref      IPLDLink `json:"ref"`
	MimeType string   `json:"mimeType"`
	Size     int64    `json:"size"`
}

func NewBlobRef(cid, mimeType string, size int64) BlobRef {
	return BlobRef{
		Type:     "blob",
		Ref:      IPLDLink{Link: cid},
		MimeType: mimeType,
		Size:     size,
	}
}

func (b BlobRef) CID() string {
	return b.Ref.Link
}

func (b BlobRef) Validate() error {
	if b.Type != "blob" {
		return errors.New("blob $type must be 'blob'")
	}
	if strings.TrimSpace(b.Ref.Link) == "" {
		return errors.New("blob ref.$link CID is required")
	}
	if strings.TrimSpace(b.MimeType) == "" {
		return errors.New("blob mimeType is required")
	}
	if b.Size <= 0 || b.Size > 10_000_000 {
		return errors.New("blob size must be between 1 byte and 10MB")
	}
	return nil
}

// AspectRatio corresponds to app.imagos.photo#aspectRatio and app.bsky.embed.defs#aspectRatio.
type AspectRatio struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

var nonSlugChars = regexp.MustCompile(`[^a-z0-9]+`)

// NormalizeTagSlug normalizes a tag name into a lowercase hyphenated slug.
func NormalizeTagSlug(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = strings.TrimPrefix(s, "#")
	s = nonSlugChars.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	return s
}

// TagRecord corresponds to app.imagos.tag.
type TagRecord struct {
	Type        string `json:"$type"`
	Name        string `json:"name"`
	Slug        string `json:"slug,omitempty"`
	Description string `json:"description,omitempty"`
	CreatedAt   string `json:"createdAt"`
}

func NewTagRecord(name, description string) *TagRecord {
	cleanName := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(name), "#"))
	return &TagRecord{
		Type:        NSIDTag,
		Name:        cleanName,
		Slug:        NormalizeTagSlug(cleanName),
		Description: strings.TrimSpace(description),
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
	}
}

func (r *TagRecord) Validate() error {
	if r.Type == "" {
		r.Type = NSIDTag
	}
	if r.Type != NSIDTag {
		return fmt.Errorf("expected $type %s, got %s", NSIDTag, r.Type)
	}
	nameLen := utf8.RuneCountInString(strings.TrimSpace(r.Name))
	if nameLen < 1 || nameLen > 64 {
		return errors.New("tag name must be between 1 and 64 characters")
	}
	if utf8.RuneCountInString(r.Description) > 300 {
		return errors.New("tag description must not exceed 300 characters")
	}
	if r.Slug == "" {
		r.Slug = NormalizeTagSlug(r.Name)
	}
	if r.CreatedAt == "" {
		r.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	return nil
}

// PhotoRecord corresponds to app.imagos.photo.
type PhotoRecord struct {
	Type        string       `json:"$type"`
	Title       string       `json:"title"`
	Image       BlobRef      `json:"image"`
	Alt         string       `json:"alt"`
	Description string       `json:"description,omitempty"`
	AspectRatio *AspectRatio `json:"aspectRatio,omitempty"`
	TagRefs     []StrongRef  `json:"tagRefs,omitempty"`
	Tags        []string     `json:"tags,omitempty"`
	BskyPostRef *StrongRef   `json:"bskyPostRef,omitempty"`
	CreatedAt   string       `json:"createdAt"`
}

func (r *PhotoRecord) Validate() error {
	if r.Type == "" {
		r.Type = NSIDPhoto
	}
	if r.Type != NSIDPhoto {
		return fmt.Errorf("expected $type %s, got %s", NSIDPhoto, r.Type)
	}
	titleLen := utf8.RuneCountInString(strings.TrimSpace(r.Title))
	if titleLen < 1 || titleLen > 200 {
		return errors.New("photo title must be between 1 and 200 characters")
	}
	altLen := utf8.RuneCountInString(strings.TrimSpace(r.Alt))
	if altLen < 1 || altLen > 1000 {
		return errors.New("photo alt text is required for accessibility (1-1000 characters)")
	}
	if utf8.RuneCountInString(r.Description) > 2000 {
		return errors.New("photo description must not exceed 2000 characters")
	}
	if err := r.Image.Validate(); err != nil {
		return fmt.Errorf("invalid photo image blob: %w", err)
	}
	for _, tr := range r.TagRefs {
		if err := tr.Validate(); err != nil {
			return fmt.Errorf("invalid tagRef in photo: %w", err)
		}
	}
	if r.CreatedAt == "" {
		r.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	return nil
}

// AlbumRecord corresponds to app.imagos.album.
type AlbumRecord struct {
	Type        string      `json:"$type"`
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	Photos      []StrongRef `json:"photos"`
	TagRefs     []StrongRef `json:"tagRefs,omitempty"`
	Tags        []string    `json:"tags,omitempty"`
	BskyPostRef *StrongRef  `json:"bskyPostRef,omitempty"`
	CreatedAt   string      `json:"createdAt"`
}

func (r *AlbumRecord) Validate() error {
	if r.Type == "" {
		r.Type = NSIDAlbum
	}
	if r.Type != NSIDAlbum {
		return fmt.Errorf("expected $type %s, got %s", NSIDAlbum, r.Type)
	}
	nameLen := utf8.RuneCountInString(strings.TrimSpace(r.Name))
	if nameLen < 1 || nameLen > 200 {
		return errors.New("album name must be between 1 and 200 characters")
	}
	if utf8.RuneCountInString(r.Description) > 2000 {
		return errors.New("album description must not exceed 2000 characters")
	}
	for _, pr := range r.Photos {
		if err := pr.Validate(); err != nil {
			return fmt.Errorf("invalid photo reference in album: %w", err)
		}
	}
	for _, tr := range r.TagRefs {
		if err := tr.Validate(); err != nil {
			return fmt.Errorf("invalid tagRef in album: %w", err)
		}
	}
	if r.CreatedAt == "" {
		r.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	return nil
}

// PostRecord corresponds to app.imagos.feed.post (or app.imagos.post).
type PostRecord struct {
	Type        string      `json:"$type"`
	Text        string      `json:"text"`
	Photos      []StrongRef `json:"photos"`
	TagRefs     []StrongRef `json:"tagRefs,omitempty"`
	Tags        []string    `json:"tags,omitempty"`
	BskyPostRef *StrongRef  `json:"bskyPostRef,omitempty"`
	CreatedAt   string      `json:"createdAt"`
}

func (r *PostRecord) Validate() error {
	if r.Type == "" {
		r.Type = NSIDPost
	}
	if r.Type != NSIDPost && r.Type != NSIDPostAlias {
		return fmt.Errorf("expected $type %s, got %s", NSIDPost, r.Type)
	}
	if utf8.RuneCountInString(r.Text) > 3000 {
		return errors.New("post text must not exceed 3000 characters")
	}
	if len(r.Photos) < 1 {
		return errors.New("post must reference at least one photo")
	}
	if len(r.Photos) > 20 {
		return errors.New("post cannot reference more than 20 photos")
	}
	for _, pr := range r.Photos {
		if err := pr.Validate(); err != nil {
			return fmt.Errorf("invalid photo reference in post: %w", err)
		}
	}
	if r.CreatedAt == "" {
		r.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	return nil
}

// FollowRecord corresponds to app.imagos.graph.follow (using Bluesky's subject DID follow model).
type FollowRecord struct {
	Type      string `json:"$type"`
	Subject   string `json:"subject"`
	CreatedAt string `json:"createdAt"`
}

func NewFollowRecord(subjectDID string) *FollowRecord {
	return &FollowRecord{
		Type:      NSIDFollow,
		Subject:   strings.TrimSpace(subjectDID),
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
}

func (r *FollowRecord) Validate() error {
	if r.Type == "" {
		r.Type = NSIDFollow
	}
	if r.Type != NSIDFollow {
		return fmt.Errorf("expected $type %s, got %s", NSIDFollow, r.Type)
	}
	if !strings.HasPrefix(r.Subject, "did:") || len(r.Subject) < 8 {
		return fmt.Errorf("follow subject must be a valid DID (e.g. did:plc:...): got %q", r.Subject)
	}
	if r.CreatedAt == "" {
		r.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	return nil
}

// BskyEmbedImage represents an item in app.bsky.embed.images#images.
type BskyEmbedImage struct {
	Image       BlobRef      `json:"image"`
	Alt         string       `json:"alt"`
	AspectRatio *AspectRatio `json:"aspectRatio,omitempty"`
}

// BskyEmbedImages represents app.bsky.embed.images.
type BskyEmbedImages struct {
	Type   string           `json:"$type"`
	Images []BskyEmbedImage `json:"images"`
}

// BskyPostRecord represents app.bsky.feed.post for cross-posting to Bluesky.
type BskyPostRecord struct {
	Type      string           `json:"$type"`
	Text      string           `json:"text"`
	Embed     *BskyEmbedImages `json:"embed,omitempty"`
	Langs     []string         `json:"langs,omitempty"`
	CreatedAt string           `json:"createdAt"`
}

// BuildBskyPost constructs an app.bsky.feed.post record embedding up to 4 photo blobs.
func BuildBskyPost(text string, photos []*PhotoRecord) *BskyPostRecord {
	trimmed := strings.TrimSpace(text)
	// Bluesky max grapheme/char length is 300
	if utf8.RuneCountInString(trimmed) > 300 {
		runes := []rune(trimmed)
		trimmed = string(runes[:297]) + "..."
	}

	var embed *BskyEmbedImages
	if len(photos) > 0 {
		maxImages := len(photos)
		if maxImages > 4 {
			maxImages = 4
		}
		imgs := make([]BskyEmbedImage, 0, maxImages)
		for i := 0; i < maxImages; i++ {
			p := photos[i]
			if p == nil {
				continue
			}
			alt := p.Alt
			if alt == "" {
				alt = p.Title
			}
			imgs = append(imgs, BskyEmbedImage{
				Image:       p.Image,
				Alt:         alt,
				AspectRatio: p.AspectRatio,
			})
		}
		if len(imgs) > 0 {
			embed = &BskyEmbedImages{
				Type:   NSIDBskyEmbedImages,
				Images: imgs,
			}
		}
	}

	return &BskyPostRecord{
		Type:      NSIDBskyPost,
		Text:      trimmed,
		Embed:     embed,
		Langs:     []string{"en"},
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
}

// GrainPhotoRecord represents a social.grain.photo record on grain.social.
type GrainPhotoRecord struct {
	Type        string       `json:"$type"`
	Photo       BlobRef      `json:"photo"`
	Alt         string       `json:"alt"`
	AspectRatio *AspectRatio `json:"aspectRatio,omitempty"`
	CreatedAt   string       `json:"createdAt"`
}

func ParseGrainPhoto(raw []byte) (*GrainPhotoRecord, error) {
	var rec GrainPhotoRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, err
	}
	if rec.Photo.CID() == "" {
		return nil, errors.New("social.grain.photo missing photo blob")
	}
	if rec.Alt == "" {
		rec.Alt = "Grain photograph"
	}
	if rec.CreatedAt == "" {
		rec.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	return &rec, nil
}

// GrainGalleryRecord represents a social.grain.gallery record on grain.social.
type GrainGalleryRecord struct {
	Type        string      `json:"$type"`
	Title       string      `json:"title"`
	Description string      `json:"description,omitempty"`
	Photos      []StrongRef `json:"photos,omitempty"` // optional inline refs if present
	CreatedAt   string      `json:"createdAt"`
}

// GrainGalleryItemRecord represents a social.grain.gallery.item linking a photo to a gallery on grain.social.
type GrainGalleryItemRecord struct {
	Type      string `json:"$type"`
	Gallery   string `json:"gallery"`
	Item      string `json:"item"`
	Position  int    `json:"position,omitempty"`
	CreatedAt string `json:"createdAt"`
}

// ParseBskyPostWithImages parses an app.bsky.feed.post JSON payload and extracts embedded images
// (supporting both app.bsky.embed.images and app.bsky.embed.recordWithMedia).
func ParseBskyPostWithImages(raw []byte) (*BskyPostRecord, []BskyEmbedImage, error) {
	var envelope struct {
		Type      string          `json:"$type"`
		Text      string          `json:"text"`
		CreatedAt string          `json:"createdAt"`
		Embed     json.RawMessage `json:"embed"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, nil, err
	}
	post := &BskyPostRecord{
		Type:      NSIDBskyPost,
		Text:      envelope.Text,
		CreatedAt: envelope.CreatedAt,
	}
	if len(envelope.Embed) == 0 {
		return post, nil, nil
	}

	var embedMeta struct {
		Type   string           `json:"$type"`
		Images []BskyEmbedImage `json:"images"`
		Media  *struct {
			Type   string           `json:"$type"`
			Images []BskyEmbedImage `json:"images"`
		} `json:"media"`
	}
	if err := json.Unmarshal(envelope.Embed, &embedMeta); err != nil {
		return post, nil, nil
	}

	var validImgs []BskyEmbedImage
	candidates := embedMeta.Images
	if len(candidates) == 0 && embedMeta.Media != nil {
		candidates = embedMeta.Media.Images
	}
	for _, img := range candidates {
		if img.Image.CID() != "" {
			if img.Alt == "" {
				img.Alt = post.Text
				if img.Alt == "" {
					img.Alt = "Bluesky photo"
				}
			}
			validImgs = append(validImgs, img)
		}
	}
	return post, validImgs, nil
}

