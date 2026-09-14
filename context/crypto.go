package context

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"crypto/sha256"
	"io"

	"golang.org/x/crypto/hkdf"
)

const (
	// hkdfSalt is a fixed application-level salt. For CLI-local encryption,
	// the threat model is casual disk exposure, not a targeted key-recovery attack.
	// A fixed salt is acceptable here per RFC 5869 section 3.1.
	hkdfSalt = "sukko-cli-context-v1"
	hkdfInfo = "encrypt"
	keyLen   = 32 // AES-256
	nonceLen = 12 // GCM standard nonce
)

// ErrDecryptFailed is returned when decryption fails (wrong key or corrupted data).
var ErrDecryptFailed = errors.New("decryption failed")

// DeriveKey derives a 256-bit encryption key from OS-specific machine identifiers.
func DeriveKey() ([]byte, error) {
	material, err := machineKeyMaterial()
	if err != nil {
		return nil, fmt.Errorf("machine key material: %w", err)
	}

	hkdfReader := hkdf.New(sha256.New, material, []byte(hkdfSalt), []byte(hkdfInfo))
	key := make([]byte, keyLen)
	if _, err := io.ReadFull(hkdfReader, key); err != nil {
		return nil, fmt.Errorf("hkdf derive: %w", err)
	}
	return key, nil
}

// Encrypt encrypts plaintext using AES-256-GCM and returns base64-encoded nonce+ciphertext.
func Encrypt(key []byte, plaintext string) (string, error) {
	if len(key) != keyLen {
		return "", fmt.Errorf("encrypt: key must be %d bytes, got %d", keyLen, len(key))
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("aes cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("gcm: %w", err)
	}

	nonce := make([]byte, nonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("random nonce: %w", err)
	}

	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// Decrypt decodes base64 input and decrypts using AES-256-GCM.
func Decrypt(key []byte, encoded string) (string, error) {
	if len(key) != keyLen {
		return "", fmt.Errorf("decrypt: key must be %d bytes, got %d", keyLen, len(key))
	}

	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("base64 decode: %w", err)
	}

	if len(data) < nonceLen {
		return "", ErrDecryptFailed
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("aes cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("gcm: %w", err)
	}

	nonce := data[:nonceLen]
	ciphertext := data[nonceLen:]

	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", ErrDecryptFailed
	}

	return string(plaintext), nil
}

// machineKeyMaterial returns OS-specific key material for HKDF derivation.
func machineKeyMaterial() ([]byte, error) {
	switch runtime.GOOS {
	case "darwin":
		return darwinKeyMaterial()
	case "linux":
		return linuxKeyMaterial()
	default:
		return fallbackKeyMaterial()
	}
}

func darwinKeyMaterial() ([]byte, error) {
	out, err := exec.Command("ioreg", "-rd1", "-c", "IOPlatformExpertDevice").Output() //nolint:noctx // one-shot CLI command; no long-running context to propagate
	if err != nil {
		return fallbackKeyMaterial()
	}
	for line := range strings.SplitSeq(string(out), "\n") {
		if strings.Contains(line, "IOPlatformUUID") {
			if _, after, ok := strings.Cut(line, "="); ok {
				uuid := strings.Trim(strings.TrimSpace(after), "\"")
				if uuid != "" {
					return []byte(uuid), nil
				}
			}
		}
	}
	return fallbackKeyMaterial()
}

func linuxKeyMaterial() ([]byte, error) {
	data, err := os.ReadFile("/etc/machine-id")
	if err != nil {
		return fallbackKeyMaterial()
	}
	id := strings.TrimSpace(string(data))
	if id == "" {
		return fallbackKeyMaterial()
	}
	return []byte(id), nil
}

func fallbackKeyMaterial() ([]byte, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("user home dir: %w", err)
	}
	// Use a stable identifier: home directory path + hostname.
	// Hostname failure is non-fatal: home directory alone provides sufficient
	// per-user uniqueness for the CLI's local encryption use case.
	hostname, _ := os.Hostname()
	return []byte(home + ":" + hostname), nil
}
