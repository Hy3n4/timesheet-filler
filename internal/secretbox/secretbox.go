// Package secretbox seals small secrets with AES-256-GCM using a versioned keyring.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"golang.org/x/crypto/hkdf"
)

var (
	ErrMalformed         = errors.New("secretbox: malformed sealed value")
	ErrUnknownKeyVersion = errors.New("secretbox: unknown key version")
	ErrDecrypt           = errors.New("secretbox: decryption failed")
)

var versionPattern = regexp.MustCompile(`^v[0-9]+$`)

// Keyring holds one active key (used for sealing) and any number of decrypt-only keys.
type Keyring struct {
	active string
	aeads  map[string]cipher.AEAD
	raw    map[string][]byte
}

// ParseKeyring parses "v2:<b64>,v1:<b64>". The first entry becomes the active key.
// Errors never include key material.
func ParseKeyring(spec string) (*Keyring, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, errors.New("secretbox: no keys configured")
	}
	k := &Keyring{aeads: map[string]cipher.AEAD{}, raw: map[string][]byte{}}
	for i, entry := range strings.Split(spec, ",") {
		version, b64, ok := strings.Cut(strings.TrimSpace(entry), ":")
		if !ok || !versionPattern.MatchString(version) {
			return nil, fmt.Errorf("secretbox: entry %d: expected v<N>:<base64>", i+1)
		}
		if _, dup := k.aeads[version]; dup {
			return nil, fmt.Errorf("secretbox: duplicate key version %s", version)
		}
		material, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
		if err != nil {
			return nil, fmt.Errorf("secretbox: key %s: invalid base64", version)
		}
		if len(material) != 32 {
			return nil, fmt.Errorf("secretbox: key %s: must be 32 bytes, got %d", version, len(material))
		}
		block, err := aes.NewCipher(material)
		if err != nil {
			return nil, fmt.Errorf("secretbox: key %s: %w", version, err)
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, fmt.Errorf("secretbox: key %s: %w", version, err)
		}
		k.aeads[version] = aead
		k.raw[version] = material
		if i == 0 {
			k.active = version
		}
	}
	return k, nil
}

// ActiveVersion returns the version used by Seal.
func (k *Keyring) ActiveVersion() string { return k.active }

// Seal encrypts plaintext with the active key and a fresh random nonce.
func (k *Keyring) Seal(plaintext, aad []byte) (string, error) {
	aead := k.aeads[k.active]
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("secretbox: nonce: %w", err)
	}
	out := aead.Seal(nonce, nonce, plaintext, aad)
	return k.active + ":" + base64.StdEncoding.EncodeToString(out), nil
}

// Open decrypts a value produced by Seal and reports which key version opened it.
func (k *Keyring) Open(sealed string, aad []byte) ([]byte, string, error) {
	version, b64, ok := strings.Cut(sealed, ":")
	if !ok {
		return nil, "", ErrMalformed
	}
	aead, ok := k.aeads[version]
	if !ok {
		return nil, "", ErrUnknownKeyVersion
	}
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, "", ErrMalformed
	}
	ns := aead.NonceSize()
	if len(data) < ns+aead.Overhead() {
		return nil, "", ErrMalformed
	}
	pt, err := aead.Open(nil, data[:ns], data[ns:], aad)
	if err != nil {
		return nil, "", ErrDecrypt
	}
	return pt, version, nil
}

// MACKey derives a purpose-bound HMAC key from the active key. Values keyed
// with it stop verifying after a key rotation, so use it only for short-lived
// data such as emailed login codes.
func (k *Keyring) MACKey(purpose string) []byte {
	out := make([]byte, 32)
	r := hkdf.New(sha256.New, k.raw[k.active], nil, []byte("timesheet-filler/"+purpose))
	if _, err := io.ReadFull(r, out); err != nil {
		panic(fmt.Sprintf("secretbox: hkdf: %v", err))
	}
	return out
}
