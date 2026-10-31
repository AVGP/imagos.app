package web_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"imagos.app/internal/atproto"
	"imagos.app/internal/service"
	"imagos.app/internal/store"
	"imagos.app/internal/web"
)

func makeSamplePNG(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8((x * 255) / w), G: 120, B: uint8((y * 255) / h), A: 255})
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func TestWebSSRWorkflowAccessibilityAnd3GPerformance(t *testing.T) {
	mockPDS := atproto.NewMockAtmosphereServer()
	defer mockPDS.Close()

	aliceDID := "did:plc:aliceweb123"
	bobDID := "did:plc:bobweb456"
	mockPDS.RegisterActor(aliceDID, "alice.imagos.test")
	mockPDS.RegisterActor(bobDID, "bob.imagos.test")

	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()

	var appServer *httptest.Server
	atClient := atproto.NewClient(atproto.ClientConfig{
		ClientID:    "http://localhost/oauth/client-metadata.json",
		RedirectURI: "http://localhost/oauth/callback",
		PLCURL:      mockPDS.Server.URL,
		HTTPClient:  mockPDS.Server.Client(),
	})
	svc := service.New(db, atClient)
	handler := web.NewServer(svc, web.ServerOptions{
		DemoAccounts: []string{"alice.imagos.test", "bob.imagos.test"},
	})
	appServer = httptest.NewServer(handler)
	defer appServer.Close()

	jar, _ := cookiejar.New(nil)
	httpClient := &http.Client{
		Jar: jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// 1. Check OAuth Client Metadata endpoint (and verify dynamic public URL resolution via X-Forwarded-Host / Host)
	metaReq, _ := http.NewRequest(http.MethodGet, appServer.URL+"/oauth/client-metadata.json", nil)
	metaReq.Header.Set("X-Forwarded-Proto", "https")
	metaReq.Header.Set("X-Forwarded-Host", "imagos.app")
	metaResp, err := httpClient.Do(metaReq)
	if err != nil || metaResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK from /oauth/client-metadata.json, got %v (err=%v)", metaResp, err)
	}
	metaBytes, _ := io.ReadAll(metaResp.Body)
	metaResp.Body.Close()
	if !strings.Contains(string(metaBytes), `"https://imagos.app/oauth/callback"`) {
		t.Errorf("expected /oauth/client-metadata.json to use public URL https://imagos.app/oauth/callback, got: %s", string(metaBytes))
	}

	// 2. Initiate OAuth Login for alice.imagos.test
	loginForm := url.Values{"handle": {"alice.imagos.test"}}
	loginResp, err := httpClient.PostForm(appServer.URL+"/login", loginForm)
	if err != nil {
		t.Fatalf("POST /login failed: %v", err)
	}
	loginResp.Body.Close()
	if loginResp.StatusCode != http.StatusSeeOther && loginResp.StatusCode != http.StatusFound {
		t.Fatalf("expected redirect from POST /login, got %d", loginResp.StatusCode)
	}
	authLoc := loginResp.Header.Get("Location")
	parsedAuth, err := url.Parse(authLoc)
	if err != nil {
		t.Fatalf("parse auth redirect: %v", err)
	}
	reqURI := parsedAuth.Query().Get("request_uri")
	if reqURI == "" {
		t.Fatalf("expected PAR request_uri in redirect %s", authLoc)
	}

	// 3. Approve at Mock Authorization Server (?approve=1)
	approveResp, err := httpClient.Get(authLoc + "&approve=1")
	if err != nil {
		t.Fatalf("GET authorize approve failed: %v", err)
	}
	approveResp.Body.Close()
	cbLoc := approveResp.Header.Get("Location")
	parsedCB, err := url.Parse(cbLoc)
	if err != nil {
		t.Fatalf("parse callback URL: %v", err)
	}

	// 4. Hit /oauth/callback on appServer
	cbResp, err := httpClient.Get(appServer.URL + "/oauth/callback?" + parsedCB.RawQuery)
	if err != nil {
		t.Fatalf("GET /oauth/callback failed: %v", err)
	}
	cbResp.Body.Close()
	if cbResp.StatusCode != http.StatusSeeOther && cbResp.StatusCode != http.StatusFound {
		t.Fatalf("expected redirect from /oauth/callback, got %d", cbResp.StatusCode)
	}

	// 5. Upload a Photo via multipart POST /photos (with cross-posting to Bluesky)
	var bodyBuf bytes.Buffer
	mw := multipart.NewWriter(&bodyBuf)
	_ = mw.WriteField("title", "Sierra Dawn")
	_ = mw.WriteField("alt", "Granite peaks glowing pink at sunrise above an alpine lake")
	_ = mw.WriteField("description", "Backpacking in the High Sierra.")
	_ = mw.WriteField("tags", "mountains, alpine, dawn")
	_ = mw.WriteField("post_to_bsky", "1")
	fw, _ := mw.CreateFormFile("image", "sierra.png")
	_, _ = fw.Write(makeSamplePNG(1200, 900))
	_ = mw.Close()

	upReq, _ := http.NewRequest(http.MethodPost, appServer.URL+"/photos", &bodyBuf)
	upReq.Header.Set("Content-Type", mw.FormDataContentType())
	upResp, err := httpClient.Do(upReq)
	if err != nil {
		t.Fatalf("POST /photos failed: %v", err)
	}
	upResp.Body.Close()
	if upResp.StatusCode != http.StatusSeeOther && upResp.StatusCode != http.StatusFound {
		t.Fatalf("expected redirect after photo upload, got %d", upResp.StatusCode)
	}

	photos, err := db.ListPhotosByAuthor(context.Background(), aliceDID, 10)
	if err != nil || len(photos) != 1 {
		t.Fatalf("expected 1 uploaded photo for Alice, got %d", len(photos))
	}
	photoURI := photos[0].URI

	// 6. Create an Album via POST /albums (with cross-posting to Bluesky)
	albumForm := url.Values{
		"name":         {"High Sierra Trail"},
		"description":  {"Summer alpine photography."},
		"photo_uris":   {photoURI},
		"tags":         {"mountains, trail"},
		"post_to_bsky": {"1"},
	}
	albResp, err := httpClient.PostForm(appServer.URL+"/albums", albumForm)
	if err != nil {
		t.Fatalf("POST /albums failed: %v", err)
	}
	albResp.Body.Close()
	if albResp.StatusCode != http.StatusSeeOther && albResp.StatusCode != http.StatusFound {
		t.Fatalf("expected redirect after album creation, got %d", albResp.StatusCode)
	}

	// 7. Create a Post via POST /posts (referencing the uploaded photo and cross-posting to Bluesky)
	var postBuf bytes.Buffer
	pmw := multipart.NewWriter(&postBuf)
	_ = pmw.WriteField("text", "First light at 11,000 feet.")
	_ = pmw.WriteField("photo_uris", photoURI)
	_ = pmw.WriteField("tags", "mountains, sunrise")
	_ = pmw.WriteField("post_to_bsky", "1")
	_ = pmw.Close()

	postReq, _ := http.NewRequest(http.MethodPost, appServer.URL+"/posts", &postBuf)
	postReq.Header.Set("Content-Type", pmw.FormDataContentType())
	postResp, err := httpClient.Do(postReq)
	if err != nil {
		t.Fatalf("POST /posts failed: %v", err)
	}
	postResp.Body.Close()
	if postResp.StatusCode != http.StatusSeeOther && postResp.StatusCode != http.StatusFound {
		t.Fatalf("expected redirect after post creation, got %d", postResp.StatusCode)
	}

	// 8. Follow bob.imagos.test via POST /follow
	followResp, err := httpClient.PostForm(appServer.URL+"/follow", url.Values{"handle": {"bob.imagos.test"}})
	if err != nil {
		t.Fatalf("POST /follow failed: %v", err)
	}
	followResp.Body.Close()
	if followResp.StatusCode != http.StatusSeeOther && followResp.StatusCode != http.StatusFound {
		t.Fatalf("expected redirect after follow, got %d", followResp.StatusCode)
	}

	// 9. Verify Home Feed SSR HTML, Gzip Compression, Accessibility, and 3G Payload Budget
	homeReq, _ := http.NewRequest(http.MethodGet, appServer.URL+"/", nil)
	homeReq.Header.Set("Accept-Encoding", "gzip")
	start := time.Now()
	homeResp, err := httpClient.Do(homeReq)
	if err != nil {
		t.Fatalf("GET / failed: %v", err)
	}
	defer homeResp.Body.Close()
	if homeResp.Header.Get("Content-Encoding") != "gzip" {
		t.Errorf("expected Content-Encoding: gzip on SSR response")
	}
	compressedBytes, err := io.ReadAll(homeResp.Body)
	if err != nil {
		t.Fatalf("read compressed body: %v", err)
	}
	elapsedServer := time.Since(start)

	// On a slow 3G connection (500 Kbps = ~62.5 KB/s, 100ms RTT), initial compressed HTML should be < 20 KB
	if len(compressedBytes) > 20*1024 {
		t.Errorf("3G budget exceeded: compressed SSR HTML is %d bytes (want < 20480 bytes)", len(compressedBytes))
	}

	gzReader, err := gzip.NewReader(bytes.NewReader(compressedBytes))
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	htmlBytes, _ := io.ReadAll(gzReader)
	htmlStr := string(htmlBytes)

	// Verify Accessibility, Logo, Theme Toggle, & Semantic HTML landmarks
	requiredSnippets := []string{
		`<html lang="en"`,
		`class="skip-link"`,
		`<main id="main-content"`,
		`aria-label="Main navigation"`,
		`class="brand-logo"`,
		`aria-label="Color theme"`,
		`Sierra Dawn`,
		`High Sierra Trail`,
		`First light at 11,000 feet.`,
		`alt="Granite peaks glowing pink at sunrise above an alpine lake"`,
		`loading="lazy"`,
	}
	for _, snippet := range requiredSnippets {
		if !strings.Contains(htmlStr, snippet) {
			t.Errorf("expected SSR HTML to contain %q", snippet)
		}
	}
	if strings.Contains(htmlStr, `class="brand-badge"`) {
		t.Errorf("expected header brand-badge to be removed")
	}
	if strings.Contains(htmlStr, `href="/lexicons"`) {
		t.Errorf("expected /lexicons nav link to be removed")
	}

	// Verify /lexicons page is removed (404) and /logo.svg serves the circular SVG logo
	lexResp, err := httpClient.Get(appServer.URL + "/lexicons")
	if err != nil {
		t.Fatalf("GET /lexicons failed: %v", err)
	}
	lexResp.Body.Close()
	if lexResp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 NotFound for removed /lexicons page, got %d", lexResp.StatusCode)
	}

	logoResp, err := httpClient.Get(appServer.URL + "/logo.svg")
	if err != nil || logoResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK from /logo.svg, got %v (err=%v)", logoResp, err)
	}
	logoBytes, _ := io.ReadAll(logoResp.Body)
	logoResp.Body.Close()
	if !strings.Contains(string(logoBytes), "<circle") || !strings.Contains(string(logoBytes), "camera-body") {
		t.Errorf("expected circular camera SVG logo at /logo.svg")
	}

	// Verify dark mode toggle via POST /theme sets SSR data-theme="dark"
	themeResp, err := httpClient.PostForm(appServer.URL+"/theme", url.Values{"theme": {"dark"}, "return_to": {"/"}})
	if err != nil {
		t.Fatalf("POST /theme failed: %v", err)
	}
	themeResp.Body.Close()
	darkHomeResp, err := httpClient.Get(appServer.URL + "/")
	if err != nil {
		t.Fatalf("GET / after dark theme toggle failed: %v", err)
	}
	darkHomeBytes, _ := io.ReadAll(darkHomeResp.Body)
	darkHomeResp.Body.Close()
	if !strings.Contains(string(darkHomeBytes), `data-theme="dark"`) {
		t.Errorf("expected SSR HTML to have data-theme=\"dark\" after toggling dark mode")
	}

	// 10. Verify 3G Thumbnail delivery & caching headers
	thumbURL := appServer.URL + "/blob/" + url.PathEscape(aliceDID) + "/" + photos[0].BlobCID + "?thumb=1"
	thumbResp, err := httpClient.Get(thumbURL)
	if err != nil || thumbResp.StatusCode != http.StatusOK {
		t.Fatalf("GET thumbnail failed: status=%v err=%v", thumbResp, err)
	}
	thumbBytes, _ := io.ReadAll(thumbResp.Body)
	thumbResp.Body.Close()
	if !strings.Contains(thumbResp.Header.Get("Cache-Control"), "immutable") {
		t.Errorf("expected immutable Cache-Control on content-addressed blob endpoint, got %q", thumbResp.Header.Get("Cache-Control"))
	}
	if len(thumbBytes) > 35*1024 {
		t.Errorf("3G thumbnail too large: %d bytes (want < 35KB)", len(thumbBytes))
	}

	// Simulate 3G transfer time for HTML + thumbnail at 750 Kbps (93.75 KB/s) + 150ms RTT
	totalPayloadBytes := len(compressedBytes) + len(thumbBytes)
	simulated3GDuration := 150*time.Millisecond + time.Duration(float64(totalPayloadBytes)/(93750.0)*float64(time.Second)) + elapsedServer
	if simulated3GDuration > 1500*time.Millisecond {
		t.Errorf("simulated 3G load time too slow: %v (payload=%d bytes)", simulated3GDuration, totalPayloadBytes)
	}
	t.Logf("3G Performance: compressed HTML=%d B, thumbnail=%d B, server render=%v, simulated 3G total=%v",
		len(compressedBytes), len(thumbBytes), elapsedServer, simulated3GDuration)
}

type throttled3GConn struct {
	net.Conn
	bytesPerSec int
	rtt         time.Duration
	firstWrite  bool
}

func (c *throttled3GConn) Write(b []byte) (int, error) {
	if !c.firstWrite {
		c.firstWrite = true
		time.Sleep(c.rtt)
	}
	if c.bytesPerSec > 0 && len(b) > 0 {
		delay := time.Duration(float64(len(b)) / float64(c.bytesPerSec) * float64(time.Second))
		time.Sleep(delay)
	}
	return c.Conn.Write(b)
}

type throttled3GListener struct {
	net.Listener
	bytesPerSec int
	rtt         time.Duration
}

func (l *throttled3GListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &throttled3GConn{
		Conn:        c,
		bytesPerSec: l.bytesPerSec,
		rtt:         l.rtt,
	}, nil
}

func TestReal3GThrottledNetworkDelivery(t *testing.T) {
	mockPDS := atproto.NewMockAtmosphereServer()
	defer mockPDS.Close()

	aliceDID := "did:plc:alice3g"
	mockPDS.RegisterActor(aliceDID, "alice.imagos.test")
	blob := mockPDS.SeedBlob(aliceDID, "image/png", makeSamplePNG(1000, 750))

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
	_ = svc.SyncActorFromPDS(context.Background(), aliceDID, "alice.imagos.test", mockPDS.Server.URL)
	_, _, _ = svc.GetOrFetchBlob(context.Background(), aliceDID, blob.CID(), true)

	handler := web.NewServer(svc, web.ServerOptions{
		DemoAccounts: []string{"alice.imagos.test"},
	})

	rawLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	// Regular 3G network profile: 750 Kbps (93,750 bytes/sec) + 100ms RTT
	ln3G := &throttled3GListener{
		Listener:    rawLn,
		bytesPerSec: 93750,
		rtt:         100 * time.Millisecond,
	}
	srv := &http.Server{Handler: handler}
	go func() { _ = srv.Serve(ln3G) }()
	defer srv.Close()

	baseURL := "http://" + rawLn.Addr().String()
	client := &http.Client{Timeout: 10 * time.Second}

	start := time.Now()
	req, _ := http.NewRequest(http.MethodGet, baseURL+"/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET / over 3G listener failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	htmlElapsed := time.Since(start)

	if htmlElapsed > 600*time.Millisecond {
		t.Errorf("3G initial SSR HTML load took %v (want < 600ms, size=%d B)", htmlElapsed, len(body))
	}

	startThumb := time.Now()
	thumbResp, err := client.Get(baseURL + "/blob/" + url.PathEscape(aliceDID) + "/" + blob.CID() + "?thumb=1")
	if err != nil {
		t.Fatalf("GET thumbnail over 3G listener failed: %v", err)
	}
	thumbBody, _ := io.ReadAll(thumbResp.Body)
	thumbResp.Body.Close()
	thumbElapsed := time.Since(startThumb)

	if thumbElapsed > 800*time.Millisecond {
		t.Errorf("3G thumbnail load took %v (want < 800ms, size=%d B)", thumbElapsed, len(thumbBody))
	}
	t.Logf("Real 3G Throttled Socket (750 Kbps, 100ms RTT): SSR HTML (%d B) loaded in %v; Thumbnail (%d B) loaded in %v",
		len(body), htmlElapsed, len(thumbBody), thumbElapsed)
}


