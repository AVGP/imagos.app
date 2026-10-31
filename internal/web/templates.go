package web

import (
	"html/template"
	"net/url"
	"strings"
)

// BrandLogoSVG is a circular SVG logo featuring an iconic rangefinder/SLR camera silhouette
// with a colorful aperture lens and an ATProto '@' lens reflection at its center.
const BrandLogoSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 48 48" width="42" height="42" role="img" aria-label="Imagos photography logo">
  <defs>
    <linearGradient id="im-bg" x1="0%" y1="0%" x2="100%" y2="100%">
      <stop offset="0%" stop-color="#0ea5e9"/>
      <stop offset="50%" stop-color="#4f46e5"/>
      <stop offset="100%" stop-color="#f43f5e"/>
    </linearGradient>
    <linearGradient id="im-lens" x1="0%" y1="0%" x2="100%" y2="100%">
      <stop offset="0%" stop-color="#0284c7"/>
      <stop offset="100%" stop-color="#1e1b4b"/>
    </linearGradient>
  </defs>
  <!-- Circular outer badge -->
  <circle cx="24" cy="24" r="22.5" fill="url(#im-bg)"/>
  <circle cx="24" cy="24" r="21" fill="none" stroke="#ffffff" stroke-opacity="0.35" stroke-width="1"/>
  <!-- Shutter button & top flash hotshoe -->
  <rect x="12.5" y="12.5" width="4.5" height="2.5" rx="1.2" fill="#fde047"/>
  <!-- Iconic Camera Body with top viewfinder prism -->
  <path class="camera-body" d="M18.5 14 L20.5 11.2 H27.5 L29.5 14 H34.5 A3.5 3.5 0 0 1 38 17.5 V31.5 A3.5 3.5 0 0 1 34.5 35 H13.5 A3.5 3.5 0 0 1 10 31.5 V17.5 A3.5 3.5 0 0 1 13.5 14 Z" fill="#ffffff"/>
  <!-- Warm rangefinder window dot (coral) -->
  <circle cx="33.8" cy="18.2" r="1.6" fill="#f43f5e"/>
  <!-- Camera Lens Barrel -->
  <circle cx="24" cy="24.8" r="8.8" fill="url(#im-lens)" stroke="#e0f2fe" stroke-width="1.5"/>
  <!-- Colorful Aperture Blades hint inside lens -->
  <circle cx="24" cy="24.8" r="6.6" fill="none" stroke="#38bdf8" stroke-opacity="0.45" stroke-width="1" stroke-dasharray="3 2"/>
  <!-- ATProto '@' symbol inside the camera lens -->
  <path d="M26.6 24.8 A2.6 2.6 0 1 1 24 22.2 A2.6 2.6 0 0 1 26.6 24.8 Z" fill="none" stroke="#ffffff" stroke-width="1.75"/>
  <path d="M26.6 22.5 V25.6 C26.6 26.8 27.6 27.4 28.6 26.9 C29.9 26.1 30.4 23.9 29.7 22.1" fill="none" stroke="#38bdf8" stroke-width="1.65" stroke-linecap="round"/>
  <!-- Lens glint -->
  <circle cx="20.8" cy="21.5" r="1.1" fill="#fde047"/>
</svg>`

var funcMap = template.FuncMap{
	"pathEscape": url.PathEscape,
	"logoSVG": func() template.HTML {
		return template.HTML(BrandLogoSVG)
	},
	"shortDID": func(did string) string {
		if len(did) > 22 {
			return did[:14] + "…" + did[len(did)-6:]
		}
		return did
	},
	"shortTime": func(iso string) string {
		if len(iso) >= 16 {
			return strings.Replace(iso[:16], "T", " ", 1) + " UTC"
		}
		return iso
	},
	"bskyWebURL": func(atURI string) string {
		if !strings.HasPrefix(atURI, "at://") {
			return ""
		}
		rest := strings.TrimPrefix(atURI, "at://")
		parts := strings.Split(rest, "/")
		if len(parts) == 3 {
			return "https://bsky.app/profile/" + parts[0] + "/post/" + parts[2]
		}
		return ""
	},
	"isGrainURI": func(atURI string) bool {
		return strings.Contains(atURI, "/social.grain.")
	},
	"isNativeBskyURI": func(atURI string) bool {
		return strings.Contains(atURI, "/app.bsky.feed.post/")
	},
}

var pageTemplates = template.Must(template.New("root").Funcs(funcMap).Parse(`
{{define "head"}}
<!doctype html>
<html lang="en"{{if eq .Theme "dark"}} data-theme="dark"{{else if eq .Theme "light"}} data-theme="light"{{end}}>
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <meta name="description" content="Imagos — A friendly home for your photos, albums, and stories on the Atmosphere">
  <link rel="icon" type="image/svg+xml" href="/logo.svg">
  <title>{{if .Title}}{{.Title}} · Imagos{{else}}Imagos · Photography on the Atmosphere{{end}}</title>
  <style>
    :root {
      --bg: #f3f6fa;
      --surface: #ffffff;
      --surface-alt: #e9eff6;
      --text: #0f172a;
      --text-muted: #475569;
      --border: #d8e2ef;
      --accent: #0284c7;
      --accent-hover: #0369a1;
      --accent-soft: #e0f2fe;
      --coral: #e11d48;
      --coral-soft: #ffe4e6;
      --amber: #0284c7;
      --amber-soft: #e0f2fe;
      --teal: #0d9488;
      --teal-soft: #ccfbf1;
      --bsky: #0284c7;
      --hero-grad: linear-gradient(135deg, #ffffff 0%, #e0f2fe 55%, #ede9fe 100%);
      --success-bg: #ecfdf5;
      --success-text: #065f46;
      --error-bg: #fef2f2;
      --error-text: #991b1b;
      --radius: 18px;
      --radius-sm: 12px;
      --shadow: 0 6px 22px rgba(15, 23, 42, 0.05);
      --max-w: 960px;
    }
    @media (prefers-color-scheme: dark) {
      :root:not([data-theme="light"]) {
        --bg: #0f141c;
        --surface: #171f2c;
        --surface-alt: #212c3d;
        --text: #f1f5f9;
        --text-muted: #94a3b8;
        --border: #2c3a4f;
        --accent: #38bdf8;
        --accent-hover: #7dd3fc;
        --accent-soft: #162a3d;
        --coral: #fb7185;
        --coral-soft: #3b1822;
        --amber: #38bdf8;
        --amber-soft: #162a3d;
        --teal: #2dd4bf;
        --teal-soft: #133330;
        --bsky: #38bdf8;
        --hero-grad: linear-gradient(135deg, #171f2c 0%, #162a3d 60%, #231d3c 100%);
        --success-bg: #064e3b;
        --success-text: #d1fae5;
        --error-bg: #7f1d1d;
        --error-text: #fee2e2;
        --shadow: 0 6px 24px rgba(0, 0, 0, 0.35);
      }
    }
    :root[data-theme="dark"] {
      --bg: #0f141c;
      --surface: #171f2c;
      --surface-alt: #212c3d;
      --text: #f1f5f9;
      --text-muted: #94a3b8;
      --border: #2c3a4f;
      --accent: #38bdf8;
      --accent-hover: #7dd3fc;
      --accent-soft: #162a3d;
      --coral: #fb7185;
      --coral-soft: #3b1822;
      --amber: #38bdf8;
      --amber-soft: #162a3d;
      --teal: #2dd4bf;
      --teal-soft: #133330;
      --bsky: #38bdf8;
      --hero-grad: linear-gradient(135deg, #171f2c 0%, #162a3d 60%, #231d3c 100%);
      --success-bg: #064e3b;
      --success-text: #d1fae5;
      --error-bg: #7f1d1d;
      --error-text: #fee2e2;
      --shadow: 0 6px 24px rgba(0, 0, 0, 0.35);
    }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      font-family: system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
      background: var(--bg);
      color: var(--text);
      line-height: 1.6;
      font-size: 16px;
    }
    a { color: var(--accent); text-decoration: none; }
    a:hover { color: var(--accent-hover); text-decoration: underline; }
    :focus-visible { outline: 3px solid var(--accent); outline-offset: 2px; }
    .skip-link {
      position: absolute;
      left: -9999px;
      top: 0;
      background: var(--accent);
      color: #fff;
      padding: 0.6rem 1rem;
      z-index: 1000;
      font-weight: 600;
      border-radius: 0 0 8px 8px;
    }
    .skip-link:focus { left: 0.5rem; top: 0.5rem; }
    header.site-header {
      background: var(--surface);
      border-bottom: 1px solid var(--border);
      position: sticky;
      top: 0;
      z-index: 50;
      box-shadow: 0 2px 12px rgba(0,0,0,0.03);
    }
    header.site-header::before {
      content: "";
      display: block;
      height: 4px;
      background: linear-gradient(90deg, #0ea5e9 0%, #6366f1 35%, #f43f5e 70%, #f59e0b 100%);
    }
    .header-inner {
      max-width: var(--max-w);
      margin: 0 auto;
      padding: 0.7rem 1.1rem;
      display: flex;
      flex-wrap: wrap;
      align-items: center;
      justify-content: space-between;
      gap: 0.75rem;
    }
    .brand {
      font-size: 1.4rem;
      font-weight: 800;
      text-decoration: none !important;
      color: var(--text) !important;
      display: inline-flex;
      align-items: center;
      gap: 0.65rem;
      letter-spacing: -0.025em;
    }
    .brand-logo {
      display: inline-flex;
      align-items: center;
      justify-content: center;
      border-radius: 50%;
      box-shadow: 0 2px 8px rgba(79, 70, 229, 0.25);
      transition: transform 0.2s ease;
    }
    .brand:hover .brand-logo {
      transform: rotate(-6deg) scale(1.06);
    }
    nav.main-nav {
      display: flex;
      flex-wrap: wrap;
      align-items: center;
      gap: 0.45rem;
    }
    nav.main-nav a, .btn {
      display: inline-flex;
      align-items: center;
      gap: 0.35rem;
      padding: 0.48rem 0.95rem;
      border-radius: 999px;
      text-decoration: none !important;
      font-weight: 600;
      font-size: 0.92rem;
      border: 1px solid transparent;
      cursor: pointer;
      transition: background 0.15s ease, transform 0.1s ease, box-shadow 0.15s ease;
    }
    nav.main-nav a { color: var(--text); }
    nav.main-nav a:hover, nav.main-nav a[aria-current="page"] {
      background: var(--surface-alt);
    }
    .btn-primary {
      background: linear-gradient(135deg, #0284c7 0%, #2563eb 100%);
      color: #ffffff !important;
      border-color: transparent;
      box-shadow: 0 3px 10px rgba(2, 132, 199, 0.28);
    }
    .btn-primary:hover {
      background: linear-gradient(135deg, #0369a1 0%, #1d4ed8 100%);
      transform: translateY(-1px);
    }
    .btn-secondary {
      background: var(--surface-alt);
      color: var(--text) !important;
      border-color: var(--border);
    }
    .btn-secondary:hover { background: var(--border); }
    main#main-content {
      max-width: var(--max-w);
      margin: 0 auto;
      padding: 1.75rem 1.1rem 3.5rem;
    }
    .alert {
      padding: 0.9rem 1.15rem;
      border-radius: var(--radius-sm);
      margin-bottom: 1.35rem;
      font-weight: 500;
      border: 1px solid var(--border);
    }
    .alert-error { background: var(--error-bg); color: var(--error-text); }
    .alert-success { background: var(--success-bg); color: var(--success-text); }
    .card {
      background: var(--surface);
      border: 1px solid var(--border);
      border-radius: var(--radius);
      padding: 1.5rem;
      margin-bottom: 1.4rem;
      box-shadow: var(--shadow);
    }
    .hero-card {
      background: var(--hero-grad);
      border: 1px solid var(--border);
      border-radius: var(--radius);
      padding: 1.85rem;
      margin-bottom: 1.5rem;
      box-shadow: var(--shadow);
    }
    .feed-header {
      display: flex;
      flex-wrap: wrap;
      align-items: center;
      justify-content: space-between;
      gap: 1rem;
      margin-bottom: 1.4rem;
    }
    .tabs {
      display: inline-flex;
      background: var(--surface-alt);
      padding: 0.3rem;
      border-radius: 999px;
      gap: 0.25rem;
      border: 1px solid var(--border);
    }
    .tabs a {
      padding: 0.42rem 1.05rem;
      border-radius: 999px;
      text-decoration: none;
      color: var(--text-muted);
      font-weight: 600;
      font-size: 0.9rem;
    }
    .tabs a.active {
      background: var(--surface);
      color: var(--accent);
      box-shadow: 0 2px 6px rgba(0,0,0,0.08);
    }
    .meta-line {
      display: flex;
      flex-wrap: wrap;
      align-items: center;
      justify-content: space-between;
      gap: 0.5rem;
      font-size: 0.9rem;
      color: var(--text-muted);
      margin-bottom: 0.85rem;
    }
    .kind-badge {
      display: inline-block;
      padding: 0.2rem 0.7rem;
      border-radius: 999px;
      font-size: 0.76rem;
      font-weight: 700;
      background: var(--accent-soft);
      color: var(--accent);
      margin-right: 0.4rem;
    }
    .kind-photo {
      background: var(--coral-soft);
      color: var(--coral);
    }
    .kind-album {
      background: var(--amber-soft);
      color: var(--amber);
    }
    .kind-post {
      background: var(--teal-soft);
      color: var(--teal);
    }
    .bsky-pill {
      display: inline-flex;
      align-items: center;
      gap: 0.25rem;
      font-size: 0.8rem;
      font-weight: 600;
      padding: 0.2rem 0.65rem;
      border-radius: 999px;
      background: var(--accent-soft);
      color: var(--bsky);
      text-decoration: none;
    }
    .photo-grid {
      display: grid;
      grid-template-columns: repeat(auto-fill, minmax(250px, 1fr));
      gap: 1rem;
      margin-top: 0.85rem;
    }
    .photo-figure {
      margin: 0;
      background: var(--surface-alt);
      border-radius: 14px;
      overflow: hidden;
      border: 1px solid var(--border);
      transition: transform 0.15s ease, box-shadow 0.15s ease;
    }
    .photo-figure:hover {
      transform: translateY(-2px);
      box-shadow: 0 6px 16px rgba(0,0,0,0.08);
    }
    .photo-figure img {
      display: block;
      width: 100%;
      height: auto;
      aspect-ratio: 4 / 3;
      object-fit: cover;
      background: var(--surface-alt);
    }
    .photo-figure figcaption {
      padding: 0.65rem 0.85rem;
      font-size: 0.9rem;
    }
    .tag-list {
      display: flex;
      flex-wrap: wrap;
      gap: 0.45rem;
      margin-top: 0.9rem;
    }
    .tag-chip {
      display: inline-block;
      padding: 0.24rem 0.75rem;
      border-radius: 999px;
      background: var(--surface-alt);
      color: var(--text);
      font-size: 0.84rem;
      font-weight: 600;
      text-decoration: none !important;
      border: 1px solid var(--border);
      transition: background 0.15s ease, color 0.15s ease;
    }
    .tag-chip:hover { background: var(--coral-soft); color: var(--coral); border-color: var(--coral); }
    .form-group { margin-bottom: 1.2rem; }
    .form-group label {
      display: block;
      font-weight: 700;
      margin-bottom: 0.35rem;
    }
    .form-hint {
      font-size: 0.86rem;
      color: var(--text-muted);
      margin-top: 0.2rem;
      margin-bottom: 0.4rem;
    }
    input[type="text"], input[type="url"], input[type="search"], textarea, select {
      width: 100%;
      padding: 0.65rem 0.85rem;
      border: 1px solid var(--border);
      border-radius: var(--radius-sm);
      background: var(--surface);
      color: var(--text);
      font-size: 1rem;
      font-family: inherit;
    }
    .checkbox-box {
      display: flex;
      align-items: flex-start;
      gap: 0.7rem;
      padding: 0.95rem 1.05rem;
      border-radius: var(--radius-sm);
      background: var(--accent-soft);
      border: 1px solid var(--border);
      margin: 1.1rem 0;
    }
    .checkbox-box input[type="checkbox"] {
      width: 1.2rem;
      height: 1.2rem;
      margin-top: 0.15rem;
    }
    .photo-selector-grid {
      display: grid;
      grid-template-columns: repeat(auto-fill, minmax(170px, 1fr));
      gap: 0.85rem;
    }
    .photo-select-card {
      display: block;
      border: 2px solid var(--border);
      border-radius: 12px;
      padding: 0.55rem;
      cursor: pointer;
      background: var(--surface);
    }
    .photo-select-card img {
      width: 100%;
      height: 115px;
      object-fit: cover;
      border-radius: 8px;
      display: block;
      margin-bottom: 0.45rem;
    }
    .uri-code {
      font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
      font-size: 0.78rem;
      color: var(--text-muted);
      word-break: break-all;
    }
    footer.site-footer {
      border-top: 1px solid var(--border);
      padding: 1.75rem 1rem;
      text-align: center;
      font-size: 0.88rem;
      color: var(--text-muted);
    }
    .theme-toggle {
      display: inline-flex;
      align-items: center;
      gap: 0.25rem;
      background: var(--surface-alt);
      border: 1px solid var(--border);
      padding: 0.25rem;
      border-radius: 999px;
      margin-top: 0.65rem;
    }
    .theme-btn {
      background: transparent;
      border: none;
      color: var(--text-muted);
      padding: 0.32rem 0.75rem;
      border-radius: 999px;
      font-size: 0.82rem;
      font-weight: 600;
      cursor: pointer;
    }
    .theme-btn.active {
      background: var(--surface);
      color: var(--accent);
      box-shadow: 0 1px 4px rgba(0,0,0,0.1);
    }
  </style>
</head>
<body>
  <a class="skip-link" href="#main-content">Skip to main content</a>
  <header class="site-header" role="banner">
    <div class="header-inner">
      <a href="/" class="brand">
        <span class="brand-logo" aria-hidden="true">{{logoSVG}}</span>
        <span>Imagos</span>
      </a>
      <nav class="main-nav" aria-label="Main navigation">
        <a href="/" {{if eq .Nav "home"}}aria-current="page"{{end}}>Feed</a>
        <a href="/explore" {{if eq .Nav "explore"}}aria-current="page"{{end}}>Explore</a>
        <a href="/tags" {{if eq .Nav "tags"}}aria-current="page"{{end}}>Tags</a>
        {{if .User}}
          <a href="/photos/new" class="btn btn-secondary">+ Photo</a>
          <a href="/albums/new" class="btn btn-secondary">+ Album</a>
          <a href="/posts/new" class="btn btn-primary">+ Post</a>
          <a href="/profile/{{pathEscape .User.Handle}}" {{if eq .Nav "profile"}}aria-current="page"{{end}}>@{{.User.Handle}}</a>
          <form method="post" action="/logout" style="display:inline;margin:0;">
            <button type="submit" class="btn btn-secondary">Logout</button>
          </form>
        {{else}}
          <a href="/login" class="btn btn-primary">Sign In</a>
        {{end}}
      </nav>
    </div>
  </header>
  <main id="main-content" role="main">
    {{if .FlashError}}<div class="alert alert-error" role="alert">{{.FlashError}}</div>{{end}}
    {{if .FlashSuccess}}<div class="alert alert-success" role="status">{{.FlashSuccess}}</div>{{end}}
{{end}}

{{define "foot"}}
  </main>
  <footer class="site-footer" role="contentinfo">
    <div><strong>Imagos</strong> · Photography on the Atmosphere · Built on ATProto</div>
    <form method="post" action="/theme" class="theme-toggle" aria-label="Color theme">
      <input type="hidden" name="return_to" value="{{.CurrentPath}}">
      <button type="submit" name="theme" value="light" class="theme-btn {{if eq .Theme "light"}}active{{end}}" aria-pressed="{{if eq .Theme "light"}}true{{else}}false{{end}}">☀️ Light</button>
      <button type="submit" name="theme" value="auto" class="theme-btn {{if or (eq .Theme "auto") (eq .Theme "")}}active{{end}}" aria-pressed="{{if or (eq .Theme "auto") (eq .Theme "")}}true{{else}}false{{end}}">🖥️ System</button>
      <button type="submit" name="theme" value="dark" class="theme-btn {{if eq .Theme "dark"}}active{{end}}" aria-pressed="{{if eq .Theme "dark"}}true{{else}}false{{end}}">🌙 Dark</button>
    </form>
  </footer>
</body>
</html>
{{end}}

{{define "feed_item"}}
  {{if eq .Kind "post"}}
    <article class="card" aria-label="Post by @{{.Post.AuthorHandle}}">
      <div class="meta-line">
        <div>
          {{if isNativeBskyURI .Post.URI}}
            <span class="kind-badge">🦋 Bluesky Post</span>
          {{else}}
            <span class="kind-badge kind-post">Story</span>
          {{end}}
          <a href="/profile/{{pathEscape .Post.AuthorHandle}}"><strong>@{{.Post.AuthorHandle}}</strong></a>
        </div>
        <div>
          {{if .Post.BskyPostURI}}
            <a class="bsky-pill" href="{{bskyWebURL .Post.BskyPostURI}}" target="_blank" rel="noopener">🦋 {{if isNativeBskyURI .Post.URI}}View on Bluesky{{else}}Shared on Bluesky{{end}}</a>
          {{end}}
          <time datetime="{{.Post.CreatedAt}}">{{shortTime .Post.CreatedAt}}</time>
        </div>
      </div>
      {{if .Post.Text}}
        <p style="font-size: 1.06rem; margin: 0.4rem 0 0.85rem; white-space: pre-wrap;">{{.Post.Text}}</p>
      {{end}}
      <div class="photo-grid">
        {{range .Post.Photos}}
          <figure class="photo-figure">
            <a href="/photos/{{pathEscape .AuthorDID}}/{{pathEscape .RKey}}">
              <img src="/blob/{{pathEscape .AuthorDID}}/{{pathEscape .BlobCID}}?thumb=1"
                   alt="{{.Alt}}"
                   width="{{.Width}}"
                   height="{{.Height}}"
                   loading="lazy"
                   decoding="async">
            </a>
            <figcaption>
              <strong>{{.Title}}</strong>
              {{if .Description}}<div style="color:var(--text-muted);font-size:0.83rem;">{{.Description}}</div>{{end}}
            </figcaption>
          </figure>
        {{end}}
      </div>
      {{if .Post.Tags}}
        <div class="tag-list" aria-label="Tags">
          {{range .Post.Tags}}<a class="tag-chip" href="/tags/{{pathEscape .}}">#{{.}}</a>{{end}}
        </div>
      {{end}}
    </article>
  {{else if eq .Kind "album"}}
    <article class="card" aria-label="Album {{.Album.Name}} by @{{.Album.AuthorHandle}}">
      <div class="meta-line">
        <div>
          {{if isGrainURI .Album.URI}}
            <span class="kind-badge kind-album">🌾 Grain Gallery</span>
          {{else}}
            <span class="kind-badge kind-album">Album</span>
          {{end}}
          <a href="/profile/{{pathEscape .Album.AuthorHandle}}"><strong>@{{.Album.AuthorHandle}}</strong></a>
        </div>
        <div>
          {{if .Album.BskyPostURI}}
            <a class="bsky-pill" href="{{bskyWebURL .Album.BskyPostURI}}" target="_blank" rel="noopener">🦋 Shared on Bluesky</a>
          {{end}}
          <time datetime="{{.Album.CreatedAt}}">{{shortTime .Album.CreatedAt}}</time>
        </div>
      </div>
      <h2 style="margin: 0.2rem 0 0.4rem; font-size: 1.3rem;">
        <a href="/albums/{{pathEscape .Album.AuthorDID}}/{{pathEscape .Album.RKey}}">{{.Album.Name}}</a>
        <span style="font-size:0.86rem;font-weight:500;color:var(--text-muted);">· {{len .Album.Photos}} photos</span>
      </h2>
      {{if .Album.Description}}<p style="margin: 0 0 0.85rem; color: var(--text-muted);">{{.Album.Description}}</p>{{end}}
      <div class="photo-grid">
        {{range .Album.Photos}}
          <figure class="photo-figure">
            <a href="/photos/{{pathEscape .AuthorDID}}/{{pathEscape .RKey}}">
              <img src="/blob/{{pathEscape .AuthorDID}}/{{pathEscape .BlobCID}}?thumb=1"
                   alt="{{.Alt}}"
                   width="{{.Width}}"
                   height="{{.Height}}"
                   loading="lazy"
                   decoding="async">
            </a>
            <figcaption><strong>{{.Title}}</strong></figcaption>
          </figure>
        {{end}}
      </div>
      {{if .Album.Tags}}
        <div class="tag-list" aria-label="Tags">
          {{range .Album.Tags}}<a class="tag-chip" href="/tags/{{pathEscape .}}">#{{.}}</a>{{end}}
        </div>
      {{end}}
    </article>
  {{else if eq .Kind "photo"}}
    <article class="card" aria-label="Photo {{.Photo.Title}} by @{{.Photo.AuthorHandle}}">
      <div class="meta-line">
        <div>
          {{if isGrainURI .Photo.URI}}
            <span class="kind-badge kind-photo">🌾 Grain Photo</span>
          {{else}}
            <span class="kind-badge kind-photo">Photo</span>
          {{end}}
          <a href="/profile/{{pathEscape .Photo.AuthorHandle}}"><strong>@{{.Photo.AuthorHandle}}</strong></a>
        </div>
        <div>
          {{if .Photo.BskyPostURI}}
            <a class="bsky-pill" href="{{bskyWebURL .Photo.BskyPostURI}}" target="_blank" rel="noopener">🦋 Shared on Bluesky</a>
          {{end}}
          <time datetime="{{.Photo.CreatedAt}}">{{shortTime .Photo.CreatedAt}}</time>
        </div>
      </div>
      <h2 style="margin: 0.2rem 0 0.55rem; font-size: 1.25rem;">
        <a href="/photos/{{pathEscape .Photo.AuthorDID}}/{{pathEscape .Photo.RKey}}">{{.Photo.Title}}</a>
      </h2>
      <figure class="photo-figure">
        <a href="/photos/{{pathEscape .Photo.AuthorDID}}/{{pathEscape .Photo.RKey}}">
          <img src="/blob/{{pathEscape .Photo.AuthorDID}}/{{pathEscape .Photo.BlobCID}}?thumb=1"
               alt="{{.Photo.Alt}}"
               width="{{.Photo.Width}}"
               height="{{.Photo.Height}}"
               loading="lazy"
               decoding="async">
        </a>
        <figcaption>
          {{if .Photo.Description}}<div>{{.Photo.Description}}</div>{{else}}<div style="color:var(--text-muted);">{{.Photo.Alt}}</div>{{end}}
        </figcaption>
      </figure>
      {{if .Photo.Tags}}
        <div class="tag-list" aria-label="Tags">
          {{range .Photo.Tags}}<a class="tag-chip" href="/tags/{{pathEscape .}}">#{{.}}</a>{{end}}
        </div>
      {{end}}
    </article>
  {{end}}
{{end}}

{{define "home"}}
{{template "head" .}}
  <div class="feed-header">
    <div>
      <h1 style="margin:0;font-size:1.7rem;letter-spacing:-0.02em;">{{if eq .Nav "explore"}}Explore Photography{{else}}Your Photography Feed{{end}}</h1>
      <p style="margin:0.2rem 0 0;color:var(--text-muted);font-size:0.95rem;">
        Photos, curated albums, and visual stories from across the Atmosphere.
      </p>
    </div>
    <div class="tabs" role="tablist" aria-label="Feed view">
      <a href="/" class="{{if eq .Nav "home"}}active{{end}}">Following</a>
      <a href="/explore" class="{{if eq .Nav "explore"}}active{{end}}">Explore</a>
    </div>
  </div>

  {{if .User}}
    <section class="card" aria-label="Follow a photographer">
      <form method="post" action="/follow" style="display:flex;flex-wrap:wrap;gap:0.65rem;align-items:flex-end;">
        <div style="flex:1;min-width:220px;">
          <label for="follow-handle" style="display:block;font-weight:700;font-size:0.92rem;margin-bottom:0.3rem;">
            Follow a Photographer on the Atmosphere
          </label>
          <input id="follow-handle" type="text" name="handle" placeholder="e.g. bob.imagos.test or handle.bsky.social" required>
        </div>
        <button type="submit" class="btn btn-primary">Follow</button>
      </form>
      {{if .Follows}}
        <div style="margin-top:0.75rem;font-size:0.88rem;color:var(--text-muted);">
          Following ({{len .Follows}}):
          {{range .Follows}}
            <a href="/profile/{{pathEscape .SubjectHandle}}" style="margin-right:0.55rem;font-weight:600;">@{{.SubjectHandle}}</a>
          {{end}}
        </div>
      {{end}}
    </section>
  {{else}}
    <section class="hero-card" aria-label="Welcome to Imagos">
      <h2 style="margin-top:0;font-size:1.45rem;">Share your photography, owned by you.</h2>
      <p style="color:var(--text-muted);max-width:66ch;margin-bottom:1.15rem;">
        Sign in with any Atmosphere (ATProto / Bluesky) account to upload high-resolution photos, curate albums, share multi-photo stories, and optionally cross-post to Bluesky — all stored directly in your Personal Data Server.
      </p>
      <a href="/login" class="btn btn-primary">Sign in with Atmosphere</a>
    </section>
  {{end}}

  {{if .FeedItems}}
    {{range .FeedItems}}
      {{template "feed_item" .}}
    {{end}}
  {{else}}
    <div class="card">
      <p style="margin:0;">Your feed is quiet right now. Upload a <a href="/photos/new">photo</a>, start an <a href="/albums/new">album</a>, share a <a href="/posts/new">post</a>, or browse <a href="/explore">Explore</a>!</p>
    </div>
  {{end}}
{{template "foot" .}}
{{end}}

{{define "login"}}
{{template "head" .}}
  <section class="card" style="max-width:520px;margin:1.5rem auto;">
    <div style="text-align:center;margin-bottom:1.25rem;">
      <div style="display:inline-block;margin-bottom:0.5rem;">{{logoSVG}}</div>
      <h1 style="margin:0;font-size:1.5rem;">Welcome to Imagos</h1>
      <p style="color:var(--text-muted);margin:0.35rem 0 0;">
        Sign in with your Atmosphere handle to connect your Personal Data Server.
      </p>
    </div>
    <form method="post" action="/login">
      <div class="form-group">
        <label for="handle-input">Your Handle or DID</label>
        <div class="form-hint" id="handle-hint">For example: <code>alice.bsky.social</code></div>
        <input id="handle-input" name="handle" type="text" aria-describedby="handle-hint" placeholder="yourname.bsky.social" required autofocus>
      </div>
      <button type="submit" class="btn btn-primary" style="width:100%;justify-content:center;padding:0.75rem;">
        Continue with Atmosphere →
      </button>
    </form>

    {{if .DemoAccounts}}
      <hr style="border:0;border-top:1px solid var(--border);margin:1.5rem 0;">
      <h2 style="font-size:0.95rem;margin-bottom:0.35rem;">Try a Demo Account</h2>
      <p class="form-hint">Explore immediately with a pre-seeded local Atmosphere account:</p>
      <div style="display:flex;flex-wrap:wrap;gap:0.5rem;margin-top:0.5rem;">
        {{range .DemoAccounts}}
          <form method="post" action="/login" style="margin:0;">
            <input type="hidden" name="handle" value="{{.}}">
            <input type="hidden" name="auto_approve_demo" value="1">
            <button type="submit" class="btn btn-secondary">Sign in as @{{.}}</button>
          </form>
        {{end}}
      </div>
    {{end}}
  </section>
{{template "foot" .}}
{{end}}

{{define "photo_new"}}
{{template "head" .}}
  <section class="card">
    <h1 style="margin-top:0;">Upload a Photo</h1>
    <p style="color:var(--text-muted);">
      Upload an individual photo to your personal repository. Once uploaded, you can include it in any of your albums or posts.
    </p>
    <form method="post" action="/photos" enctype="multipart/form-data">
      <div class="form-group">
        <label for="photo-file">Choose Image (JPEG, PNG, WebP, GIF) *</label>
        <input id="photo-file" name="image" type="file" accept="image/jpeg,image/png,image/webp,image/gif" required>
      </div>
      <div class="form-group">
        <label for="photo-title">Title *</label>
        <input id="photo-title" name="title" type="text" maxlength="200" placeholder="e.g. Morning Mist on Lake Bled" required>
      </div>
      <div class="form-group">
        <label for="photo-alt">Alt Text (For Accessibility) *</label>
        <div class="form-hint" id="alt-hint">Briefly describe what is visible in the photo for people using screen readers.</div>
        <textarea id="photo-alt" name="alt" rows="2" maxlength="1000" aria-describedby="alt-hint" placeholder="e.g. A wooden rowboat gliding across calm reflective water toward an island church in morning fog" required></textarea>
      </div>
      <div class="form-group">
        <label for="photo-desc">Story or Camera Notes</label>
        <textarea id="photo-desc" name="description" rows="3" maxlength="2000" placeholder="Share the story behind the shot, camera, lens, or location..."></textarea>
      </div>
      <div class="form-group">
        <label for="photo-tags">Tags</label>
        <div class="form-hint">Comma-separated tags (e.g. <code>landscape, film, 35mm</code>)</div>
        <input id="photo-tags" name="tags" type="text" placeholder="landscape, monochrome, street">
        {{if .ExistingTags}}
          <div class="form-hint" style="margin-top:0.45rem;">Your tags:
            {{range .ExistingTags}}<span class="tag-chip">#{{.Name}}</span> {{end}}
          </div>
        {{end}}
      </div>
      <div class="checkbox-box">
        <input id="photo-bsky" name="post_to_bsky" type="checkbox" value="1">
        <div>
          <label for="photo-bsky" style="margin:0;cursor:pointer;">🦋 Also share on Bluesky</label>
          <div class="form-hint" style="margin:0.15rem 0 0;">Publishes a Bluesky post with this photo and its alt text.</div>
        </div>
      </div>
      <button type="submit" class="btn btn-primary">Upload Photo</button>
    </form>
  </section>
{{template "foot" .}}
{{end}}

{{define "photo_detail"}}
{{template "head" .}}
  <article class="card">
    <div class="meta-line">
      <div>
        <span class="kind-badge kind-photo">Photo</span>
        by <a href="/profile/{{pathEscape .Photo.AuthorHandle}}"><strong>@{{.Photo.AuthorHandle}}</strong></a>
      </div>
      <div>
        {{if .Photo.BskyPostURI}}
          <a class="bsky-pill" href="{{bskyWebURL .Photo.BskyPostURI}}" target="_blank" rel="noopener">🦋 View on Bluesky</a>
        {{end}}
        <time datetime="{{.Photo.CreatedAt}}">{{shortTime .Photo.CreatedAt}}</time>
      </div>
    </div>
    <h1 style="margin:0.2rem 0 0.85rem;">{{.Photo.Title}}</h1>
    <figure class="photo-figure" style="background:#0d1117;">
      <img src="/blob/{{pathEscape .Photo.AuthorDID}}/{{pathEscape .Photo.BlobCID}}"
           alt="{{.Photo.Alt}}"
           width="{{.Photo.Width}}"
           height="{{.Photo.Height}}"
           style="aspect-ratio:auto;max-height:78vh;object-fit:contain;margin:0 auto;">
      <figcaption style="background:var(--surface);">
        {{if .Photo.Description}}<p style="margin:0 0 0.4rem;font-size:1rem;">{{.Photo.Description}}</p>{{end}}
        <div style="color:var(--text-muted);font-size:0.86rem;"><strong>Alt description:</strong> {{.Photo.Alt}}</div>
      </figcaption>
    </figure>
    {{if .Photo.Tags}}
      <div class="tag-list" aria-label="Tags">
        {{range .Photo.Tags}}<a class="tag-chip" href="/tags/{{pathEscape .}}">#{{.}}</a>{{end}}
      </div>
    {{end}}
    <div style="margin-top:1.1rem;padding-top:0.85rem;border-top:1px solid var(--border);display:flex;flex-wrap:wrap;justify-content:space-between;align-items:center;gap:0.5rem;">
      <div class="uri-code">{{.Photo.URI}}</div>
      {{if .User}}
        <div style="display:flex;gap:0.5rem;">
          <a class="btn btn-secondary" href="/posts/new?photo={{pathEscape .Photo.URI}}">Create Post with Photo</a>
          <a class="btn btn-secondary" href="/albums/new?photo={{pathEscape .Photo.URI}}">Add to New Album</a>
        </div>
      {{end}}
    </div>
  </article>
{{template "foot" .}}
{{end}}

{{define "album_new"}}
{{template "head" .}}
  <section class="card">
    <h1 style="margin-top:0;">Create a Photo Album</h1>
    <p style="color:var(--text-muted);">
      Organize your favorite photos into a themed collection with tags.
    </p>
    <form method="post" action="/albums" enctype="multipart/form-data">
      <div class="form-group">
        <label for="album-name">Album Name *</label>
        <input id="album-name" name="name" type="text" maxlength="200" placeholder="e.g. Autumn in Kyoto" required>
      </div>
      <div class="form-group">
        <label for="album-desc">Description</label>
        <textarea id="album-desc" name="description" rows="3" maxlength="2000" placeholder="What connects the photos in this album?"></textarea>
      </div>
      <div class="form-group">
        <label>Choose Photos from Your Library</label>
        {{if .UserPhotos}}
          <div class="photo-selector-grid">
            {{range .UserPhotos}}
              <label class="photo-select-card">
                <img src="/blob/{{pathEscape .AuthorDID}}/{{pathEscape .BlobCID}}?thumb=1" alt="{{.Alt}}" loading="lazy">
                <input type="checkbox" name="photo_uris" value="{{.URI}}" {{if eq $.PreselectURI .URI}}checked{{end}}>
                <strong>{{.Title}}</strong>
              </label>
            {{end}}
          </div>
        {{else}}
          <p class="form-hint">You haven't uploaded any photos yet — upload one below or <a href="/photos/new">upload photos first</a>.</p>
        {{end}}
      </div>
      <div class="form-group">
        <label for="album-inline-file">Or Upload a New Photo into this Album (Optional)</label>
        <input id="album-inline-file" name="new_image" type="file" accept="image/jpeg,image/png,image/webp,image/gif">
        <div style="display:grid;grid-template-columns:1fr 1fr;gap:0.5rem;margin-top:0.5rem;">
          <input name="new_image_title" type="text" placeholder="Photo title (if uploading)">
          <input name="new_image_alt" type="text" placeholder="Accessible alt text (if uploading)">
        </div>
      </div>
      <div class="form-group">
        <label for="album-tags">Tags</label>
        <input id="album-tags" name="tags" type="text" placeholder="travel, kyoto, film">
      </div>
      <div class="checkbox-box">
        <input id="album-bsky" name="post_to_bsky" type="checkbox" value="1">
        <div>
          <label for="album-bsky" style="margin:0;cursor:pointer;">🦋 Also share this album on Bluesky</label>
          <div class="form-hint" style="margin:0.15rem 0 0;">Posts your album title, description, and up to 4 preview photos to Bluesky.</div>
        </div>
      </div>
      <button type="submit" class="btn btn-primary">Create Album</button>
    </form>
  </section>
{{template "foot" .}}
{{end}}

{{define "album_detail"}}
{{template "head" .}}
  <article class="card">
    <div class="meta-line">
      <div>
        <span class="kind-badge kind-album">Album</span>
        by <a href="/profile/{{pathEscape .Album.AuthorHandle}}"><strong>@{{.Album.AuthorHandle}}</strong></a>
      </div>
      <div>
        {{if .Album.BskyPostURI}}
          <a class="bsky-pill" href="{{bskyWebURL .Album.BskyPostURI}}" target="_blank" rel="noopener">🦋 View on Bluesky</a>
        {{end}}
        <time datetime="{{.Album.CreatedAt}}">{{shortTime .Album.CreatedAt}}</time>
      </div>
    </div>
    <h1 style="margin:0.2rem 0 0.4rem;">{{.Album.Name}}</h1>
    {{if .Album.Description}}<p style="color:var(--text-muted);margin-top:0;">{{.Album.Description}}</p>{{end}}
    <div class="photo-grid">
      {{range .Album.Photos}}
        <figure class="photo-figure">
          <a href="/photos/{{pathEscape .AuthorDID}}/{{pathEscape .RKey}}">
            <img src="/blob/{{pathEscape .AuthorDID}}/{{pathEscape .BlobCID}}?thumb=1"
                 alt="{{.Alt}}"
                 width="{{.Width}}"
                 height="{{.Height}}"
                 loading="lazy"
                 decoding="async">
          </a>
          <figcaption>
            <strong>{{.Title}}</strong>
            <div style="font-size:0.82rem;color:var(--text-muted);">{{.Alt}}</div>
          </figcaption>
        </figure>
      {{end}}
    </div>
    {{if .Album.Tags}}
      <div class="tag-list" aria-label="Tags">
        {{range .Album.Tags}}<a class="tag-chip" href="/tags/{{pathEscape .}}">#{{.}}</a>{{end}}
      </div>
    {{end}}
  </article>
{{template "foot" .}}
{{end}}

{{define "post_new"}}
{{template "head" .}}
  <section class="card">
    <h1 style="margin-top:0;">New Photo Post</h1>
    <p style="color:var(--text-muted);">
      Share a story with one or more photos from your library (or upload a fresh photo below).
    </p>
    <form method="post" action="/posts" enctype="multipart/form-data">
      <div class="form-group">
        <label for="post-text">Your Story or Caption *</label>
        <textarea id="post-text" name="text" rows="4" maxlength="3000" placeholder="What moment are you sharing?" required></textarea>
      </div>
      <div class="form-group">
        <label>Select One or More Photos</label>
        {{if .UserPhotos}}
          <div class="photo-selector-grid">
            {{range .UserPhotos}}
              <label class="photo-select-card">
                <img src="/blob/{{pathEscape .AuthorDID}}/{{pathEscape .BlobCID}}?thumb=1" alt="{{.Alt}}" loading="lazy">
                <input type="checkbox" name="photo_uris" value="{{.URI}}" {{if eq $.PreselectURI .URI}}checked{{end}}>
                <strong>{{.Title}}</strong>
              </label>
            {{end}}
          </div>
        {{else}}
          <p class="form-hint">No photos in your library yet — upload one right below!</p>
        {{end}}
      </div>
      <div class="form-group">
        <label for="post-inline-file">Or Upload a New Photo to Attach (Optional)</label>
        <input id="post-inline-file" name="new_image" type="file" accept="image/jpeg,image/png,image/webp,image/gif">
        <div style="display:grid;grid-template-columns:1fr 1fr;gap:0.5rem;margin-top:0.5rem;">
          <input name="new_image_title" type="text" placeholder="Photo title (if uploading)">
          <input name="new_image_alt" type="text" placeholder="Accessible alt text (if uploading)">
        </div>
      </div>
      <div class="form-group">
        <label for="post-tags">Tags</label>
        <input id="post-tags" name="tags" type="text" placeholder="street, night, portrait">
      </div>
      <div class="checkbox-box">
        <input id="post-bsky" name="post_to_bsky" type="checkbox" value="1">
        <div>
          <label for="post-bsky" style="margin:0;cursor:pointer;">🦋 Also share as a Bluesky post</label>
          <div class="form-hint" style="margin:0.15rem 0 0;">Cross-posts your text and embeds up to 4 selected photos on Bluesky.</div>
        </div>
      </div>
      <button type="submit" class="btn btn-primary">Publish Post</button>
    </form>
  </section>
{{template "foot" .}}
{{end}}

{{define "tags"}}
{{template "head" .}}
  <div class="feed-header">
    <div>
      <h1 style="margin:0;">Explore by Tag</h1>
      <p style="margin:0.2rem 0 0;color:var(--text-muted);">Discover photos, albums, and stories organized by topic.</p>
    </div>
  </div>

  {{if .User}}
    <section class="card" aria-label="Create a new tag">
      <h2 style="margin-top:0;font-size:1.1rem;">Create a New Tag</h2>
      <form method="post" action="/tags" style="display:flex;flex-wrap:wrap;gap:0.65rem;align-items:flex-end;">
        <div style="flex:1;min-width:180px;">
          <label for="tag-name">Tag Name *</label>
          <input id="tag-name" name="name" type="text" placeholder="e.g. Brutalism" required>
        </div>
        <div style="flex:2;min-width:240px;">
          <label for="tag-desc">Description</label>
          <input id="tag-desc" name="description" type="text" placeholder="Concrete architectural photography">
        </div>
        <button type="submit" class="btn btn-primary">Create Tag</button>
      </form>
    </section>
  {{end}}

  <section class="card">
    <h2 style="margin-top:0;font-size:1.15rem;">Popular Tags</h2>
    {{if .Tags}}
      <div class="tag-list">
        {{range .Tags}}
          <a class="tag-chip" href="/tags/{{pathEscape .Slug}}" title="{{.Description}} (by @{{.AuthorHandle}})">
            #{{.Name}} <span style="opacity:0.65;">(@{{.AuthorHandle}})</span>
          </a>
        {{end}}
      </div>
    {{else}}
      <p style="color:var(--text-muted);margin:0;">No tags created yet.</p>
    {{end}}
  </section>

  {{if .TagResults}}
    <section>
      <h2>Tagged with <code>#{{.TagResults.Slug}}</code></h2>
      {{range .TagResults.Posts}}
        <article class="card">
          <div class="meta-line">
            <span><span class="kind-badge kind-post">Story</span> by <a href="/profile/{{pathEscape .AuthorHandle}}">@{{.AuthorHandle}}</a></span>
            <time datetime="{{.CreatedAt}}">{{shortTime .CreatedAt}}</time>
          </div>
          <p>{{.Text}}</p>
          <div class="photo-grid">
            {{range .Photos}}
              <figure class="photo-figure">
                <a href="/photos/{{pathEscape .AuthorDID}}/{{pathEscape .RKey}}">
                  <img src="/blob/{{pathEscape .AuthorDID}}/{{pathEscape .BlobCID}}?thumb=1" alt="{{.Alt}}" width="{{.Width}}" height="{{.Height}}" loading="lazy">
                </a>
                <figcaption><strong>{{.Title}}</strong></figcaption>
              </figure>
            {{end}}
          </div>
        </article>
      {{end}}
      {{range .TagResults.Albums}}
        <article class="card">
          <div class="meta-line">
            <span><span class="kind-badge kind-album">Album</span> by <a href="/profile/{{pathEscape .AuthorHandle}}">@{{.AuthorHandle}}</a></span>
            <time datetime="{{.CreatedAt}}">{{shortTime .CreatedAt}}</time>
          </div>
          <h3><a href="/albums/{{pathEscape .AuthorDID}}/{{pathEscape .RKey}}">{{.Name}}</a></h3>
          <p>{{.Description}}</p>
        </article>
      {{end}}
      {{if .TagResults.Photos}}
        <h3>Photos tagged #{{.TagResults.Slug}}</h3>
        <div class="photo-grid">
          {{range .TagResults.Photos}}
            <figure class="photo-figure">
              <a href="/photos/{{pathEscape .AuthorDID}}/{{pathEscape .RKey}}">
                <img src="/blob/{{pathEscape .AuthorDID}}/{{pathEscape .BlobCID}}?thumb=1" alt="{{.Alt}}" width="{{.Width}}" height="{{.Height}}" loading="lazy">
              </a>
              <figcaption><strong>{{.Title}}</strong> by @{{.AuthorHandle}}</figcaption>
            </figure>
          {{end}}
        </div>
      {{end}}
    </section>
  {{end}}
{{template "foot" .}}
{{end}}

{{define "profile"}}
{{template "head" .}}
  <section class="hero-card">
    <div style="display:flex;flex-wrap:wrap;justify-content:space-between;align-items:flex-start;gap:1rem;">
      <div>
        <h1 style="margin:0;">@{{.ProfileActor.Handle}}</h1>
        <div class="uri-code" style="margin-top:0.25rem;">{{.ProfileActor.DID}}</div>
        <div style="margin-top:0.65rem;font-size:0.95rem;">
          <strong>{{.FollowerCount}}</strong> followers ·
          <strong>{{len .Photos}}</strong> photos ·
          <strong>{{len .Albums}}</strong> albums ·
          <strong>{{len .Posts}}</strong> posts
        </div>
      </div>
      <div style="display:flex;gap:0.5rem;align-items:center;">
        {{if and .User (ne .User.DID .ProfileActor.DID)}}
          {{if .IsFollowing}}
            <form method="post" action="/unfollow" style="margin:0;">
              <input type="hidden" name="subject_did" value="{{.ProfileActor.DID}}">
              <button type="submit" class="btn btn-secondary">Following (Unfollow)</button>
            </form>
          {{else}}
            <form method="post" action="/follow" style="margin:0;">
              <input type="hidden" name="handle" value="{{.ProfileActor.Handle}}">
              <button type="submit" class="btn btn-primary">Follow @{{.ProfileActor.Handle}}</button>
            </form>
          {{end}}
        {{end}}
        <form method="post" action="/profile/{{pathEscape .ProfileActor.Handle}}/sync" style="margin:0;">
          <button type="submit" class="btn btn-secondary">↻ Refresh from PDS</button>
        </form>
      </div>
    </div>
  </section>

  {{if .Albums}}
    <h2>Albums</h2>
    {{range .Albums}}
      <article class="card">
        <div class="meta-line">
          <strong><a href="/albums/{{pathEscape .AuthorDID}}/{{pathEscape .RKey}}">{{.Name}}</a> ({{len .Photos}} photos)</strong>
          <time datetime="{{.CreatedAt}}">{{shortTime .CreatedAt}}</time>
        </div>
        {{if .Description}}<p style="margin:0 0 0.5rem;color:var(--text-muted);">{{.Description}}</p>{{end}}
        <div class="photo-grid">
          {{range .Photos}}
            <figure class="photo-figure">
              <a href="/photos/{{pathEscape .AuthorDID}}/{{pathEscape .RKey}}">
                <img src="/blob/{{pathEscape .AuthorDID}}/{{pathEscape .BlobCID}}?thumb=1" alt="{{.Alt}}" width="{{.Width}}" height="{{.Height}}" loading="lazy">
              </a>
              <figcaption>{{.Title}}</figcaption>
            </figure>
          {{end}}
        </div>
      </article>
    {{end}}
  {{end}}

  {{if .Posts}}
    <h2>Stories</h2>
    {{range .Posts}}
      <article class="card">
        <div class="meta-line">
          <span>@{{.AuthorHandle}}</span>
          <time datetime="{{.CreatedAt}}">{{shortTime .CreatedAt}}</time>
        </div>
        <p>{{.Text}}</p>
        <div class="photo-grid">
          {{range .Photos}}
            <figure class="photo-figure">
              <a href="/photos/{{pathEscape .AuthorDID}}/{{pathEscape .RKey}}">
                <img src="/blob/{{pathEscape .AuthorDID}}/{{pathEscape .BlobCID}}?thumb=1" alt="{{.Alt}}" width="{{.Width}}" height="{{.Height}}" loading="lazy">
              </a>
              <figcaption>{{.Title}}</figcaption>
            </figure>
          {{end}}
        </div>
      </article>
    {{end}}
  {{end}}

  <h2>Photos</h2>
  {{if .Photos}}
    <div class="photo-grid">
      {{range .Photos}}
        <figure class="photo-figure">
          <a href="/photos/{{pathEscape .AuthorDID}}/{{pathEscape .RKey}}">
            <img src="/blob/{{pathEscape .AuthorDID}}/{{pathEscape .BlobCID}}?thumb=1" alt="{{.Alt}}" width="{{.Width}}" height="{{.Height}}" loading="lazy">
          </a>
          <figcaption>
            <strong><a href="/photos/{{pathEscape .AuthorDID}}/{{pathEscape .RKey}}">{{.Title}}</a></strong>
            <div style="font-size:0.82rem;color:var(--text-muted);">{{.Alt}}</div>
          </figcaption>
        </figure>
      {{end}}
    </div>
  {{else}}
    <p style="color:var(--text-muted);">No uploaded photos yet.</p>
  {{end}}
{{template "foot" .}}
{{end}}
`))
