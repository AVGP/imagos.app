package atproto

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"imagos.app/internal/lexicon"
)

const DefaultScope = "atproto transition:generic"

// ClientMetadata represents the ATProto OAuth client-metadata.json document.
type ClientMetadata struct {
	ClientID                string   `json:"client_id"`
	ClientName              string   `json:"client_name"`
	ClientURI               string   `json:"client_uri,omitempty"`
	RedirectURIs            []string `json:"redirect_uris"`
	Scope                   string   `json:"scope"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	ApplicationType         string   `json:"application_type"`
	DPoPBoundAccessTokens   bool     `json:"dpop_bound_access_tokens"`
}

func BuildClientMetadata(clientID, redirectURI string) ClientMetadata {
	u, _ := url.Parse(clientID)
	clientURI := ""
	if u != nil && u.Scheme != "" && u.Host != "" {
		clientURI = u.Scheme + "://" + u.Host
	}
	return ClientMetadata{
		ClientID:                clientID,
		ClientName:              "Imagos Photography App",
		ClientURI:               clientURI,
		RedirectURIs:            []string{redirectURI},
		Scope:                   DefaultScope,
		GrantTypes:              []string{"authorization_code", "refresh_token"},
		ResponseTypes:           []string{"code"},
		TokenEndpointAuthMethod: "none",
		ApplicationType:         "web",
		DPoPBoundAccessTokens:   true,
	}
}

// BuildLoopbackClientID constructs a spec-compliant ATProto loopback OAuth client_id for local development.
func BuildLoopbackClientID(redirectURI string) string {
	v := url.Values{}
	v.Set("redirect_uri", redirectURI)
	v.Set("scope", DefaultScope)
	return "http://127.0.0.1?" + v.Encode()
}

// Identity holds resolved ATProto actor information.
type Identity struct {
	DID    string `json:"did"`
	Handle string `json:"handle"`
	PDSURL string `json:"pdsUrl"`
}

// OAuthPendingRequest holds state for an in-flight OAuth authorization request.
type OAuthPendingRequest struct {
	State         string `json:"state"`
	DID           string `json:"did"`
	Handle        string `json:"handle"`
	PDSURL        string `json:"pdsUrl"`
	AuthServerURL string `json:"authServerUrl"`
	TokenEndpoint string `json:"tokenEndpoint"`
	ClientID      string `json:"clientId,omitempty"`
	RedirectURI   string `json:"redirectUri,omitempty"`
	CodeVerifier  string `json:"codeVerifier"`
	DPoPKeyPEM    string `json:"dpopKeyPem"`
	DPoPNonce     string `json:"dpopNonce"`
	RedirectURL   string `json:"redirectUrl"`
	CreatedAt     int64  `json:"createdAt"`
}

// OAuthSession holds an authenticated user's DPoP-bound ATProto OAuth session.
type OAuthSession struct {
	DID           string    `json:"did"`
	Handle        string    `json:"handle"`
	PDSURL        string    `json:"pdsUrl"`
	AuthServerURL string    `json:"authServerUrl"`
	TokenEndpoint string    `json:"tokenEndpoint"`
	AccessToken   string    `json:"accessToken"`
	RefreshToken  string    `json:"refreshToken"`
	DPoPKeyPEM    string    `json:"dpopKeyPem"`
	ExpiresAt     time.Time `json:"expiresAt"`
}

// RecordItem represents an item returned by com.atproto.repo.listRecords.
type RecordItem struct {
	URI   string          `json:"uri"`
	CID   string          `json:"cid"`
	Value json.RawMessage `json:"value"`
}

type ClientConfig struct {
	ClientID         string
	RedirectURI      string
	PLCURL           string
	DefaultHandleURL string
	DemoPDSURL       string
	HTTPClient       *http.Client
}

// Client handles ATProto Identity resolution, OAuth 2.0 DPoP flows, and XRPC calls to PDS instances.
type Client struct {
	cfg        ClientConfig
	httpClient *http.Client
	nonceMu    sync.RWMutex
	nonces     map[string]string // host -> latest DPoP-Nonce
}

func NewClient(cfg ClientConfig) *Client {
	if cfg.PLCURL == "" {
		cfg.PLCURL = "https://plc.directory"
	}
	if cfg.DefaultHandleURL == "" {
		cfg.DefaultHandleURL = "https://public.api.bsky.app"
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	return &Client{
		cfg:        cfg,
		httpClient: hc,
		nonces:     make(map[string]string),
	}
}

func (c *Client) ClientID() string {
	return c.cfg.ClientID
}

func (c *Client) RedirectURI() string {
	return c.cfg.RedirectURI
}

func (c *Client) getNonce(targetURL string) string {
	u, err := url.Parse(targetURL)
	if err != nil {
		return ""
	}
	c.nonceMu.RLock()
	defer c.nonceMu.RUnlock()
	return c.nonces[u.Host]
}

func (c *Client) setNonce(targetURL, nonce string) {
	if nonce == "" {
		return
	}
	u, err := url.Parse(targetURL)
	if err != nil {
		return
	}
	c.nonceMu.Lock()
	c.nonces[u.Host] = nonce
	c.nonceMu.Unlock()
}

type didDoc struct {
	ID          string   `json:"id"`
	AlsoKnownAs []string `json:"alsoKnownAs"`
	Service     []struct {
		ID              string `json:"id"`
		Type            string `json:"type"`
		ServiceEndpoint string `json:"serviceEndpoint"`
	} `json:"service"`
}

// ResolveIdentity resolves a handle or DID to its canonical DID, handle, and PDS endpoint URL.
func (c *Client) ResolveIdentity(ctx context.Context, identifier string) (*Identity, error) {
	identifier = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(identifier), "@"))
	if identifier == "" {
		return nil, errors.New("identifier (handle or DID) is required")
	}

	var did string
	var initialHandle string
	if strings.HasPrefix(identifier, "did:") {
		did = identifier
	} else {
		initialHandle = strings.ToLower(identifier)
		resolvedDID, err := c.resolveHandleToDID(ctx, initialHandle)
		if err != nil {
			return nil, fmt.Errorf("resolve handle %q: %w", initialHandle, err)
		}
		did = resolvedDID
	}

	doc, err := c.fetchDIDDoc(ctx, did)
	if err != nil {
		return nil, fmt.Errorf("fetch DID doc for %s: %w", did, err)
	}

	pdsURL := ""
	for _, svc := range doc.Service {
		if svc.ID == "#atproto_pds" || strings.HasSuffix(svc.ID, "#atproto_pds") || svc.Type == "AtprotoPersonalDataServer" {
			pdsURL = strings.TrimRight(svc.ServiceEndpoint, "/")
			break
		}
	}
	if pdsURL == "" {
		return nil, fmt.Errorf("no #atproto_pds service endpoint found in DID document for %s", did)
	}

	handle := initialHandle
	for _, aka := range doc.AlsoKnownAs {
		if strings.HasPrefix(aka, "at://") {
			handle = strings.TrimPrefix(aka, "at://")
			break
		}
	}
	if handle == "" {
		handle = did
	}

	return &Identity{
		DID:    did,
		Handle: handle,
		PDSURL: pdsURL,
	}, nil
}

func (c *Client) resolveHandleToDID(ctx context.Context, handle string) (string, error) {
	// 1. Try PLC/Directory XRPC com.atproto.identity.resolveHandle first if PLCURL is configured (great for local/mock + directory)
	bases := []string{c.cfg.PLCURL, c.cfg.DefaultHandleURL}
	if c.cfg.DemoPDSURL != "" && strings.HasSuffix(handle, ".imagos.test") {
		bases = append([]string{c.cfg.DemoPDSURL}, bases...)
	}
	for _, base := range bases {
		if base == "" {
			continue
		}
		u := strings.TrimRight(base, "/") + "/xrpc/com.atproto.identity.resolveHandle?handle=" + url.QueryEscape(handle)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err == nil {
			resp, err := c.httpClient.Do(req)
			if err == nil {
				var out struct {
					DID string `json:"did"`
				}
				decodeErr := json.NewDecoder(resp.Body).Decode(&out)
				resp.Body.Close()
				if resp.StatusCode == http.StatusOK && decodeErr == nil && strings.HasPrefix(out.DID, "did:") {
					return out.DID, nil
				}
			}
		}
	}

	// 2. Try DNS TXT _atproto.<handle>
	txtRecords, err := net.DefaultResolver.LookupTXT(ctx, "_atproto."+handle)
	if err == nil {
		for _, txt := range txtRecords {
			txt = strings.TrimSpace(txt)
			if strings.HasPrefix(txt, "did=") {
				candidate := strings.TrimSpace(strings.TrimPrefix(txt, "did="))
				if strings.HasPrefix(candidate, "did:") {
					return candidate, nil
				}
			}
		}
	}

	// 3. Try HTTPS /.well-known/atproto-did
	wellKnownURL := "https://" + handle + "/.well-known/atproto-did"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, wellKnownURL, nil)
	if err == nil {
		resp, err := c.httpClient.Do(req)
		if err == nil {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			resp.Body.Close()
			candidate := strings.TrimSpace(string(body))
			if resp.StatusCode == http.StatusOK && strings.HasPrefix(candidate, "did:") {
				return candidate, nil
			}
		}
	}

	return "", fmt.Errorf("unable to resolve handle %q via XRPC, DNS TXT, or .well-known/atproto-did", handle)
}

func (c *Client) fetchDIDDoc(ctx context.Context, did string) (*didDoc, error) {
	var docURL string
	switch {
	case strings.HasPrefix(did, "did:plc:"):
		plcBase := c.cfg.PLCURL
		if c.cfg.DemoPDSURL != "" && strings.HasPrefix(did, "did:plc:demo") {
			plcBase = c.cfg.DemoPDSURL
		}
		docURL = strings.TrimRight(plcBase, "/") + "/" + url.PathEscape(did)
	case strings.HasPrefix(did, "did:web:"):
		domain := strings.TrimPrefix(did, "did:web:")
		domain, _ = url.PathUnescape(domain)
		scheme := "https"
		if strings.HasPrefix(domain, "localhost") || strings.HasPrefix(domain, "127.0.0.1") {
			scheme = "http"
		}
		docURL = fmt.Sprintf("%s://%s/.well-known/did.json", scheme, domain)
	default:
		return nil, fmt.Errorf("unsupported DID method: %s", did)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, docURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("DID doc lookup returned status %d", resp.StatusCode)
	}
	var doc didDoc
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return nil, err
	}
	return &doc, nil
}

type protectedResourceMeta struct {
	Resource             string   `json:"resource"`
	AuthorizationServers []string `json:"authorization_servers"`
}

type authServerMeta struct {
	Issuer                             string `json:"issuer"`
	AuthorizationEndpoint              string `json:"authorization_endpoint"`
	TokenEndpoint                      string `json:"token_endpoint"`
	PushedAuthorizationRequestEndpoint string `json:"pushed_authorization_request_endpoint"`
}

func (c *Client) discoverAuthServer(ctx context.Context, pdsURL string) (*authServerMeta, error) {
	prURL := strings.TrimRight(pdsURL, "/") + "/.well-known/oauth-protected-resource"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, prURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	authServerIssuer := strings.TrimRight(pdsURL, "/")
	if resp.StatusCode == http.StatusOK {
		var pr protectedResourceMeta
		if err := json.NewDecoder(resp.Body).Decode(&pr); err == nil && len(pr.AuthorizationServers) > 0 {
			authServerIssuer = strings.TrimRight(pr.AuthorizationServers[0], "/")
		}
	}

	asURL := authServerIssuer + "/.well-known/oauth-authorization-server"
	reqAS, err := http.NewRequestWithContext(ctx, http.MethodGet, asURL, nil)
	if err != nil {
		return nil, err
	}
	respAS, err := c.httpClient.Do(reqAS)
	if err != nil {
		return nil, err
	}
	defer respAS.Body.Close()
	if respAS.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oauth-authorization-server lookup returned %d", respAS.StatusCode)
	}
	var meta authServerMeta
	if err := json.NewDecoder(respAS.Body).Decode(&meta); err != nil {
		return nil, err
	}
	if meta.AuthorizationEndpoint == "" || meta.TokenEndpoint == "" {
		return nil, errors.New("authorization server metadata missing authorization_endpoint or token_endpoint")
	}
	return &meta, nil
}

// ResolveOAuthEndpointsForBaseURL returns the effective client_id and redirect_uri for a given public base URL.
func (c *Client) ResolveOAuthEndpointsForBaseURL(publicBaseURL string) (clientID string, redirectURI string) {
	publicBaseURL = strings.TrimRight(strings.TrimSpace(publicBaseURL), "/")
	if publicBaseURL == "" {
		return c.cfg.ClientID, c.cfg.RedirectURI
	}
	redirectURI = publicBaseURL + "/oauth/callback"
	u, err := url.Parse(publicBaseURL)
	if err == nil {
		host := u.Hostname()
		if (host == "127.0.0.1" || host == "localhost") && strings.HasPrefix(c.cfg.ClientID, "http://127.0.0.1?") {
			return BuildLoopbackClientID(redirectURI), redirectURI
		}
	}
	return publicBaseURL + "/oauth/client-metadata.json", redirectURI
}

// StartOAuth initiates an ATProto OAuth 2.0 flow with PKCE, DPoP, and PAR.
func (c *Client) StartOAuth(ctx context.Context, identifier string) (*OAuthPendingRequest, error) {
	return c.StartOAuthWithBaseURL(ctx, identifier, "")
}

// StartOAuthWithBaseURL initiates an ATProto OAuth 2.0 flow using the given public base URL for client_id and redirect_uri.
func (c *Client) StartOAuthWithBaseURL(ctx context.Context, identifier, publicBaseURL string) (*OAuthPendingRequest, error) {
	ident, err := c.ResolveIdentity(ctx, identifier)
	if err != nil {
		return nil, err
	}

	asMeta, err := c.discoverAuthServer(ctx, ident.PDSURL)
	if err != nil {
		return nil, fmt.Errorf("discover OAuth server for %s: %w", ident.PDSURL, err)
	}

	dpopKey, err := GenerateDPoPKey()
	if err != nil {
		return nil, err
	}
	dpopPEM, err := MarshalDPoPKeyPEM(dpopKey)
	if err != nil {
		return nil, err
	}

	verifier, challenge, err := GeneratePKCE()
	if err != nil {
		return nil, err
	}
	state, err := RandomToken(24)
	if err != nil {
		return nil, err
	}

	clientID, redirectURI := c.ResolveOAuthEndpointsForBaseURL(publicBaseURL)

	form := url.Values{}
	form.Set("client_id", clientID)
	form.Set("redirect_uri", redirectURI)
	form.Set("response_type", "code")
	form.Set("scope", DefaultScope)
	form.Set("state", state)
	form.Set("code_challenge", challenge)
	form.Set("code_challenge_method", "S256")
	form.Set("login_hint", ident.Handle)

	var redirectURL string
	if asMeta.PushedAuthorizationRequestEndpoint != "" {
		respBytes, status, err := c.doDPoPFormPost(ctx, dpopKey, asMeta.PushedAuthorizationRequestEndpoint, form)
		if err != nil {
			return nil, fmt.Errorf("PAR request failed: %w", err)
		}
		if status != http.StatusCreated && status != http.StatusOK {
			return nil, fmt.Errorf("PAR endpoint returned status %d: %s", status, string(respBytes))
		}
		var parOut struct {
			RequestURI string `json:"request_uri"`
		}
		if err := json.Unmarshal(respBytes, &parOut); err != nil || parOut.RequestURI == "" {
			return nil, fmt.Errorf("invalid PAR response: %s", string(respBytes))
		}
		authQuery := url.Values{}
		authQuery.Set("client_id", clientID)
		authQuery.Set("request_uri", parOut.RequestURI)
		redirectURL = asMeta.AuthorizationEndpoint + "?" + authQuery.Encode()
	} else {
		redirectURL = asMeta.AuthorizationEndpoint + "?" + form.Encode()
	}

	return &OAuthPendingRequest{
		State:         state,
		DID:           ident.DID,
		Handle:        ident.Handle,
		PDSURL:        ident.PDSURL,
		AuthServerURL: asMeta.Issuer,
		TokenEndpoint: asMeta.TokenEndpoint,
		ClientID:      clientID,
		RedirectURI:   redirectURI,
		CodeVerifier:  verifier,
		DPoPKeyPEM:    dpopPEM,
		DPoPNonce:     c.getNonce(asMeta.TokenEndpoint),
		RedirectURL:   redirectURL,
		CreatedAt:     time.Now().Unix(),
	}, nil
}

// ExchangeOAuthCode exchanges the authorization code for DPoP-bound tokens at the Authorization Server.
func (c *Client) ExchangeOAuthCode(ctx context.Context, pending *OAuthPendingRequest, code string) (*OAuthSession, error) {
	if pending == nil {
		return nil, errors.New("pending OAuth request is nil")
	}
	if strings.TrimSpace(code) == "" {
		return nil, errors.New("authorization code is empty")
	}

	dpopKey, err := ParseDPoPKeyPEM(pending.DPoPKeyPEM)
	if err != nil {
		return nil, err
	}

	clientID := pending.ClientID
	if clientID == "" {
		clientID = c.cfg.ClientID
	}
	redirectURI := pending.RedirectURI
	if redirectURI == "" {
		redirectURI = c.cfg.RedirectURI
	}

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", clientID)
	form.Set("redirect_uri", redirectURI)
	form.Set("code", code)
	form.Set("code_verifier", pending.CodeVerifier)

	respBytes, status, err := c.doDPoPFormPost(ctx, dpopKey, pending.TokenEndpoint, form)
	if err != nil {
		return nil, fmt.Errorf("token exchange failed: %w", err)
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("token endpoint returned status %d: %s", status, string(respBytes))
	}

	var tokOut struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int    `json:"expires_in"`
		Sub          string `json:"sub"`
	}
	if err := json.Unmarshal(respBytes, &tokOut); err != nil {
		return nil, fmt.Errorf("decode token response: %w", err)
	}
	if tokOut.AccessToken == "" {
		return nil, errors.New("token response missing access_token")
	}
	if pending.DID != "" && tokOut.Sub != "" && tokOut.Sub != pending.DID {
		return nil, fmt.Errorf("OAuth token sub (%s) does not match expected DID (%s)", tokOut.Sub, pending.DID)
	}
	did := pending.DID
	if did == "" {
		did = tokOut.Sub
	}
	expiresIn := tokOut.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 3600
	}

	return &OAuthSession{
		DID:           did,
		Handle:        pending.Handle,
		PDSURL:        pending.PDSURL,
		AuthServerURL: pending.AuthServerURL,
		TokenEndpoint: pending.TokenEndpoint,
		AccessToken:   tokOut.AccessToken,
		RefreshToken:  tokOut.RefreshToken,
		DPoPKeyPEM:    pending.DPoPKeyPEM,
		ExpiresAt:     time.Now().Add(time.Duration(expiresIn) * time.Second),
	}, nil
}

func (c *Client) doDPoPFormPost(ctx context.Context, key *ecdsa.PrivateKey, endpoint string, form url.Values) ([]byte, int, error) {
	for attempt := 0; attempt < 2; attempt++ {
		nonce := c.getNonce(endpoint)
		proof, err := CreateDPoPProof(key, http.MethodPost, endpoint, nonce, "")
		if err != nil {
			return nil, 0, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
		if err != nil {
			return nil, 0, err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("DPoP", proof)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, 0, err
		}
		newNonce := resp.Header.Get("DPoP-Nonce")
		if newNonce != "" {
			c.setNonce(endpoint, newNonce)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if (resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnauthorized) && newNonce != "" && attempt == 0 {
			continue
		}
		return body, resp.StatusCode, nil
	}
	return nil, 0, errors.New("DPoP form post exhausted retries")
}

func (c *Client) doAuthenticatedXRPC(ctx context.Context, session *OAuthSession, method, endpoint, contentType string, bodyBytes []byte) ([]byte, int, error) {
	if session == nil {
		return nil, 0, errors.New("OAuth session is required")
	}
	key, err := ParseDPoPKeyPEM(session.DPoPKeyPEM)
	if err != nil {
		return nil, 0, err
	}

	for attempt := 0; attempt < 2; attempt++ {
		nonce := c.getNonce(endpoint)
		proof, err := CreateDPoPProof(key, method, endpoint, nonce, session.AccessToken)
		if err != nil {
			return nil, 0, err
		}

		var bodyReader io.Reader
		if bodyBytes != nil {
			bodyReader = bytes.NewReader(bodyBytes)
		}
		req, err := http.NewRequestWithContext(ctx, method, endpoint, bodyReader)
		if err != nil {
			return nil, 0, err
		}
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		req.Header.Set("Authorization", "DPoP "+session.AccessToken)
		req.Header.Set("DPoP", proof)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, 0, err
		}
		newNonce := resp.Header.Get("DPoP-Nonce")
		if newNonce != "" {
			c.setNonce(endpoint, newNonce)
		}
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if (resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnauthorized) && newNonce != "" && attempt == 0 {
			continue
		}
		return respBody, resp.StatusCode, nil
	}
	return nil, 0, errors.New("authenticated XRPC exhausted DPoP retries")
}

// UploadBlob uploads an image blob to the user's PDS via com.atproto.repo.uploadBlob.
func (c *Client) UploadBlob(ctx context.Context, session *OAuthSession, mimeType string, data []byte) (lexicon.BlobRef, error) {
	endpoint := strings.TrimRight(session.PDSURL, "/") + "/xrpc/com.atproto.repo.uploadBlob"
	respBytes, status, err := c.doAuthenticatedXRPC(ctx, session, http.MethodPost, endpoint, mimeType, data)
	if err != nil {
		return lexicon.BlobRef{}, err
	}
	if status != http.StatusOK {
		return lexicon.BlobRef{}, fmt.Errorf("uploadBlob returned status %d: %s", status, string(respBytes))
	}
	var out struct {
		Blob lexicon.BlobRef `json:"blob"`
	}
	if err := json.Unmarshal(respBytes, &out); err != nil {
		return lexicon.BlobRef{}, fmt.Errorf("decode uploadBlob response: %w", err)
	}
	return out.Blob, nil
}

// CreateRecord writes a record to the user's PDS via com.atproto.repo.createRecord.
func (c *Client) CreateRecord(ctx context.Context, session *OAuthSession, collection, rkey string, record any) (lexicon.StrongRef, error) {
	endpoint := strings.TrimRight(session.PDSURL, "/") + "/xrpc/com.atproto.repo.createRecord"
	payload := map[string]any{
		"repo":       session.DID,
		"collection": collection,
		"record":     record,
	}
	if rkey != "" {
		payload["rkey"] = rkey
	}
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return lexicon.StrongRef{}, err
	}

	respBytes, status, err := c.doAuthenticatedXRPC(ctx, session, http.MethodPost, endpoint, "application/json", bodyBytes)
	if err != nil {
		return lexicon.StrongRef{}, err
	}
	if status != http.StatusOK {
		return lexicon.StrongRef{}, fmt.Errorf("createRecord (%s) returned status %d: %s", collection, status, string(respBytes))
	}
	var ref lexicon.StrongRef
	if err := json.Unmarshal(respBytes, &ref); err != nil {
		return lexicon.StrongRef{}, fmt.Errorf("decode createRecord response: %w", err)
	}
	return ref, nil
}

// PutRecord updates or creates a record at a known rkey on the user's PDS via com.atproto.repo.putRecord.
func (c *Client) PutRecord(ctx context.Context, session *OAuthSession, collection, rkey string, record any) (lexicon.StrongRef, error) {
	endpoint := strings.TrimRight(session.PDSURL, "/") + "/xrpc/com.atproto.repo.putRecord"
	payload := map[string]any{
		"repo":       session.DID,
		"collection": collection,
		"rkey":       rkey,
		"record":     record,
	}
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return lexicon.StrongRef{}, err
	}

	respBytes, status, err := c.doAuthenticatedXRPC(ctx, session, http.MethodPost, endpoint, "application/json", bodyBytes)
	if err != nil {
		return lexicon.StrongRef{}, err
	}
	if status != http.StatusOK {
		return lexicon.StrongRef{}, fmt.Errorf("putRecord (%s/%s) returned status %d: %s", collection, rkey, status, string(respBytes))
	}
	var ref lexicon.StrongRef
	if err := json.Unmarshal(respBytes, &ref); err != nil {
		return lexicon.StrongRef{}, fmt.Errorf("decode putRecord response: %w", err)
	}
	return ref, nil
}

// DeleteRecord removes a record from the user's PDS via com.atproto.repo.deleteRecord.
func (c *Client) DeleteRecord(ctx context.Context, session *OAuthSession, collection, rkey string) error {
	endpoint := strings.TrimRight(session.PDSURL, "/") + "/xrpc/com.atproto.repo.deleteRecord"
	payload := map[string]any{
		"repo":       session.DID,
		"collection": collection,
		"rkey":       rkey,
	}
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	respBytes, status, err := c.doAuthenticatedXRPC(ctx, session, http.MethodPost, endpoint, "application/json", bodyBytes)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("deleteRecord (%s/%s) returned status %d: %s", collection, rkey, status, string(respBytes))
	}
	return nil
}

// ListRecords fetches records in a collection from any user's PDS via com.atproto.repo.listRecords.
func (c *Client) ListRecords(ctx context.Context, pdsURL, repoDID, collection string, limit int, cursor string) ([]RecordItem, string, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	q := url.Values{}
	q.Set("repo", repoDID)
	q.Set("collection", collection)
	q.Set("limit", fmt.Sprintf("%d", limit))
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	endpoint := strings.TrimRight(pdsURL, "/") + "/xrpc/com.atproto.repo.listRecords?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, "", fmt.Errorf("listRecords returned status %d: %s", resp.StatusCode, string(body))
	}
	var out struct {
		Records []RecordItem `json:"records"`
		Cursor  string       `json:"cursor"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, "", err
	}
	return out.Records, out.Cursor, nil
}

// GetRecord fetches a single record from a user's PDS via com.atproto.repo.getRecord.
func (c *Client) GetRecord(ctx context.Context, pdsURL, repoDID, collection, rkey string) (*RecordItem, error) {
	q := url.Values{}
	q.Set("repo", repoDID)
	q.Set("collection", collection)
	q.Set("rkey", rkey)
	endpoint := strings.TrimRight(pdsURL, "/") + "/xrpc/com.atproto.repo.getRecord?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("getRecord returned status %d: %s", resp.StatusCode, string(body))
	}
	var item RecordItem
	if err := json.NewDecoder(resp.Body).Decode(&item); err != nil {
		return nil, err
	}
	return &item, nil
}

// GetBlob downloads a raw blob from a user's PDS via com.atproto.sync.getBlob.
func (c *Client) GetBlob(ctx context.Context, pdsURL, repoDID, cid string) ([]byte, string, error) {
	q := url.Values{}
	q.Set("did", repoDID)
	q.Set("cid", cid)
	endpoint := strings.TrimRight(pdsURL, "/") + "/xrpc/com.atproto.sync.getBlob?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, "", fmt.Errorf("getBlob returned status %d: %s", resp.StatusCode, string(body))
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 15_000_000))
	if err != nil {
		return nil, "", err
	}
	mimeType := resp.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = "image/jpeg"
	}
	return data, mimeType, nil
}
