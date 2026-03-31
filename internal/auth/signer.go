package auth

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// FormatDate returns the current UTC time in the format OSFCI expects:
// "Mon, 02 Jan 2006 15:04:05 +0000" (HTTP date with +0000 instead of GMT).
func FormatDate() string {
	d := time.Now().UTC().Format(http.TimeFormat)
	return strings.Replace(d, "GMT", "+0000", -1)
}

// Sign computes the HMAC-SHA1 signature for an OSFCI API request.
//
// The string-to-sign is:
//
//	{METHOD}\n\n{Content-Type}\n{formattedDate}\n{urlPath}
//
// The signature is base64(HMAC-SHA1(secretKey, stringToSign)).
func Sign(method, contentType, date, urlPath, secretKey string) string {
	stringToSign := fmt.Sprintf("%s\n\n%s\n%s\n%s", method, contentType, date, urlPath)
	mac := hmac.New(sha1.New, []byte(secretKey))
	mac.Write([]byte(stringToSign))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// AuthorizationHeader returns the full Authorization header value:
// "OSF {accessKey}:{signature}"
func AuthorizationHeader(accessKey, signature string) string {
	return fmt.Sprintf("OSF %s:%s", accessKey, signature)
}
