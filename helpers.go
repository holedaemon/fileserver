// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fileserver

import (
	"errors"
	"io/fs"
	"net/http"
	"net/textproto"
	"strings"
	"time"
)

func localRedirect(w http.ResponseWriter, r *http.Request, newPath string) {
	if q := r.URL.RawQuery; q != "" {
		newPath += "?" + q
	}
	w.Header().Set("Location", newPath)
	w.WriteHeader(http.StatusMovedPermanently)
}

func toHTTPError(err error) int {
	if errors.Is(err, fs.ErrNotExist) {
		return http.StatusNotFound
	}

	if errors.Is(err, fs.ErrPermission) {
		return http.StatusForbidden
	}

	return http.StatusInternalServerError
}

func isZeroTime(t time.Time) bool {
	return t.IsZero() || t.Equal(time.Unix(0, 0))
}

func writeNotModified(w http.ResponseWriter) {
	h := w.Header()
	delete(h, "Content-Type")
	delete(h, "Content-Length")
	delete(h, "Content-Encoding")
	if h.Get("Etag") != "" {
		delete(h, "Last-Modified")
	}
	w.WriteHeader(http.StatusNotModified)
}

func setLastModified(w http.ResponseWriter, modtime time.Time) {
	if !isZeroTime(modtime) {
		w.Header().Set("Last-Modified", modtime.UTC().Format(http.TimeFormat))
	}
}

func scanETag(s string) (string, string) {
	s = textproto.TrimString(s)
	start := 0
	if strings.HasPrefix(s, "W/") {
		start = 2
	}

	if len(s[start:]) < 2 || s[start] != '"' {
		return "", ""
	}

	for i := start + 1; i < len(s); i++ {
		c := s[i]
		switch {
		case c == 0x21 || c >= 0x23 && c <= 0x7E || c >= 0x80:
		case c == '"':
			return s[:i+1], s[i+1:]
		default:
			return "", ""
		}
	}

	return "", ""
}

func etagStrongMatch(a, b string) bool {
	return a == b && a != "" && a[0] == '"'
}

func etagWeakMatch(a, b string) bool {
	return strings.TrimPrefix(a, "W/") == strings.TrimPrefix(b, "W/")
}

func getHeader(h http.Header, key string) string {
	if v := h[key]; len(v) > 0 {
		return v[0]
	}

	return ""
}
