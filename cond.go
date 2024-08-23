// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fileserver

import (
	"net/http"
	"net/textproto"
	"time"
)

type condResult int

const (
	condNone condResult = iota
	condTrue
	condFalse
)

func checkIfModifiedSince(r *http.Request, modtime time.Time) condResult {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return condNone
	}

	ims := r.Header.Get("If-Modified-Since")
	if ims == "" || isZeroTime(modtime) {
		return condNone
	}

	t, err := http.ParseTime(ims)
	if err != nil {
		return condNone
	}

	modtime = modtime.Truncate(time.Second)
	if ret := modtime.Compare(t); ret <= 0 {
		return condFalse
	}

	return condTrue
}

func checkIfMatch(w http.ResponseWriter, r *http.Request) condResult {
	im := r.Header.Get("If-Match")
	if im == "" {
		return condNone
	}

	for {
		im = textproto.TrimString(im)
		if len(im) == 0 {
			break
		}

		if im[0] == ',' {
			im = im[1:]
			continue
		}

		if im[0] == '*' {
			return condTrue
		}

		etag, remain := scanETag(im)
		if etag == "" {
			break
		}

		if etagStrongMatch(etag, getHeader(w.Header(), "Etag")) {
			return condTrue
		}

		im = remain
	}

	return condFalse
}

func checkIfUnmodifiedSince(r *http.Request, modtime time.Time) condResult {
	ius := r.Header.Get("If-Unmodified-Since")
	if ius == "" || isZeroTime(modtime) {
		return condNone
	}

	t, err := http.ParseTime(ius)
	if err != nil {
		return condNone
	}

	modtime = modtime.Truncate(time.Second)
	if ret := modtime.Compare(t); ret <= 0 {
		return condTrue
	}

	return condFalse
}

func checkIfNoneMatch(w http.ResponseWriter, r *http.Request) condResult {
	inm := r.Header.Get("If-None-Match")
	if inm == "" {
		return condNone
	}

	buf := inm
	for {
		buf = textproto.TrimString(buf)
		if len(buf) == 0 {
			break
		}

		if buf[0] == ',' {
			buf = buf[1:]
			continue
		}

		if buf[0] == '*' {
			return condFalse
		}

		etag, remain := scanETag(buf)
		if etag == "" {
			break
		}

		if etagWeakMatch(etag, getHeader(w.Header(), "Etag")) {
			return condFalse
		}

		buf = remain
	}

	return condFalse
}

func checkIfRange(w http.ResponseWriter, r *http.Request, modtime time.Time) condResult {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return condNone
	}

	ir := r.Header.Get("If-Range")
	if ir == "" {
		return condNone
	}

	etag, _ := scanETag(ir)
	if etag != "" {
		if etagStrongMatch(etag, w.Header().Get("Etag")) {
			return condTrue
		} else {
			return condFalse
		}
	}

	if modtime.IsZero() {
		return condFalse
	}

	t, err := http.ParseTime(ir)
	if err != nil {
		return condFalse
	}

	if t.Unix() == modtime.Unix() {
		return condTrue
	}

	return condFalse
}
