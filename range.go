// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fileserver

import (
	"errors"
	"fmt"
	"mime/multipart"
	"net/textproto"
	"strconv"
	"strings"
)

type httpRange struct {
	start, length int64
}

func (r httpRange) contentRange(size int64) string {
	return fmt.Sprintf("bytes %d-%d/%d", r.start, r.start+r.length-1, size)
}

func (r httpRange) mimeHeader(contentType string, size int64) textproto.MIMEHeader {
	return textproto.MIMEHeader{
		"Content-Range": {r.contentRange(size)},
		"Content-Type":  {contentType},
	}
}

func parseRange(s string, size int64) ([]httpRange, error) {
	if s == "" {
		return nil, nil
	}

	const b = "bytes="
	if !strings.HasPrefix(s, b) {
		return nil, errors.New("invalid range")
	}

	var ranges []httpRange
	noOverlap := false

	for _, ra := range strings.Split(s[len(b):], ",") {
		ra = textproto.TrimString(ra)

		if ra == "" {
			continue
		}

		start, end, ok := strings.Cut(ra, "-")
		if !ok {
			return nil, errors.New("invalid range")
		}

		start, end = textproto.TrimString(start), textproto.TrimString(end)
		var r httpRange

		if start == "" {
			if end == "" || end[0] == '-' {
				return nil, errors.New("invalid range")
			}

			i, err := strconv.ParseInt(end, 10, 64)
			if i < 0 || err != nil {
				return nil, errors.New("invalid range")
			}

			if i > size {
				i = size
			}

			r.start = size - i
			r.length = size - r.start
		} else {
			i, err := strconv.ParseInt(start, 10, 64)
			if err != nil || i < 0 {
				return nil, errors.New("invalid range")
			}

			if i >= size {
				noOverlap = true
				continue
			}

			r.start = i

			if end == "" {
				r.length = size - r.start
			} else {
				i, err := strconv.ParseInt(end, 10, 64)
				if err != nil || r.start > i {
					return nil, errors.New("invalid range")
				}

				if i >= size {
					i = size - 1
				}

				r.length = i - r.start + 1
			}
		}

		ranges = append(ranges, r)
	}

	if noOverlap && len(ranges) == 0 {
		return nil, errNoOverlap
	}

	return ranges, nil
}

func sumRangesSize(ranges []httpRange) int64 {
	var size int64
	for _, ra := range ranges {
		size += ra.length
	}
	return size
}

type countingWriter int64

func (w *countingWriter) Write(p []byte) (n int, err error) {
	*w += countingWriter(len(p))
	return len(p), nil
}

func rangesMIMESize(ranges []httpRange, contentType string, contentSize int64) int64 {
	var w countingWriter
	var encSize int64
	mw := multipart.NewWriter(&w)

	for _, ra := range ranges {
		mw.CreatePart(ra.mimeHeader(contentType, contentSize))
		encSize += ra.length
	}

	mw.Close()
	encSize += int64(w)
	return encSize
}
