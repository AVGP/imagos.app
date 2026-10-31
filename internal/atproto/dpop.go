package atproto

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"strings"
	"time"
)

// GenerateDPoPKey generates a P-256 ECDSA private key for ATProto OAuth DPoP proofs.
func GenerateDPoPKey() (*ecdsa.PrivateKey, error) {
	return ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
}

// MarshalDPoPKeyPEM serializes a P-256 ECDSA private key to PEM for session storage.
func MarshalDPoPKeyPEM(key *ecdsa.PrivateKey) (string, error) {
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return "", err
	}
	block := &pem.Block{
		Type:  "EC PRIVATE KEY",
		Bytes: der,
	}
	return string(pem.EncodeToMemory(block)), nil
}

// ParseDPoPKeyPEM parses a PEM-encoded P-256 ECDSA private key.
func ParseDPoPKeyPEM(pemStr string) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, errors.New("invalid PEM block for DPoP private key")
	}
	return x509.ParseECPrivateKey(block.Bytes)
}

// GeneratePKCE creates a PKCE code_verifier and S256 code_challenge.
func GeneratePKCE() (verifier string, challenge string, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", err
	}
	verifier = base64.RawURLEncoding.EncodeToString(buf)
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge, nil
}

// RandomToken generates a cryptographically random URL-safe token.
func RandomToken(numBytes int) (string, error) {
	buf := make([]byte, numBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

type jwkECPublic struct {
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

type dpopHeader struct {
	Typ string      `json:"typ"`
	Alg string      `json:"alg"`
	JWK jwkECPublic `json:"jwk"`
}

func normalizeHTU(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

// CreateDPoPProof creates an ES256 signed DPoP JWT proof per RFC 9449 and ATProto OAuth spec.
func CreateDPoPProof(key *ecdsa.PrivateKey, httpMethod, targetURL, nonce, accessToken string) (string, error) {
	if key == nil {
		return "", errors.New("DPoP key is required")
	}
	xBytes := key.PublicKey.X.FillBytes(make([]byte, 32))
	yBytes := key.PublicKey.Y.FillBytes(make([]byte, 32))

	header := dpopHeader{
		Typ: "dpop+jwt",
		Alg: "ES256",
		JWK: jwkECPublic{
			Kty: "EC",
			Crv: "P-256",
			X:   base64.RawURLEncoding.EncodeToString(xBytes),
			Y:   base64.RawURLEncoding.EncodeToString(yBytes),
		},
	}

	jti, err := RandomToken(16)
	if err != nil {
		return "", err
	}

	claims := map[string]any{
		"jti": jti,
		"htm": strings.ToUpper(httpMethod),
		"htu": normalizeHTU(targetURL),
		"iat": time.Now().Unix(),
	}
	if nonce != "" {
		claims["nonce"] = nonce
	}
	if accessToken != "" {
		athSum := sha256.Sum256([]byte(accessToken))
		claims["ath"] = base64.RawURLEncoding.EncodeToString(athSum[:])
	}

	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}

	signingInput := base64.RawURLEncoding.EncodeToString(headerJSON) + "." +
		base64.RawURLEncoding.EncodeToString(claimsJSON)

	digest := sha256.Sum256([]byte(signingInput))
	r, s, err := ecdsa.Sign(rand.Reader, key, digest[:])
	if err != nil {
		return "", err
	}

	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])

	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// VerifyDPoPProofForTest verifies a DPoP JWT signature and claims (used by tests & mock PDS/AuthServer).
func VerifyDPoPProofForTest(jwtStr, expectedMethod, expectedURL, expectedNonce, expectedAccessToken string) (map[string]any, error) {
	parts := strings.Split(jwtStr, ".")
	if len(parts) != 3 {
		return nil, errors.New("invalid DPoP JWT format")
	}

	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("decode header: %w", err)
	}
	var hdr dpopHeader
	if err := json.Unmarshal(headerBytes, &hdr); err != nil {
		return nil, fmt.Errorf("unmarshal header: %w", err)
	}
	if hdr.Typ != "dpop+jwt" || hdr.Alg != "ES256" {
		return nil, fmt.Errorf("invalid DPoP header typ=%s alg=%s", hdr.Typ, hdr.Alg)
	}

	xBytes, err := base64.RawURLEncoding.DecodeString(hdr.JWK.X)
	if err != nil || len(xBytes) != 32 {
		return nil, errors.New("invalid JWK X coordinate")
	}
	yBytes, err := base64.RawURLEncoding.DecodeString(hdr.JWK.Y)
	if err != nil || len(yBytes) != 32 {
		return nil, errors.New("invalid JWK Y coordinate")
	}
	pubKey := &ecdsa.PublicKey{
		Curve: elliptic.P256(),
		X:     new(big.Int).SetBytes(xBytes),
		Y:     new(big.Int).SetBytes(yBytes),
	}

	sigBytes, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(sigBytes) != 64 {
		return nil, errors.New("invalid ES256 signature length")
	}
	r := new(big.Int).SetBytes(sigBytes[:32])
	s := new(big.Int).SetBytes(sigBytes[32:])

	signingInput := parts[0] + "." + parts[1]
	digest := sha256.Sum256([]byte(signingInput))
	if !ecdsa.Verify(pubKey, digest[:], r, s) {
		return nil, errors.New("DPoP signature verification failed")
	}

	claimsBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	var claims map[string]any
	if err := json.Unmarshal(claimsBytes, &claims); err != nil {
		return nil, err
	}

	if expectedMethod != "" && claims["htm"] != strings.ToUpper(expectedMethod) {
		return nil, fmt.Errorf("DPoP htm mismatch: got %v, want %s", claims["htm"], expectedMethod)
	}
	if expectedURL != "" && claims["htu"] != normalizeHTU(expectedURL) {
		return nil, fmt.Errorf("DPoP htu mismatch: got %v, want %s", claims["htu"], normalizeHTU(expectedURL))
	}
	if expectedNonce != "" && claims["nonce"] != expectedNonce {
		return nil, fmt.Errorf("use_dpop_nonce: got %v, want %s", claims["nonce"], expectedNonce)
	}
	if expectedAccessToken != "" {
		athSum := sha256.Sum256([]byte(expectedAccessToken))
		wantAth := base64.RawURLEncoding.EncodeToString(athSum[:])
		if claims["ath"] != wantAth {
			return nil, fmt.Errorf("DPoP ath mismatch: got %v, want %s", claims["ath"], wantAth)
		}
	}

	return claims, nil
}
