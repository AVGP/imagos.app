package atproto

import (
	"crypto/sha256"
	"encoding/base32"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"

	"imagos.app/internal/lexicon"
)

type storedRecord struct {
	URI   string
	CID   string
	RKey  string
	Value json.RawMessage
}

type storedBlob struct {
	CID      string
	MimeType string
	Data     []byte
}

type parSession struct {
	RequestURI    string
	ClientID      string
	RedirectURI   string
	State         string
	CodeChallenge string
	LoginHint     string
	Code          string
	AuthorizedDID string
}

// MockAtmosphereServer simulates an ATProto PLC directory, OAuth 2.0 Authorization Server (with PAR & DPoP), and PDS XRPC server.
type MockAtmosphereServer struct {
	Server *httptest.Server

	mu            sync.RWMutex
	handlesToDID  map[string]string
	didsToHandle  map[string]string
	parRequests   map[string]*parSession
	codesToPAR    map[string]*parSession
	tokensToDID   map[string]string
	repos         map[string]map[string]map[string]storedRecord // did -> collection -> rkey -> record
	blobs         map[string]map[string]storedBlob              // did -> cid -> blob
	currentNonce  string
}

func NewMockAtmosphereServer() *MockAtmosphereServer {
	m := &MockAtmosphereServer{
		handlesToDID: make(map[string]string),
		didsToHandle: make(map[string]string),
		parRequests:  make(map[string]*parSession),
		codesToPAR:   make(map[string]*parSession),
		tokensToDID:  make(map[string]string),
		repos:        make(map[string]map[string]map[string]storedRecord),
		blobs:        make(map[string]map[string]storedBlob),
		currentNonce: "dpop-nonce-imagos-1",
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/xrpc/com.atproto.identity.resolveHandle", m.handleResolveHandle)
	mux.HandleFunc("/.well-known/oauth-protected-resource", m.handleProtectedResource)
	mux.HandleFunc("/.well-known/oauth-authorization-server", m.handleAuthServerMeta)
	mux.HandleFunc("/oauth/par", m.handlePAR)
	mux.HandleFunc("/oauth/authorize", m.handleAuthorize)
	mux.HandleFunc("/oauth/token", m.handleToken)
	mux.HandleFunc("/xrpc/com.atproto.repo.uploadBlob", m.handleUploadBlob)
	mux.HandleFunc("/xrpc/com.atproto.repo.createRecord", m.handleCreateRecord)
	mux.HandleFunc("/xrpc/com.atproto.repo.putRecord", m.handlePutRecord)
	mux.HandleFunc("/xrpc/com.atproto.repo.deleteRecord", m.handleDeleteRecord)
	mux.HandleFunc("/xrpc/com.atproto.repo.listRecords", m.handleListRecords)
	mux.HandleFunc("/xrpc/com.atproto.repo.getRecord", m.handleGetRecord)
	mux.HandleFunc("/xrpc/com.atproto.sync.getBlob", m.handleGetBlob)
	mux.HandleFunc("/", m.handleDIDDocOrRoot)

	m.Server = httptest.NewServer(mux)
	return m
}

func (m *MockAtmosphereServer) Close() {
	if m.Server != nil {
		m.Server.Close()
	}
}

func (m *MockAtmosphereServer) RegisterActor(did, handle string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	handle = strings.ToLower(strings.TrimSpace(handle))
	m.handlesToDID[handle] = did
	m.didsToHandle[did] = handle
	if m.repos[did] == nil {
		m.repos[did] = make(map[string]map[string]storedRecord)
	}
	if m.blobs[did] == nil {
		m.blobs[did] = make(map[string]storedBlob)
	}
}

func (m *MockAtmosphereServer) SeedBlob(did, mimeType string, data []byte) lexicon.BlobRef {
	cid := computeCID(data)
	m.mu.Lock()
	if m.blobs[did] == nil {
		m.blobs[did] = make(map[string]storedBlob)
	}
	m.blobs[did][cid] = storedBlob{CID: cid, MimeType: mimeType, Data: data}
	m.mu.Unlock()
	return lexicon.NewBlobRef(cid, mimeType, int64(len(data)))
}

func (m *MockAtmosphereServer) SeedRecord(did, collection, rkey string, record any) lexicon.StrongRef {
	if rkey == "" {
		rkey = lexicon.NewTID()
	}
	raw, _ := json.Marshal(record)
	cid := computeCID(raw)
	uri := lexicon.FormatATURI(did, collection, rkey)

	m.mu.Lock()
	if m.repos[did] == nil {
		m.repos[did] = make(map[string]map[string]storedRecord)
	}
	if m.repos[did][collection] == nil {
		m.repos[did][collection] = make(map[string]storedRecord)
	}
	m.repos[did][collection][rkey] = storedRecord{
		URI:   uri,
		CID:   cid,
		RKey:  rkey,
		Value: raw,
	}
	m.mu.Unlock()
	return lexicon.StrongRef{URI: uri, CID: cid}
}

func computeCID(data []byte) string {
	sum := sha256.Sum256(data)
	enc := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:]))
	return "bafkrei" + enc[:46]
}

func (m *MockAtmosphereServer) handleResolveHandle(w http.ResponseWriter, r *http.Request) {
	handle := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("handle")))
	m.mu.RLock()
	did, ok := m.handlesToDID[handle]
	m.mu.RUnlock()
	if !ok {
		http.Error(w, `{"error":"HandleNotFound"}`, http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"did": did})
}

func (m *MockAtmosphereServer) handleDIDDocOrRoot(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	path, _ = url.PathUnescape(path)
	if !strings.HasPrefix(path, "did:") {
		http.NotFound(w, r)
		return
	}
	m.mu.RLock()
	handle, ok := m.didsToHandle[path]
	m.mu.RUnlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":          path,
		"alsoKnownAs": []string{"at://" + handle},
		"service": []map[string]string{
			{
				"id":              "#atproto_pds",
				"type":            "AtprotoPersonalDataServer",
				"serviceEndpoint": m.Server.URL,
			},
		},
	})
}

func (m *MockAtmosphereServer) handleProtectedResource(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"resource":              m.Server.URL,
		"authorization_servers": []string{m.Server.URL},
	})
}

func (m *MockAtmosphereServer) handleAuthServerMeta(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"issuer":                                m.Server.URL,
		"authorization_endpoint":                m.Server.URL + "/oauth/authorize",
		"token_endpoint":                        m.Server.URL + "/oauth/token",
		"pushed_authorization_request_endpoint": m.Server.URL + "/oauth/par",
		"dpop_signing_alg_values_supported":     []string{"ES256"},
	})
}

func (m *MockAtmosphereServer) verifyRequestDPoP(w http.ResponseWriter, r *http.Request, accessToken string) bool {
	proof := r.Header.Get("DPoP")
	w.Header().Set("DPoP-Nonce", m.currentNonce)
	fullURL := m.Server.URL + r.URL.Path
	if _, err := VerifyDPoPProofForTest(proof, r.Method, fullURL, m.currentNonce, accessToken); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error":             "use_dpop_nonce",
			"error_description": err.Error(),
		})
		return false
	}
	return true
}

func (m *MockAtmosphereServer) handlePAR(w http.ResponseWriter, r *http.Request) {
	if !m.verifyRequestDPoP(w, r, "") {
		return
	}
	_ = r.ParseForm()
	reqURI := "urn:ietf:params:oauth:request_uri:" + lexicon.NewTID()
	ps := &parSession{
		RequestURI:    reqURI,
		ClientID:      r.FormValue("client_id"),
		RedirectURI:   r.FormValue("redirect_uri"),
		State:         r.FormValue("state"),
		CodeChallenge: r.FormValue("code_challenge"),
		LoginHint:     r.FormValue("login_hint"),
	}
	m.mu.Lock()
	m.parRequests[reqURI] = ps
	m.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"request_uri": reqURI,
		"expires_in":  300,
	})
}

func (m *MockAtmosphereServer) IssueCodeForRequestURI(requestURI, did string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ps, ok := m.parRequests[requestURI]
	if !ok {
		return "", fmt.Errorf("unknown request_uri: %s", requestURI)
	}
	code := "authcode-" + lexicon.NewTID()
	ps.Code = code
	ps.AuthorizedDID = did
	m.codesToPAR[code] = ps
	return code, nil
}

func (m *MockAtmosphereServer) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	reqURI := r.URL.Query().Get("request_uri")
	m.mu.RLock()
	ps, ok := m.parRequests[reqURI]
	var did string
	if ok {
		did = m.handlesToDID[strings.ToLower(ps.LoginHint)]
	}
	m.mu.RUnlock()

	if !ok {
		http.Error(w, "invalid request_uri", http.StatusBadRequest)
		return
	}

	if r.Method == http.MethodPost || r.URL.Query().Get("approve") == "1" {
		if did == "" {
			// Register dynamic actor if needed
			did = "did:plc:" + lexicon.NewTID()
			m.RegisterActor(did, ps.LoginHint)
		}
		code, err := m.IssueCodeForRequestURI(reqURI, did)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		redir, _ := url.Parse(ps.RedirectURI)
		q := redir.Query()
		q.Set("code", code)
		q.Set("state", ps.State)
		q.Set("iss", m.Server.URL)
		redir.RawQuery = q.Encode()
		http.Redirect(w, r, redir.String(), http.StatusFound)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = fmt.Fprintf(w, `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Atmosphere OAuth Authorize</title></head><body><main><h1>Authorize Imagos</h1><p>Sign in as <strong>%s</strong> (%s)</p><form method="post"><button type="submit">Approve Access</button></form></main></body></html>`, html.EscapeString(ps.LoginHint), html.EscapeString(did))
}

func (m *MockAtmosphereServer) handleToken(w http.ResponseWriter, r *http.Request) {
	if !m.verifyRequestDPoP(w, r, "") {
		return
	}
	_ = r.ParseForm()
	code := r.FormValue("code")
	m.mu.Lock()
	ps, ok := m.codesToPAR[code]
	if !ok {
		m.mu.Unlock()
		http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
		return
	}
	accessToken := "atp-access-" + lexicon.NewTID()
	refreshToken := "atp-refresh-" + lexicon.NewTID()
	m.tokensToDID[accessToken] = ps.AuthorizedDID
	m.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token":  accessToken,
		"refresh_token": refreshToken,
		"token_type":    "DPoP",
		"expires_in":    3600,
		"sub":           ps.AuthorizedDID,
		"scope":         DefaultScope,
	})
}

func (m *MockAtmosphereServer) authenticatePDSRequest(w http.ResponseWriter, r *http.Request) (string, bool) {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "DPoP ") {
		http.Error(w, `{"error":"AuthMissing"}`, http.StatusUnauthorized)
		return "", false
	}
	token := strings.TrimPrefix(auth, "DPoP ")
	if !m.verifyRequestDPoP(w, r, token) {
		return "", false
	}
	m.mu.RLock()
	did, ok := m.tokensToDID[token]
	m.mu.RUnlock()
	if !ok {
		http.Error(w, `{"error":"InvalidToken"}`, http.StatusUnauthorized)
		return "", false
	}
	return did, true
}

func (m *MockAtmosphereServer) handleUploadBlob(w http.ResponseWriter, r *http.Request) {
	did, ok := m.authenticatePDSRequest(w, r)
	if !ok {
		return
	}
	data, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	mimeType := r.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = "image/jpeg"
	}
	blobRef := m.SeedBlob(did, mimeType, data)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"blob": blobRef})
}

func (m *MockAtmosphereServer) handleCreateRecord(w http.ResponseWriter, r *http.Request) {
	did, ok := m.authenticatePDSRequest(w, r)
	if !ok {
		return
	}
	var body struct {
		Repo       string          `json:"repo"`
		Collection string          `json:"collection"`
		RKey       string          `json:"rkey"`
		Record     json.RawMessage `json:"record"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if body.Repo != "" && body.Repo != did {
		http.Error(w, `{"error":"ForbiddenRepo"}`, http.StatusForbidden)
		return
	}
	ref := m.SeedRecord(did, body.Collection, body.RKey, body.Record)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(ref)
}

func (m *MockAtmosphereServer) handlePutRecord(w http.ResponseWriter, r *http.Request) {
	m.handleCreateRecord(w, r)
}

func (m *MockAtmosphereServer) handleDeleteRecord(w http.ResponseWriter, r *http.Request) {
	did, ok := m.authenticatePDSRequest(w, r)
	if !ok {
		return
	}
	var body struct {
		Repo       string `json:"repo"`
		Collection string `json:"collection"`
		RKey       string `json:"rkey"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	m.mu.Lock()
	if m.repos[did] != nil && m.repos[did][body.Collection] != nil {
		delete(m.repos[did][body.Collection], body.RKey)
	}
	m.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{}`))
}

func (m *MockAtmosphereServer) handleListRecords(w http.ResponseWriter, r *http.Request) {
	repo := r.URL.Query().Get("repo")
	collection := r.URL.Query().Get("collection")
	m.mu.RLock()
	colMap := m.repos[repo][collection]
	items := make([]RecordItem, 0, len(colMap))
	keys := make([]string, 0, len(colMap))
	for k := range colMap {
		keys = append(keys, k)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(keys)))
	for _, k := range keys {
		rec := colMap[k]
		items = append(items, RecordItem{
			URI:   rec.URI,
			CID:   rec.CID,
			Value: rec.Value,
		})
	}
	m.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"records": items,
	})
}

func (m *MockAtmosphereServer) handleGetRecord(w http.ResponseWriter, r *http.Request) {
	repo := r.URL.Query().Get("repo")
	collection := r.URL.Query().Get("collection")
	rkey := r.URL.Query().Get("rkey")

	m.mu.RLock()
	rec, ok := m.repos[repo][collection][rkey]
	m.mu.RUnlock()
	if !ok {
		http.Error(w, `{"error":"RecordNotFound"}`, http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(RecordItem{
		URI:   rec.URI,
		CID:   rec.CID,
		Value: rec.Value,
	})
}

func (m *MockAtmosphereServer) handleGetBlob(w http.ResponseWriter, r *http.Request) {
	did := r.URL.Query().Get("did")
	cid := r.URL.Query().Get("cid")

	m.mu.RLock()
	blob, ok := m.blobs[did][cid]
	m.mu.RUnlock()
	if !ok {
		http.Error(w, `{"error":"BlobNotFound"}`, http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", blob.MimeType)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(blob.Data)
}
