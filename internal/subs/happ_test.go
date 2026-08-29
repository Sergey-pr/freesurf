package subs

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/chacha20poly1305"
)

// TestDecryptHappLink decrypts a real happ://crypt5/ link read from a file (path
// in FREESURF_HAPP_FILE) so the subscription stays out of the repo.
//
//	FREESURF_HAPP_FILE=/tmp/happ_link.txt go test -run TestDecryptHappLink -v
func TestDecryptHappLink(t *testing.T) {
	path := os.Getenv("FREESURF_HAPP_FILE")
	if path == "" {
		t.Skip("set FREESURF_HAPP_FILE to a file containing a happ://crypt5/ link")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := decryptHapp(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("decrypt failed: %v", err)
	}
	t.Logf("decrypted (%d bytes):\n%s", len(plain), plain)
}

// TestHappEndToEnd decrypts the link, fetches the subscription and parses nodes.
//
//	FREESURF_HAPP_FILE=/tmp/happ_link.txt go test -run TestHappEndToEnd -v
func TestHappEndToEnd(t *testing.T) {
	path := os.Getenv("FREESURF_HAPP_FILE")
	if path == "" {
		t.Skip("set FREESURF_HAPP_FILE to a file containing a happ://crypt5/ link")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	parsed, err := BuildImport(ctx, strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("BuildImport: %v", err)
	}
	t.Logf("server %q (kind=%s) with %d nodes", parsed.Name, parsed.Kind, len(parsed.Nodes))
	if len(parsed.Nodes) == 0 {
		t.Fatal("expected at least one node")
	}
}

func TestHappShufflesAreInvolutions(t *testing.T) {
	in := []byte("abcdefghijklmnopqrstuvwxyz0123456789")
	if got := string(happPermute4(happPermute4(in))); got != string(in) {
		t.Errorf("permute4 not involution: %q", got)
	}
	if got := string(happSwapPairs(happSwapPairs(in))); got != string(in) {
		t.Errorf("swapPairs not involution: %q", got)
	}
}

// buildCrypt5Link encodes plaintext the way Happ does, so decryptHapp can be
// exercised on both layouts without shipping a real subscription.
func buildCrypt5Link(t *testing.T, marker, plaintext string, salt string) string {
	t.Helper()
	keyB64, ok := happKeys()[marker]
	if !ok {
		t.Fatalf("marker %q not bundled", marker)
	}
	priv, err := parsePKCS8RSA(keyB64)
	if err != nil {
		t.Fatal(err)
	}

	chachaKey := make([]byte, chacha20poly1305.KeySize)
	if _, err := rand.Read(chachaKey); err != nil {
		t.Fatal(err)
	}
	wrapped := base64.StdEncoding.EncodeToString(chachaKey)
	rsaCipher, err := rsa.EncryptPKCS1v15(rand.Reader, &priv.PublicKey, happSwapPairs([]byte(wrapped)))
	if err != nil {
		t.Fatal(err)
	}

	sealKey := append([]byte(nil), chachaKey...)
	for i := range sealKey {
		if salt != "" {
			sealKey[i] ^= salt[i%len(salt)]
		}
	}
	aead, err := chacha20poly1305.New(sealKey)
	if err != nil {
		t.Fatal(err)
	}
	nonce := "nonce-123456"
	inner := happSwapPairs([]byte(base64.StdEncoding.EncodeToString([]byte(plaintext))))
	cipher := base64.StdEncoding.EncodeToString(aead.Seal(nil, []byte(nonce), inner, nil))

	header := nonce
	if salt != "" {
		header += "xy" + salt
	}
	body := header + strconv.Itoa(len(cipher)) + "|" + cipher + base64.StdEncoding.EncodeToString(rsaCipher)
	return "happ://crypt5/" + string(happPermute4([]byte(marker[:4]+body+marker[4:])))
}

func TestDecryptCrypt5Layouts(t *testing.T) {
	const want = "https://example.com/sub/token"
	for name, salt := range map[string]string{"legacy": "", "salted": "saltsalt"} {
		t.Run(name, func(t *testing.T) {
			got, err := decryptHapp(buildCrypt5Link(t, "vdfzfoff", want, salt))
			if err != nil {
				t.Fatalf("decrypt failed: %v", err)
			}
			if got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		})
	}
}
