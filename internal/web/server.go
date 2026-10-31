package web

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"imagos.app/internal/atproto"
	"imagos.app/internal/service"
	"imagos.app/internal/store"
)

const (
	sessionCookieName = "imagos_session"
	themeCookieName   = "imagos_theme"
)

type ServerOptions struct {
	PublicURL    string
	DemoAccounts []string
	MockPDS      *atproto.MockAtmosphereServer
}

type Server struct {
	svc  *service.Service
	opts ServerOptions
	mux  *http.ServeMux
}

func NewServer(svc *service.Service, opts ServerOptions) http.Handler {
	s := &Server{
		svc:  svc,
		opts: opts,
		mux:  http.NewServeMux(),
	}
	s.routes()
	return s
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /oauth/client-metadata.json", s.handleClientMetadata)
	s.mux.HandleFunc("GET /oauth/callback", s.handleOAuthCallback)
	s.mux.HandleFunc("GET /login", s.handleLoginGet)
	s.mux.HandleFunc("POST /login", s.handleLoginPost)
	s.mux.HandleFunc("POST /logout", s.handleLogout)

	s.mux.HandleFunc("GET /", s.handleHome)
	s.mux.HandleFunc("GET /explore", s.handleExplore)

	s.mux.HandleFunc("GET /photos/new", s.handlePhotoNew)
	s.mux.HandleFunc("POST /photos", s.handlePhotoCreate)
	s.mux.HandleFunc("GET /photos/{did}/{rkey}", s.handlePhotoDetail)

	s.mux.HandleFunc("GET /albums/new", s.handleAlbumNew)
	s.mux.HandleFunc("POST /albums", s.handleAlbumCreate)
	s.mux.HandleFunc("GET /albums/{did}/{rkey}", s.handleAlbumDetail)

	s.mux.HandleFunc("GET /posts/new", s.handlePostNew)
	s.mux.HandleFunc("POST /posts", s.handlePostCreate)

	s.mux.HandleFunc("GET /tags", s.handleTags)
	s.mux.HandleFunc("POST /tags", s.handleTagCreate)
	s.mux.HandleFunc("GET /tags/{slug}", s.handleTagDetail)

	s.mux.HandleFunc("POST /follow", s.handleFollow)
	s.mux.HandleFunc("POST /unfollow", s.handleUnfollow)

	s.mux.HandleFunc("GET /profile/{actor}", s.handleProfile)
	s.mux.HandleFunc("POST /profile/{actor}/sync", s.handleProfileSync)

	s.mux.HandleFunc("GET /blob/{did}/{cid}", s.handleBlob)
	s.mux.HandleFunc("GET /logo.svg", s.handleLogoSVG)
	s.mux.HandleFunc("POST /theme", s.handleTheme)
}

type gzipResponseWriter struct {
	io.Writer
	http.ResponseWriter
}

func (g *gzipResponseWriter) Write(b []byte) (int, error) {
	return g.Writer.Write(b)
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// Avoid double-compressing binary image blobs
	if !strings.HasPrefix(r.URL.Path, "/blob/") && strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Vary", "Accept-Encoding")
		gz, _ := gzip.NewWriterLevel(w, gzip.BestSpeed)
		defer gz.Close()
		s.mux.ServeHTTP(&gzipResponseWriter{Writer: gz, ResponseWriter: w}, r)
		return
	}
	s.mux.ServeHTTP(w, r)
}

func (s *Server) currentSession(r *http.Request) *atproto.OAuthSession {
	c, err := r.Cookie(sessionCookieName)
	if err != nil || c.Value == "" {
		return nil
	}
	sess, err := s.svc.Store().GetOAuthSession(r.Context(), c.Value)
	if err != nil {
		return nil
	}
	return sess
}

func (s *Server) currentTheme(r *http.Request) string {
	c, err := r.Cookie(themeCookieName)
	if err != nil {
		return "auto"
	}
	switch c.Value {
	case "dark", "light":
		return c.Value
	default:
		return "auto"
	}
}

func (s *Server) handleTheme(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	theme := strings.TrimSpace(r.FormValue("theme"))
	if theme != "dark" && theme != "light" {
		theme = "auto"
	}
	http.SetCookie(w, &http.Cookie{
		Name:     themeCookieName,
		Value:    theme,
		Path:     "/",
		MaxAge:   365 * 86400,
		SameSite: http.SameSiteLaxMode,
	})
	returnTo := strings.TrimSpace(r.FormValue("return_to"))
	if returnTo == "" || !strings.HasPrefix(returnTo, "/") {
		returnTo = "/"
	}
	http.Redirect(w, r, returnTo, http.StatusSeeOther)
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, tmpl string, data map[string]any) {
	if data == nil {
		data = make(map[string]any)
	}
	if _, ok := data["User"]; !ok {
		data["User"] = s.currentSession(r)
	}
	if _, ok := data["Theme"]; !ok {
		data["Theme"] = s.currentTheme(r)
	}
	if _, ok := data["CurrentPath"]; !ok {
		data["CurrentPath"] = r.URL.RequestURI()
	}
	if errMsg := r.URL.Query().Get("err"); errMsg != "" && data["FlashError"] == nil {
		data["FlashError"] = errMsg
	}
	if okMsg := r.URL.Query().Get("msg"); okMsg != "" && data["FlashSuccess"] == nil {
		data["FlashSuccess"] = okMsg
	}

	var buf bytes.Buffer
	if err := pageTemplates.ExecuteTemplate(&buf, tmpl, data); err != nil {
		http.Error(w, "Template render error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(buf.Bytes())
}

func (s *Server) resolvePublicBaseURL(r *http.Request) string {
	if s.opts.PublicURL != "" {
		return strings.TrimRight(s.opts.PublicURL, "/")
	}
	scheme := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0])
	if scheme == "" {
		if r.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	host := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Host"), ",")[0])
	if host == "" {
		host = strings.TrimSpace(r.Host)
	}
	if host == "" {
		return ""
	}
	return scheme + "://" + host
}

func (s *Server) handleClientMetadata(w http.ResponseWriter, r *http.Request) {
	baseURL := s.resolvePublicBaseURL(r)
	clientID, redirectURI := s.svc.Client().ResolveOAuthEndpointsForBaseURL(baseURL)
	if strings.HasPrefix(clientID, "http://localhost?") && baseURL != "" {
		clientID = baseURL + "/oauth/client-metadata.json"
	}
	meta := atproto.BuildClientMetadata(clientID, redirectURI)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(meta)
}

func (s *Server) handleLoginGet(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "login", map[string]any{
		"Title":        "Sign In",
		"Nav":          "login",
		"DemoAccounts": s.opts.DemoAccounts,
	})
}

func (s *Server) handleLoginPost(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	handle := strings.TrimSpace(r.FormValue("handle"))
	if handle == "" {
		http.Redirect(w, r, "/login?err="+url.QueryEscape("Handle or DID is required"), http.StatusSeeOther)
		return
	}
	baseURL := s.resolvePublicBaseURL(r)
	pending, err := s.svc.StartLoginWithBaseURL(r.Context(), handle, baseURL)
	if err != nil {
		http.Redirect(w, r, "/login?err="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	redirectTarget := pending.RedirectURL
	if s.opts.MockPDS != nil && (r.FormValue("auto_approve_demo") == "1" || strings.HasSuffix(strings.ToLower(handle), ".imagos.test")) {
		if parsed, err := url.Parse(pending.RedirectURL); err == nil {
			reqURI := parsed.Query().Get("request_uri")
			if code, err := s.opts.MockPDS.IssueCodeForRequestURI(reqURI, pending.DID); err == nil {
				cbQuery := url.Values{}
				cbQuery.Set("code", code)
				cbQuery.Set("state", pending.State)
				http.Redirect(w, r, "/oauth/callback?"+cbQuery.Encode(), http.StatusSeeOther)
				return
			}
		}
	}
	if r.FormValue("auto_approve_demo") == "1" {
		redirectTarget += "&approve=1"
	}
	http.Redirect(w, r, redirectTarget, http.StatusSeeOther)
}

func (s *Server) handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")
	if state == "" || code == "" {
		http.Redirect(w, r, "/login?err="+url.QueryEscape("Missing OAuth state or code"), http.StatusSeeOther)
		return
	}
	sessionID, sess, err := s.svc.CompleteLogin(r.Context(), state, code)
	if err != nil {
		http.Redirect(w, r, "/login?err="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    sessionID,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  sess.ExpiresAt,
	})
	http.Redirect(w, r, "/?msg="+url.QueryEscape("Signed in as @"+sess.Handle), http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookieName); err == nil && c.Value != "" {
		_ = s.svc.Store().DeleteOAuthSession(r.Context(), c.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
	http.Redirect(w, r, "/?msg="+url.QueryEscape("Signed out"), http.StatusSeeOther)
}

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	sess := s.currentSession(r)
	var items []store.FeedItem
	var follows []store.FollowView
	var err error
	if sess != nil {
		items, err = s.svc.Store().GetFollowingFeed(r.Context(), sess.DID, 30)
		follows, _ = s.svc.Store().ListFollows(r.Context(), sess.DID)
	} else {
		items, err = s.svc.Store().GetExploreFeed(r.Context(), 30)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.render(w, r, "home", map[string]any{
		"Title":     "Home Feed",
		"Nav":       "home",
		"User":      sess,
		"FeedItems": items,
		"Follows":   follows,
	})
}

func (s *Server) handleExplore(w http.ResponseWriter, r *http.Request) {
	sess := s.currentSession(r)
	items, err := s.svc.Store().GetExploreFeed(r.Context(), 30)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var follows []store.FollowView
	if sess != nil {
		follows, _ = s.svc.Store().ListFollows(r.Context(), sess.DID)
	}
	s.render(w, r, "home", map[string]any{
		"Title":     "Explore",
		"Nav":       "explore",
		"User":      sess,
		"FeedItems": items,
		"Follows":   follows,
	})
}

func (s *Server) handlePhotoNew(w http.ResponseWriter, r *http.Request) {
	sess := s.currentSession(r)
	if sess == nil {
		http.Redirect(w, r, "/login?err="+url.QueryEscape("Please sign in to upload photos"), http.StatusSeeOther)
		return
	}
	tags, _ := s.svc.Store().ListTagsByAuthor(r.Context(), sess.DID)
	s.render(w, r, "photo_new", map[string]any{
		"Title":        "Upload Photo",
		"User":         sess,
		"ExistingTags": tags,
	})
}

func (s *Server) handlePhotoCreate(w http.ResponseWriter, r *http.Request) {
	sess := s.currentSession(r)
	if sess == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if err := r.ParseMultipartForm(12 << 20); err != nil {
		http.Redirect(w, r, "/photos/new?err="+url.QueryEscape("Invalid multipart form: "+err.Error()), http.StatusSeeOther)
		return
	}
	file, header, err := r.FormFile("image")
	if err != nil {
		http.Redirect(w, r, "/photos/new?err="+url.QueryEscape("Please choose an image file"), http.StatusSeeOther)
		return
	}
	defer file.Close()
	imgBytes, err := io.ReadAll(io.LimitReader(file, 10<<20))
	if err != nil {
		http.Redirect(w, r, "/photos/new?err="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	mimeType := header.Header.Get("Content-Type")
	if mimeType == "" || mimeType == "application/octet-stream" {
		mimeType = http.DetectContentType(imgBytes)
	}

	photo, err := s.svc.UploadPhoto(r.Context(), sess, service.UploadPhotoInput{
		Title:       r.FormValue("title"),
		Alt:         r.FormValue("alt"),
		Description: r.FormValue("description"),
		MimeType:    mimeType,
		ImageBytes:  imgBytes,
		Tags:        service.ParseTagsString(r.FormValue("tags")),
		PostToBsky:  r.FormValue("post_to_bsky") == "1",
	})
	if err != nil {
		http.Redirect(w, r, "/photos/new?err="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/photos/"+url.PathEscape(photo.AuthorDID)+"/"+url.PathEscape(photo.RKey)+"?msg="+url.QueryEscape("Photo uploaded to your PDS!"), http.StatusSeeOther)
}

func (s *Server) handlePhotoDetail(w http.ResponseWriter, r *http.Request) {
	did, _ := url.PathUnescape(r.PathValue("did"))
	rkey, _ := url.PathUnescape(r.PathValue("rkey"))
	pv, err := s.svc.Store().GetPhotoByAuthorAndRKey(r.Context(), did, rkey)
	if err != nil || pv == nil {
		http.NotFound(w, r)
		return
	}
	s.render(w, r, "photo_detail", map[string]any{
		"Title": pv.Title,
		"Photo": pv,
	})
}

func (s *Server) handleAlbumNew(w http.ResponseWriter, r *http.Request) {
	sess := s.currentSession(r)
	if sess == nil {
		http.Redirect(w, r, "/login?err="+url.QueryEscape("Please sign in to create an album"), http.StatusSeeOther)
		return
	}
	photos, _ := s.svc.Store().ListPhotosByAuthor(r.Context(), sess.DID, 100)
	s.render(w, r, "album_new", map[string]any{
		"Title":        "New Album",
		"User":         sess,
		"UserPhotos":   photos,
		"PreselectURI": r.URL.Query().Get("photo"),
	})
}

func (s *Server) handleAlbumCreate(w http.ResponseWriter, r *http.Request) {
	sess := s.currentSession(r)
	if sess == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		_ = r.ParseMultipartForm(12 << 20)
	} else {
		_ = r.ParseForm()
	}

	photoURIs := append([]string{}, r.Form["photo_uris"]...)

	// Support optional inline photo upload when creating an album
	if file, header, err := r.FormFile("new_image"); err == nil {
		defer file.Close()
		if imgBytes, err := io.ReadAll(io.LimitReader(file, 10<<20)); err == nil && len(imgBytes) > 0 {
			title := strings.TrimSpace(r.FormValue("new_image_title"))
			if title == "" {
				title = strings.TrimSpace(r.FormValue("name")) + " Photo"
			}
			alt := strings.TrimSpace(r.FormValue("new_image_alt"))
			if alt == "" {
				alt = title
			}
			mimeType := header.Header.Get("Content-Type")
			if mimeType == "" || mimeType == "application/octet-stream" {
				mimeType = http.DetectContentType(imgBytes)
			}
			uploaded, err := s.svc.UploadPhoto(r.Context(), sess, service.UploadPhotoInput{
				Title:      title,
				Alt:        alt,
				MimeType:   mimeType,
				ImageBytes: imgBytes,
				Tags:       service.ParseTagsString(r.FormValue("tags")),
			})
			if err != nil {
				http.Redirect(w, r, "/albums/new?err="+url.QueryEscape(err.Error()), http.StatusSeeOther)
				return
			}
			photoURIs = append(photoURIs, uploaded.URI)
		}
	}

	album, err := s.svc.CreateAlbum(r.Context(), sess, service.CreateAlbumInput{
		Name:        r.FormValue("name"),
		Description: r.FormValue("description"),
		PhotoURIs:   photoURIs,
		Tags:        service.ParseTagsString(r.FormValue("tags")),
		PostToBsky:  r.FormValue("post_to_bsky") == "1",
	})
	if err != nil {
		http.Redirect(w, r, "/albums/new?err="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/albums/"+url.PathEscape(album.AuthorDID)+"/"+url.PathEscape(album.RKey)+"?msg="+url.QueryEscape("Album created on your PDS!"), http.StatusSeeOther)
}

func (s *Server) handleAlbumDetail(w http.ResponseWriter, r *http.Request) {
	did, _ := url.PathUnescape(r.PathValue("did"))
	rkey, _ := url.PathUnescape(r.PathValue("rkey"))
	av, err := s.svc.Store().GetAlbumByAuthorAndRKey(r.Context(), did, rkey)
	if err != nil || av == nil {
		http.NotFound(w, r)
		return
	}
	s.render(w, r, "album_detail", map[string]any{
		"Title": av.Name,
		"Album": av,
	})
}

func (s *Server) handlePostNew(w http.ResponseWriter, r *http.Request) {
	sess := s.currentSession(r)
	if sess == nil {
		http.Redirect(w, r, "/login?err="+url.QueryEscape("Please sign in to create a post"), http.StatusSeeOther)
		return
	}
	photos, _ := s.svc.Store().ListPhotosByAuthor(r.Context(), sess.DID, 100)
	s.render(w, r, "post_new", map[string]any{
		"Title":        "New Post",
		"User":         sess,
		"UserPhotos":   photos,
		"PreselectURI": r.URL.Query().Get("photo"),
	})
}

func (s *Server) handlePostCreate(w http.ResponseWriter, r *http.Request) {
	sess := s.currentSession(r)
	if sess == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		_ = r.ParseMultipartForm(12 << 20)
	} else {
		_ = r.ParseForm()
	}

	photoURIs := append([]string{}, r.Form["photo_uris"]...)

	if file, header, err := r.FormFile("new_image"); err == nil {
		defer file.Close()
		if imgBytes, err := io.ReadAll(io.LimitReader(file, 10<<20)); err == nil && len(imgBytes) > 0 {
			title := strings.TrimSpace(r.FormValue("new_image_title"))
			if title == "" {
				title = "Post Photo"
			}
			alt := strings.TrimSpace(r.FormValue("new_image_alt"))
			if alt == "" {
				alt = title
			}
			mimeType := header.Header.Get("Content-Type")
			if mimeType == "" || mimeType == "application/octet-stream" {
				mimeType = http.DetectContentType(imgBytes)
			}
			uploaded, err := s.svc.UploadPhoto(r.Context(), sess, service.UploadPhotoInput{
				Title:      title,
				Alt:        alt,
				MimeType:   mimeType,
				ImageBytes: imgBytes,
				Tags:       service.ParseTagsString(r.FormValue("tags")),
			})
			if err != nil {
				http.Redirect(w, r, "/posts/new?err="+url.QueryEscape(err.Error()), http.StatusSeeOther)
				return
			}
			photoURIs = append(photoURIs, uploaded.URI)
		}
	}

	_, err := s.svc.CreatePost(r.Context(), sess, service.CreatePostInput{
		Text:       r.FormValue("text"),
		PhotoURIs:  photoURIs,
		Tags:       service.ParseTagsString(r.FormValue("tags")),
		PostToBsky: r.FormValue("post_to_bsky") == "1",
	})
	if err != nil {
		http.Redirect(w, r, "/posts/new?err="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/?msg="+url.QueryEscape("Post published to your PDS!"), http.StatusSeeOther)
}

func (s *Server) handleTags(w http.ResponseWriter, r *http.Request) {
	tags, _ := s.svc.Store().ListAllTags(r.Context())
	s.render(w, r, "tags", map[string]any{
		"Title": "Tags",
		"Nav":   "tags",
		"Tags":  tags,
	})
}

func (s *Server) handleTagCreate(w http.ResponseWriter, r *http.Request) {
	sess := s.currentSession(r)
	if sess == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	_ = r.ParseForm()
	tv, err := s.svc.CreateTag(r.Context(), sess, r.FormValue("name"), r.FormValue("description"))
	if err != nil {
		http.Redirect(w, r, "/tags?err="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/tags/"+url.PathEscape(tv.Slug)+"?msg="+url.QueryEscape("Created app.imagos.tag record #"+tv.Name), http.StatusSeeOther)
}

func (s *Server) handleTagDetail(w http.ResponseWriter, r *http.Request) {
	slug, _ := url.PathUnescape(r.PathValue("slug"))
	tags, _ := s.svc.Store().ListAllTags(r.Context())
	results, err := s.svc.Store().GetItemsByTagSlug(r.Context(), slug)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.render(w, r, "tags", map[string]any{
		"Title":      "#" + slug,
		"Nav":        "tags",
		"Tags":       tags,
		"TagResults": results,
	})
}

func (s *Server) handleFollow(w http.ResponseWriter, r *http.Request) {
	sess := s.currentSession(r)
	if sess == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	_ = r.ParseForm()
	target := strings.TrimSpace(r.FormValue("handle"))
	fv, err := s.svc.FollowActor(r.Context(), sess, target)
	if err != nil {
		http.Redirect(w, r, "/?err="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/?msg="+url.QueryEscape("Now following @"+fv.SubjectHandle+" via app.imagos.graph.follow!"), http.StatusSeeOther)
}

func (s *Server) handleUnfollow(w http.ResponseWriter, r *http.Request) {
	sess := s.currentSession(r)
	if sess == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	_ = r.ParseForm()
	subjectDID := strings.TrimSpace(r.FormValue("subject_did"))
	if err := s.svc.UnfollowActor(r.Context(), sess, subjectDID); err != nil {
		http.Redirect(w, r, "/?err="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/?msg="+url.QueryEscape("Unfollowed account"), http.StatusSeeOther)
}

func (s *Server) handleProfile(w http.ResponseWriter, r *http.Request) {
	actorParam, _ := url.PathUnescape(r.PathValue("actor"))
	actor, _ := s.svc.Store().GetActor(r.Context(), actorParam)
	if actor == nil {
		if ident, err := s.svc.Client().ResolveIdentity(r.Context(), actorParam); err == nil {
			_ = s.svc.SyncActorFromPDS(r.Context(), ident.DID, ident.Handle, ident.PDSURL)
			actor, _ = s.svc.Store().GetActor(r.Context(), ident.DID)
		}
	}
	if actor == nil {
		http.NotFound(w, r)
		return
	}

	sess := s.currentSession(r)
	isFollowing := false
	if sess != nil {
		if fv, _ := s.svc.Store().GetFollow(r.Context(), sess.DID, actor.DID); fv != nil {
			isFollowing = true
		}
	}
	followerCount, _ := s.svc.Store().CountFollowers(r.Context(), actor.DID)
	photos, _ := s.svc.Store().ListPhotosByAuthor(r.Context(), actor.DID, 50)
	albums, _ := s.svc.Store().ListAlbumsByAuthor(r.Context(), actor.DID, 50)
	posts, _ := s.svc.Store().ListPostsByAuthor(r.Context(), actor.DID, 50)

	s.render(w, r, "profile", map[string]any{
		"Title":         "@" + actor.Handle,
		"Nav":           "profile",
		"User":          sess,
		"ProfileActor":  actor,
		"IsFollowing":   isFollowing,
		"FollowerCount": followerCount,
		"Photos":        photos,
		"Albums":        albums,
		"Posts":         posts,
	})
}

func (s *Server) handleProfileSync(w http.ResponseWriter, r *http.Request) {
	actorParam, _ := url.PathUnescape(r.PathValue("actor"))
	ident, err := s.svc.Client().ResolveIdentity(r.Context(), actorParam)
	if err != nil {
		http.Redirect(w, r, "/?err="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	_ = s.svc.SyncActorFromPDS(r.Context(), ident.DID, ident.Handle, ident.PDSURL)
	http.Redirect(w, r, "/profile/"+url.PathEscape(ident.Handle)+"?msg="+url.QueryEscape("Synced records from PDS!"), http.StatusSeeOther)
}

func (s *Server) handleBlob(w http.ResponseWriter, r *http.Request) {
	did, _ := url.PathUnescape(r.PathValue("did"))
	cid, _ := url.PathUnescape(r.PathValue("cid"))
	wantThumb := r.URL.Query().Get("thumb") == "1"

	etag := `"` + cid
	if wantThumb {
		etag += "-thumb"
	}
	etag += `"`

	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	data, mimeType, err := s.svc.GetOrFetchBlob(r.Context(), did, cid, wantThumb)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("ETag", etag)
	w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
	_, _ = w.Write(data)
}

func (s *Server) handleLogoSVG(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write([]byte(BrandLogoSVG))
}

