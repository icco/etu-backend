package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"
)

// mediaURL grants access to one object for a day. The image gateway permits
// bounded responsive transforms without exposing this server-side signing key.
func mediaURL(base, object string, now time.Time) string {
	key := os.Getenv("MEDIA_SIGNING_KEY")
	if base == "" || key == "" {
		return ""
	}
	u, err := url.Parse(strings.TrimRight(base, "/") + "/" + (&url.URL{Path: object}).EscapedPath())
	if err != nil {
		return ""
	}
	expiry := fmt.Sprint(now.Add(24 * time.Hour).Unix())
	mac := hmac.New(sha256.New, []byte(key))
	_, _ = mac.Write([]byte(u.EscapedPath() + "\n" + expiry))
	u.RawQuery = url.Values{"exp": {expiry}, "sig": {hex.EncodeToString(mac.Sum(nil))}}.Encode()
	return u.String()
}
