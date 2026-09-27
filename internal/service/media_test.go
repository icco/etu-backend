package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"testing"
	"time"
)

func TestMediaURLCapability(t *testing.T) {
	t.Setenv("MEDIA_SIGNING_KEY", "test-key")
	got := mediaURL("https://images.natwelch.com/etu", "notes/note/a b.jpg", time.Unix(1000, 0))
	u, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if u.EscapedPath() != "/etu/notes/note/a%20b.jpg" || u.Query().Get("exp") != "87400" {
		t.Fatalf("unexpected URL: %s", got)
	}
	mac := hmac.New(sha256.New, []byte("test-key"))
	_, _ = mac.Write([]byte("/etu/notes/note/a%20b.jpg\n87400"))
	if u.Query().Get("sig") != hex.EncodeToString(mac.Sum(nil)) {
		t.Fatal("gateway signature mismatch")
	}
	t.Setenv("MEDIA_SIGNING_KEY", "")
	if mediaURL("https://images.natwelch.com/etu", "notes/a", time.Now()) != "" {
		t.Fatal("unsigned media must not be emitted")
	}
}
