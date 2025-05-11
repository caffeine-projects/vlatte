package functions

import (
	"encoding/base64"
	"net/url"
)

func URL(baseURL string) func(paths ...string) string {
	return func(paths ...string) string {
		if len(paths) == 0 {
			return baseURL
		}

		u, err := url.JoinPath(baseURL, paths...)
		if err != nil {
			return ""
		}

		return u
	}
}

func Base64(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}
