package atproto_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"imagos.app/internal/atproto"
	"imagos.app/internal/lexicon"
)

func TestDPoPProofGenerationAndVerification(t *testing.T) {
	key, err := atproto.GenerateDPoPKey()
	if err != nil {
		t.Fatalf("failed to generate DPoP key: %v", err)
	}

	proof, err := atproto.CreateDPoPProof(key, "POST", "https://pds.example.com/xrpc/com.atproto.repo.createRecord", "nonce-123", "access-token-xyz")
	if err != nil {
		t.Fatalf("failed to create DPoP proof: %v", err)
	}
	if proof == "" {
		t.Fatal("expected non-empty DPoP JWT")
	}

	claims, err := atproto.VerifyDPoPProofForTest(proof, "POST", "https://pds.example.com/xrpc/com.atproto.repo.createRecord", "nonce-123", "access-token-xyz")
	if err != nil {
		t.Fatalf("DPoP verification failed: %v", err)
	}
	if claims["htm"] != "POST" {
		t.Errorf("expected htm POST, got %v", claims["htm"])
	}
}

func TestOAuthFlowAndPDSRecordOperations(t *testing.T) {
	ctx := context.Background()

	// Spin up a spec-compliant mock Atmosphere PDS + OAuth + PLC server
	mockEnv := atproto.NewMockAtmosphereServer()
	defer mockEnv.Close()

	aliceDID := "did:plc:alice1234567890abc"
	aliceHandle := "alice.imagos.test"
	mockEnv.RegisterActor(aliceDID, aliceHandle)

	client := atproto.NewClient(atproto.ClientConfig{
		ClientID:    "http://localhost/oauth/client-metadata.json",
		RedirectURI: "http://localhost/oauth/callback",
		PLCURL:      mockEnv.Server.URL,
		HTTPClient:  mockEnv.Server.Client(),
	})

	// 1. Resolve handle to DID and PDS URL
	identity, err := client.ResolveIdentity(ctx, aliceHandle)
	if err != nil {
		t.Fatalf("ResolveIdentity failed: %v", err)
	}
	if identity.DID != aliceDID {
		t.Errorf("expected DID %s, got %s", aliceDID, identity.DID)
	}
	if identity.Handle != aliceHandle {
		t.Errorf("expected handle %s, got %s", aliceHandle, identity.Handle)
	}
	if identity.PDSURL != mockEnv.Server.URL {
		t.Errorf("expected PDS URL %s, got %s", mockEnv.Server.URL, identity.PDSURL)
	}

	// 2. Start OAuth flow (includes Protected Resource discovery, Auth Server discovery, DPoP nonce retry, and PAR)
	authReq, err := client.StartOAuth(ctx, aliceHandle)
	if err != nil {
		t.Fatalf("StartOAuth failed: %v", err)
	}
	if authReq.RedirectURL == "" || authReq.State == "" {
		t.Fatalf("expected non-empty RedirectURL and State, got %+v", authReq)
	}

	// 3. Simulate user authorizing at the Authorization Server
	parsedAuthURL, err := url.Parse(authReq.RedirectURL)
	if err != nil {
		t.Fatalf("invalid redirect URL: %v", err)
	}
	requestURI := parsedAuthURL.Query().Get("request_uri")
	if requestURI == "" {
		t.Fatalf("expected request_uri in PAR redirect URL: %s", authReq.RedirectURL)
	}
	code, err := mockEnv.IssueCodeForRequestURI(requestURI, aliceDID)
	if err != nil {
		t.Fatalf("failed to issue auth code: %v", err)
	}

	// 4. Exchange code for DPoP-bound tokens
	session, err := client.ExchangeOAuthCode(ctx, authReq, code)
	if err != nil {
		t.Fatalf("ExchangeOAuthCode failed: %v", err)
	}
	if session.DID != aliceDID {
		t.Errorf("expected session DID %s, got %s", aliceDID, session.DID)
	}
	if session.AccessToken == "" {
		t.Errorf("expected non-empty access token")
	}

	// 5. Upload a photo blob to the user's PDS
	imgBytes := []byte("fake-jpeg-binary-content")
	blobRef, err := client.UploadBlob(ctx, session, "image/jpeg", imgBytes)
	if err != nil {
		t.Fatalf("UploadBlob failed: %v", err)
	}
	if blobRef.CID() == "" {
		t.Fatalf("expected blob CID from UploadBlob")
	}
	if blobRef.Size != int64(len(imgBytes)) {
		t.Errorf("expected blob size %d, got %d", len(imgBytes), blobRef.Size)
	}

	// 6. Create an app.imagos.photo record on the PDS
	photoRec := &lexicon.PhotoRecord{
		Type:      lexicon.NSIDPhoto,
		Title:     "Test Upload",
		Image:     blobRef,
		Alt:       "A test uploaded image",
		Tags:      []string{"test"},
		CreatedAt: "2026-10-31T12:00:00Z",
	}
	rkey := lexicon.NewTID()
	strongRef, err := client.CreateRecord(ctx, session, lexicon.NSIDPhoto, rkey, photoRec)
	if err != nil {
		t.Fatalf("CreateRecord failed: %v", err)
	}
	if strongRef.URI == "" || strongRef.CID == "" {
		t.Fatalf("expected valid strongRef from CreateRecord, got %+v", strongRef)
	}

	// 7. List records from the PDS (public XRPC read)
	records, _, err := client.ListRecords(ctx, session.PDSURL, aliceDID, lexicon.NSIDPhoto, 50, "")
	if err != nil {
		t.Fatalf("ListRecords failed: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 photo record on PDS, got %d", len(records))
	}
	var fetchedPhoto lexicon.PhotoRecord
	if err := json.Unmarshal(records[0].Value, &fetchedPhoto); err != nil {
		t.Fatalf("unmarshal record value failed: %v", err)
	}
	if fetchedPhoto.Title != "Test Upload" {
		t.Errorf("expected photo title 'Test Upload', got %q", fetchedPhoto.Title)
	}

	// 8. Fetch raw blob from PDS
	fetchedBlob, mimeType, err := client.GetBlob(ctx, session.PDSURL, aliceDID, blobRef.CID())
	if err != nil {
		t.Fatalf("GetBlob failed: %v", err)
	}
	if mimeType != "image/jpeg" {
		t.Errorf("expected mimeType image/jpeg, got %s", mimeType)
	}
	if !bytes.Equal(fetchedBlob, imgBytes) {
		t.Errorf("expected blob bytes to match uploaded bytes")
	}

	// 9. Delete record from PDS
	if err := client.DeleteRecord(ctx, session, lexicon.NSIDPhoto, rkey); err != nil {
		t.Fatalf("DeleteRecord failed: %v", err)
	}
	recordsAfter, _, err := client.ListRecords(ctx, session.PDSURL, aliceDID, lexicon.NSIDPhoto, 50, "")
	if err != nil {
		t.Fatalf("ListRecords after delete failed: %v", err)
	}
	if len(recordsAfter) != 0 {
		t.Errorf("expected 0 records after delete, got %d", len(recordsAfter))
	}
}

func TestLoopbackClientMetadata(t *testing.T) {
	meta := atproto.BuildClientMetadata("https://imagos.app/oauth/client-metadata.json", "https://imagos.app/oauth/callback")
	if meta.ClientID != "https://imagos.app/oauth/client-metadata.json" {
		t.Errorf("unexpected client_id: %s", meta.ClientID)
	}
	if !meta.DPoPBoundAccessTokens {
		t.Errorf("expected dpop_bound_access_tokens=true")
	}
	if meta.TokenEndpointAuthMethod != "none" {
		t.Errorf("expected token_endpoint_auth_method=none, got %s", meta.TokenEndpointAuthMethod)
	}
	_ = http.StatusOK
}
