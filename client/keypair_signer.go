package client

import (
	"crypto/ed25519"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// AuthSigner signs HTTP requests with admin credentials.
type AuthSigner interface {
	SignRequest(req *http.Request)
}

// KeypairSigner signs requests with an Ed25519 admin JWT.
type KeypairSigner struct {
	privateKey ed25519.PrivateKey
	keyID      string
	keyName    string
}

// NewKeypairSigner creates a KeypairSigner.
func NewKeypairSigner(privateKey ed25519.PrivateKey, keyID, keyName string) *KeypairSigner {
	return &KeypairSigner{
		privateKey: privateKey,
		keyID:      keyID,
		keyName:    keyName,
	}
}

// SignRequest creates a short-lived admin JWT and sets it as the Authorization header.
func (s *KeypairSigner) SignRequest(req *http.Request) {
	now := time.Now()
	claims := jwt.MapClaims{
		"iss":   "sukko-admin",
		"sub":   s.keyName,
		"roles": []string{"admin"},
		"exp":   jwt.NewNumericDate(now.Add(5 * time.Minute)),
		"iat":   jwt.NewNumericDate(now),
		"jti":   uuid.NewString(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	token.Header["kid"] = s.keyID

	signed, err := token.SignedString(s.privateKey)
	if err != nil {
		return // Ed25519 signing cannot fail with a valid key; unauthenticated request will be rejected by server
	}

	req.Header.Set("Authorization", "Bearer "+signed)
}
