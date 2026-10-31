package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"log"
	"math"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/acme/autocert"

	"imagos.app/internal/atproto"
	"imagos.app/internal/lexicon"
	"imagos.app/internal/service"
	"imagos.app/internal/store"
	"imagos.app/internal/web"
)

func main() {
	addr := flag.String("addr", envOr("IMAGOS_ADDR", "0.0.0.0:8080"), "HTTP listen address")
	dbPath := flag.String("db", envOr("IMAGOS_DB", "imagos.db"), "SQLite database path")
	publicURL := flag.String("public-url", envOr("IMAGOS_PUBLIC_URL", ""), "Public base URL (optional; uses ATProto loopback OAuth client_id if empty)")
	acmeDomain := flag.String("acme-domain", envOr("IMAGOS_ACME_DOMAIN", ""), "Enable automatic Let's Encrypt HTTPS on :443/:80 for this domain (requires cap_net_bind_service or root)")
	enableDemo := flag.Bool("demo", envOr("IMAGOS_DEMO", "false") == "true", "Enable local demo Atmosphere PDS accounts (.imagos.test) alongside live Atmosphere OAuth")
	flag.Parse()

	if *acmeDomain != "" && *publicURL == "" {
		*publicURL = "https://" + strings.TrimSpace(*acmeDomain)
	}

	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("failed to open database %s: %v", *dbPath, err)
	}
	defer st.Close()

	baseURL := strings.TrimRight(*publicURL, "/")
	if baseURL == "" {
		host, port, err := net.SplitHostPort(*addr)
		if err != nil {
			host = *addr
			port = "8080"
		}
		if host == "" || host == "0.0.0.0" || host == "::" {
			if pubIP := detectPublicIPv4(); pubIP != "" {
				host = pubIP
			} else {
				host = "127.0.0.1"
			}
		}
		baseURL = "http://" + net.JoinHostPort(host, port)
	}

	redirectURI := baseURL + "/oauth/callback"
	var clientID string
	if *publicURL != "" || (!strings.Contains(baseURL, "127.0.0.1") && !strings.Contains(baseURL, "localhost")) {
		clientID = baseURL + "/oauth/client-metadata.json"
	} else {
		clientID = atproto.BuildLoopbackClientID(redirectURI)
	}

	var demoPDS *atproto.MockAtmosphereServer
	var demoPDSURL string
	var demoHandles []string
	if *enableDemo {
		demoPDS = atproto.NewMockAtmosphereServer()
		defer demoPDS.Close()
		demoPDSURL = demoPDS.Server.URL
		demoHandles = []string{"alice.imagos.test", "bob.imagos.test"}
	}

	atClient := atproto.NewClient(atproto.ClientConfig{
		ClientID:    clientID,
		RedirectURI: redirectURI,
		DemoPDSURL:  demoPDSURL,
	})

	svc := service.New(st, atClient)

	if *enableDemo && demoPDS != nil {
		seedDemoAtmosphere(context.Background(), demoPDS, svc)
	}

	handler := web.NewServer(svc, web.ServerOptions{
		PublicURL:    strings.TrimRight(*publicURL, "/"),
		DemoAccounts: demoHandles,
		MockPDS:      demoPDS,
	})

	if *acmeDomain != "" {
		m := &autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			HostPolicy: autocert.HostWhitelist(strings.TrimSpace(*acmeDomain)),
			Cache:      autocert.DirCache("/home/martin/projects/imagos.app/.autocert"),
		}
		go func() {
			log.Printf("Starting ACME HTTP-01 challenge listener on :80 for %s", *acmeDomain)
			if err := http.ListenAndServe(":80", m.HTTPHandler(nil)); err != nil {
				log.Printf("ACME :80 listener error: %v", err)
			}
		}()
		tlsSrv := &http.Server{
			Addr:      ":443",
			Handler:   handler,
			TLSConfig: m.TLSConfig(),
		}
		log.Printf("Imagos listening with Let's Encrypt HTTPS on https://%s (OAuth callback: %s)", *acmeDomain, redirectURI)
		if err := tlsSrv.ListenAndServeTLS("", ""); err != nil {
			log.Fatalf("HTTPS server error: %v", err)
		}
		return
	}

	log.Printf("Imagos listening on %s (default public URL: %s, OAuth callback: %s)", *addr, baseURL, redirectURI)
	if err := http.ListenAndServe(*addr, handler); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

func detectPublicIPv4() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		ipNet, ok := a.(*net.IPNet)
		if !ok || ipNet.IP.IsLoopback() {
			continue
		}
		ip4 := ipNet.IP.To4()
		if ip4 == nil || ip4.IsPrivate() {
			continue
		}
		return ip4.String()
	}
	return ""
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func generateScenicJPEG(w, h int, pal string) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		fy := float64(y) / float64(h)
		for x := 0; x < w; x++ {
			fx := float64(x) / float64(w)
			var r, g, b uint8
			switch pal {
			case "sunset":
				r = uint8(220 - 90*fy + 25*math.Sin(fx*6))
				g = uint8(95 + 80*(1-fy))
				b = uint8(70 + 110*fy)
			case "forest":
				r = uint8(30 + 40*fy)
				g = uint8(90 + 90*(1-fy) + 20*math.Cos(fx*8))
				b = uint8(60 + 50*fy)
			default:
				r = uint8(40 + 120*fy)
				g = uint8(100 + 80*fx)
				b = uint8(175 - 50*fy)
			}
			// Horizon silhouette
			ridge := 0.62 + 0.08*math.Sin(fx*5.0) + 0.04*math.Cos(fx*13.0)
			if fy > ridge {
				r /= 3
				g /= 3
				b /= 3
			}
			img.Set(x, y, color.RGBA{R: r, G: g, B: b, A: 255})
		}
	}
	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80})
	return buf.Bytes()
}

func seedDemoAtmosphere(ctx context.Context, demoPDS *atproto.MockAtmosphereServer, svc *service.Service) {
	aliceDID := "did:plc:demoalice123456"
	bobDID := "did:plc:demobob78901234"
	demoPDS.RegisterActor(aliceDID, "alice.imagos.test")
	demoPDS.RegisterActor(bobDID, "bob.imagos.test")

	now := time.Now().UTC()

	// Seed tags on Bob's PDS
	tagFilm := lexicon.NewTagRecord("Film", "Analog film photography")
	tagFilmRef := demoPDS.SeedRecord(bobDID, lexicon.NSIDTag, "3ktagfilm0001", tagFilm)

	tagAlpine := lexicon.NewTagRecord("Alpine", "High altitude mountain landscapes")
	tagAlpineRef := demoPDS.SeedRecord(bobDID, lexicon.NSIDTag, "3ktagalpi0002", tagAlpine)

	// Seed two photos on Bob's PDS
	img1 := generateScenicJPEG(960, 640, "sunset")
	blob1 := demoPDS.SeedBlob(bobDID, "image/jpeg", img1)
	photo1 := &lexicon.PhotoRecord{
		Type:        lexicon.NSIDPhoto,
		Title:       "Dolomites Alpenglow",
		Image:       blob1,
		Alt:         "Jagged limestone peaks glowing warm orange under twilight sky",
		Description: "Captured on medium format film just before dusk.",
		AspectRatio: &lexicon.AspectRatio{Width: 960, Height: 640},
		TagRefs:     []lexicon.StrongRef{tagFilmRef, tagAlpineRef},
		Tags:        []string{"film", "alpine"},
		CreatedAt:   now.Add(-2 * time.Hour).Format(time.RFC3339),
	}
	photo1Ref := demoPDS.SeedRecord(bobDID, lexicon.NSIDPhoto, "3kphotobob001", photo1)

	img2 := generateScenicJPEG(960, 640, "forest")
	blob2 := demoPDS.SeedBlob(bobDID, "image/jpeg", img2)
	photo2 := &lexicon.PhotoRecord{
		Type:        lexicon.NSIDPhoto,
		Title:       "Pine Valley Mist",
		Image:       blob2,
		Alt:         "Layered evergreen mountain ridges fading into morning valley fog",
		Description: "Early hike before the clouds lifted.",
		AspectRatio: &lexicon.AspectRatio{Width: 960, Height: 640},
		TagRefs:     []lexicon.StrongRef{tagAlpineRef},
		Tags:        []string{"alpine", "forest"},
		CreatedAt:   now.Add(-90 * time.Minute).Format(time.RFC3339),
	}
	photo2Ref := demoPDS.SeedRecord(bobDID, lexicon.NSIDPhoto, "3kphotobob002", photo2)

	// Seed an Album on Bob's PDS
	album := &lexicon.AlbumRecord{
		Type:        lexicon.NSIDAlbum,
		Name:        "Autumn in the Dolomites",
		Description: "A study of light and elevation across northern Italy.",
		Photos:      []lexicon.StrongRef{photo1Ref, photo2Ref},
		TagRefs:     []lexicon.StrongRef{tagFilmRef, tagAlpineRef},
		Tags:        []string{"film", "alpine"},
		CreatedAt:   now.Add(-60 * time.Minute).Format(time.RFC3339),
	}
	demoPDS.SeedRecord(bobDID, lexicon.NSIDAlbum, "3kalbumbob001", album)

	// Seed a Post on Bob's PDS
	post := &lexicon.PostRecord{
		Type:      lexicon.NSIDPost,
		Text:      "Nothing compares to the last ten minutes of light in the mountains. Here are two frames from the trip.",
		Photos:    []lexicon.StrongRef{photo1Ref, photo2Ref},
		TagRefs:   []lexicon.StrongRef{tagFilmRef, tagAlpineRef},
		Tags:      []string{"film", "alpine"},
		CreatedAt: now.Add(-30 * time.Minute).Format(time.RFC3339),
	}
	demoPDS.SeedRecord(bobDID, lexicon.NSIDPost, "3kpostbob0001", post)

	// Seed a Grain (social.grain.photo + social.grain.gallery + social.grain.gallery.item) on Bob's PDS
	grainPhoto := lexicon.GrainPhotoRecord{
		Type:        lexicon.NSIDGrainPhoto,
		Photo:       blob1,
		Alt:         "Dolomites ridge line shared from Grain.social",
		AspectRatio: &lexicon.AspectRatio{Width: 960, Height: 640},
		CreatedAt:   now.Add(-20 * time.Minute).Format(time.RFC3339),
	}
	grainPhotoRef := demoPDS.SeedRecord(bobDID, lexicon.NSIDGrainPhoto, "3kgrainbob001", grainPhoto)
	grainGallery := lexicon.GrainGalleryRecord{
		Type:        lexicon.NSIDGrainGallery,
		Title:       "Grain.social Portfolio Highlights",
		Description: "Synced automatically from social.grain.gallery on Bob's PDS.",
		CreatedAt:   now.Add(-15 * time.Minute).Format(time.RFC3339),
	}
	grainGalRef := demoPDS.SeedRecord(bobDID, lexicon.NSIDGrainGallery, "3kgraingal001", grainGallery)
	demoPDS.SeedRecord(bobDID, lexicon.NSIDGrainGalleryItem, "3kgrainitm001", lexicon.GrainGalleryItemRecord{
		Type:      lexicon.NSIDGrainGalleryItem,
		Gallery:   grainGalRef.URI,
		Item:      grainPhotoRef.URI,
		Position:  0,
		CreatedAt: now.Add(-14 * time.Minute).Format(time.RFC3339),
	})

	// Seed a native Bluesky photo post (app.bsky.feed.post with app.bsky.embed.images) on Bob's PDS
	bskyPhotoPost := lexicon.BuildBskyPost("Out hiking through the pines this morning — posted from Bluesky with an embedded photo!", []*lexicon.PhotoRecord{photo2})
	bskyPhotoPost.CreatedAt = now.Add(-10 * time.Minute).Format(time.RFC3339)
	demoPDS.SeedRecord(bobDID, lexicon.NSIDBskyPost, "3kbskybob0001", bskyPhotoPost)

	// Seed a Photo on Alice's PDS and a follow from Alice -> Bob
	img3 := generateScenicJPEG(960, 640, "ocean")
	blob3 := demoPDS.SeedBlob(aliceDID, "image/jpeg", img3)
	photo3 := &lexicon.PhotoRecord{
		Type:        lexicon.NSIDPhoto,
		Title:       "Big Sur Horizon",
		Image:       blob3,
		Alt:         "Deep blue Pacific ocean meeting coastal headlands at twilight",
		Description: "35mm frame from Highway 1.",
		AspectRatio: &lexicon.AspectRatio{Width: 960, Height: 640},
		Tags:        []string{"coast", "film"},
		CreatedAt:   now.Add(-45 * time.Minute).Format(time.RFC3339),
	}
	demoPDS.SeedRecord(aliceDID, lexicon.NSIDPhoto, "3kphotoali001", photo3)

	follow := lexicon.NewFollowRecord(bobDID)
	demoPDS.SeedRecord(aliceDID, lexicon.NSIDFollow, "3kfollowab001", follow)

	_ = svc.SyncActorFromPDS(ctx, bobDID, "bob.imagos.test", demoPDS.Server.URL)
	_ = svc.SyncActorFromPDS(ctx, aliceDID, "alice.imagos.test", demoPDS.Server.URL)
	_, _, _ = svc.GetOrFetchBlob(ctx, bobDID, blob1.CID(), true)
	_, _, _ = svc.GetOrFetchBlob(ctx, bobDID, blob2.CID(), true)
	_, _, _ = svc.GetOrFetchBlob(ctx, aliceDID, blob3.CID(), true)
	fmt.Println("Seeded local demo Atmosphere PDS accounts: @alice.imagos.test and @bob.imagos.test")
}
