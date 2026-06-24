package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/chacha20poly1305"
)

// Happ "crypt5" deep links (happ://crypt5/…) hide a subscription behind a
// ChaCha20-Poly1305 + RSA envelope. The 34 marker→RSA-key pairs below are the
// ones bundled in the Happ app (same set the public decryptors use); decoding is
// otherwise a pure, offline transform. Ported from the reference JS/Rust
// implementations (LeeeeT/happ-decryptor, amurcanov/happ-decrypt-universal).
//
//go:embed happ_keys.json
var happKeysJSON []byte

var happCrypt5Keys map[string]string // marker → base64 PKCS#8 RSA private key

func happKeys() map[string]string {
	if happCrypt5Keys == nil {
		_ = json.Unmarshal(happKeysJSON, &happCrypt5Keys)
	}
	return happCrypt5Keys
}

// isHappLink reports whether s is a Happ deep link we can attempt to decrypt.
func isHappLink(s string) bool {
	return strings.HasPrefix(strings.TrimSpace(s), "happ://crypt5/")
}

// decryptHapp decrypts a happ://crypt5/ link to its underlying plaintext
// (a subscription URL or an inline config list).
func decryptHapp(link string) (string, error) {
	link = strings.TrimSpace(link)
	const prefix = "happ://crypt5/"
	if !strings.HasPrefix(link, prefix) {
		return "", fmt.Errorf("unsupported Happ link (only crypt5 is supported)")
	}
	return decryptCrypt5(link[len(prefix):])
}

func decryptCrypt5(payload string) (string, error) {
	shuffled := happPermute4([]byte(payload))
	if len(shuffled) < 8 {
		return "", fmt.Errorf("crypt5 payload too short")
	}

	marker := string(shuffled[:4]) + string(shuffled[len(shuffled)-4:])
	body := shuffled[4 : len(shuffled)-4]
	if len(body) < 13 {
		return "", fmt.Errorf("crypt5 body too short")
	}

	nonce := body[:12]
	rest := body[12:]

	digits := 0
	for digits < len(rest) && rest[digits] >= '0' && rest[digits] <= '9' {
		digits++
	}
	if digits == 0 {
		return "", fmt.Errorf("crypt5 segment length missing")
	}
	segLen, err := strconv.Atoi(string(rest[:digits]))
	if err != nil {
		return "", fmt.Errorf("crypt5 bad segment length")
	}
	packed := rest[digits:]
	if len(packed) < 1+segLen {
		return "", fmt.Errorf("crypt5 segment truncated")
	}

	cipherSegment := string(packed[1 : 1+segLen]) // ChaCha20-Poly1305 ciphertext (base64)
	rsaSegment := string(packed[1+segLen:])       // RSA-wrapped ChaCha key (base64)

	keyB64, ok := happKeys()[marker]
	if !ok {
		return "", fmt.Errorf("unknown Happ key marker %q", marker)
	}
	priv, err := parsePKCS8RSA(keyB64)
	if err != nil {
		return "", err
	}

	rsaCipher, err := happB64(rsaSegment)
	if err != nil {
		return "", fmt.Errorf("crypt5 rsa segment base64: %w", err)
	}
	rsaPlain, err := rsa.DecryptPKCS1v15(rand.Reader, priv, rsaCipher)
	if err != nil {
		return "", fmt.Errorf("crypt5 rsa decrypt: %w", err)
	}

	chachaKey, err := happB64(string(happSwapPairs(rsaPlain)))
	if err != nil {
		return "", fmt.Errorf("crypt5 chacha key base64: %w", err)
	}
	if len(chachaKey) != chacha20poly1305.KeySize {
		return "", fmt.Errorf("crypt5 chacha key has invalid length %d", len(chachaKey))
	}

	ciphertext, err := happB64(cipherSegment)
	if err != nil {
		return "", fmt.Errorf("crypt5 cipher segment base64: %w", err)
	}
	aead, err := chacha20poly1305.New(chachaKey)
	if err != nil {
		return "", err
	}
	intermediate, err := aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("crypt5 chacha decrypt failed")
	}

	final, err := happB64(string(happSwapPairs(intermediate)))
	if err != nil {
		return "", fmt.Errorf("crypt5 final base64: %w", err)
	}
	return string(final), nil
}

func parsePKCS8RSA(keyB64 string) (*rsa.PrivateKey, error) {
	der, err := base64.StdEncoding.DecodeString(keyB64)
	if err != nil {
		return nil, fmt.Errorf("happ key base64: %w", err)
	}
	key, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, fmt.Errorf("happ key parse: %w", err)
	}
	priv, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("happ key is not RSA")
	}
	return priv, nil
}

// happPermute4 maps every full 4-byte block ABCD → CDAB; trailing 1-3 bytes pass through.
func happPermute4(b []byte) []byte {
	n := len(b) - len(b)%4
	out := make([]byte, 0, len(b))
	for i := 0; i < n; i += 4 {
		out = append(out, b[i+2], b[i+3], b[i], b[i+1])
	}
	return append(out, b[n:]...)
}

// happSwapPairs swaps adjacent byte pairs AB → BA; a trailing odd byte passes through.
func happSwapPairs(b []byte) []byte {
	out := make([]byte, len(b))
	copy(out, b)
	for i := 0; i+1 < len(out); i += 2 {
		out[i], out[i+1] = out[i+1], out[i]
	}
	return out
}

// happB64 decodes base64 tolerant of url-safe alphabet and missing padding.
func happB64(s string) ([]byte, error) {
	s = strings.ReplaceAll(s, "-", "+")
	s = strings.ReplaceAll(s, "_", "/")
	if m := len(s) % 4; m != 0 {
		s += strings.Repeat("=", 4-m)
	}
	return base64.StdEncoding.DecodeString(s)
}
