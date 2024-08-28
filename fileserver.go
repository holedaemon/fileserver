// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fileserver

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	errNoOverlap = errors.New("invalid range: failed to overlap")

	htmlReplacer = strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&#34;",
		"'", "&#39",
	)
)

const (
	sniffLen = 512
)

// FileServer returns a handler that serves HTTP requests
// with the contents of the file system rooted at root.
//
// As a special case, the returned file server redirects any request
// ending in "/index.html" to the same path, without the final
// "index.html".
//
// To use the operating system's file system implementation,
// use [http.Dir]:
//
//	http.Handle("/", FileServer(http.Dir("/tmp")))
//
// This has been modified from the implementation found
// in stdlib's net/http to allow for custom markup
// instead of the hardcoded markup.
func FileServer(
	root http.FileSystem,
	errHandler func(http.ResponseWriter, *http.Request, int, error),
	dirListHandler func(http.ResponseWriter, *http.Request, []FileEntry),
) *fileHandler {
	return &fileHandler{
		root:           root,
		errHandler:     errHandler,
		dirListHandler: dirListHandler,
	}
}

// FileEntry contains metadata for a file found in a filesystem.
type FileEntry struct {
	URL  string
	Name string
}

type fileHandler struct {
	root           http.FileSystem
	errHandler     func(http.ResponseWriter, *http.Request, int, error)
	dirListHandler func(http.ResponseWriter, *http.Request, []FileEntry)
}

func (h *fileHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	upath := r.URL.Path
	if !strings.HasPrefix(upath, "/") {
		upath = "/" + upath
		r.URL.Path = upath
	}
	h.serveFile(w, r, h.root, path.Clean(upath), true)
}

func (h *fileHandler) serveFile(w http.ResponseWriter, r *http.Request, fs http.FileSystem, name string, redirect bool) {
	const indexPage = "/index.html"

	if strings.HasSuffix(r.URL.Path, indexPage) {
		localRedirect(w, r, "./")
		return
	}

	f, err := fs.Open(name)
	if err != nil {
		code := toHTTPError(err)
		h.errHandler(w, r, code, err)
		return
	}

	defer f.Close()

	d, err := f.Stat()
	if err != nil {
		code := toHTTPError(err)
		h.errHandler(w, r, code, err)
		return
	}

	if redirect {
		url := r.URL.Path
		if d.IsDir() {
			if url[len(url)-1] != '/' {
				localRedirect(w, r, path.Base(url)+"/")
				return
			}
		} else {
			if url[len(url)-1] == '/' {
				localRedirect(w, r, "../"+path.Base(url))
				return
			}
		}
	}

	if d.IsDir() {
		url := r.URL.Path
		if url == "" || url[len(url)-1] != '/' {
			localRedirect(w, r, path.Base(url)+"/")
			return
		}

		index := strings.TrimSuffix(name, "/") + indexPage
		ff, err := fs.Open(index)
		if err == nil {
			defer ff.Close()
			dd, err := ff.Stat()
			if err == nil {
				d = dd
				f = ff
			}
		}
	}

	if d.IsDir() {
		if checkIfModifiedSince(r, d.ModTime()) == condFalse {
			writeNotModified(w)
			return
		}
		setLastModified(w, d.ModTime())
		h.dirList(w, r, f)
		return
	}

	sizeFunc := func() (int64, error) { return d.Size(), nil }
	h.serveContent(w, r, d.Name(), d.ModTime(), sizeFunc, f)
}

type anyDirs interface {
	len() int
	name(i int) string
	isDir(i int) bool
}

type fileInfoDirs []fs.FileInfo

func (d fileInfoDirs) len() int          { return len(d) }
func (d fileInfoDirs) isDir(i int) bool  { return d[i].IsDir() }
func (d fileInfoDirs) name(i int) string { return d[i].Name() }

type dirEntryDirs []fs.DirEntry

func (d dirEntryDirs) len() int          { return len(d) }
func (d dirEntryDirs) isDir(i int) bool  { return d[i].IsDir() }
func (d dirEntryDirs) name(i int) string { return d[i].Name() }

func (h *fileHandler) dirList(w http.ResponseWriter, r *http.Request, f http.File) {
	var dirs anyDirs
	var err error

	if d, ok := f.(fs.ReadDirFile); ok {
		var list dirEntryDirs
		list, err = d.ReadDir(-1)
		dirs = list
	} else {
		var list fileInfoDirs
		list, err = f.Readdir(-1)
		dirs = list
	}

	var files []FileEntry
	for i, n := 0, dirs.len(); i < n; i++ {
		name := dirs.name(i)
		if dirs.isDir(i) {
			name += "/"
		}

		url := url.URL{Path: name}
		files = append(files, FileEntry{
			URL:  url.String(),
			Name: htmlReplacer.Replace(name),
		})
	}

	if err != nil {
		h.errHandler(w, r, http.StatusInternalServerError, err)
		return
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs.name(i) < dirs.name(j) })

	h.dirListHandler(w, r, files)
}

func (h *fileHandler) serveContent(w http.ResponseWriter, r *http.Request, name string, modtime time.Time, sizeFunc func() (int64, error), content io.ReadSeeker) {
	setLastModified(w, modtime)
	done, rangeReq := checkPreconditions(w, r, modtime)
	if done {
		return
	}

	code := http.StatusOK

	ctypes, haveType := w.Header()["Content-Type"]
	var ctype string
	if !haveType {
		ctype = mime.TypeByExtension(filepath.Ext(name))
		if ctype == "" {
			var buf [sniffLen]byte
			n, _ := io.ReadFull(content, buf[:])
			ctype = http.DetectContentType(buf[:n])
			_, err := content.Seek(0, io.SeekStart)
			if err != nil {
				h.errHandler(w, r, http.StatusInternalServerError, err)
				return
			}
		}
		w.Header().Set("Content-Type", ctype)
	} else if len(ctypes) > 0 {
		ctype = ctypes[0]
	}

	size, err := sizeFunc()
	if err != nil {
		h.errHandler(w, r, http.StatusInternalServerError, err)
		return
	}

	if size < 0 {
		h.errHandler(w, r, http.StatusInternalServerError, err)
		return
	}

	sendSize := size
	var sendContent io.Reader = content
	ranges, err := parseRange(rangeReq, size)
	switch err {
	case nil:
	case errNoOverlap:
		if size == 0 {
			ranges = nil
			break
		}

		w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", size))
		fallthrough
	default:
		h.errHandler(w, r, http.StatusRequestedRangeNotSatisfiable, err)
		return
	}

	if sumRangesSize(ranges) > size {
		ranges = nil
	}

	switch {
	case len(ranges) == 1:
		ra := ranges[0]

		if _, err := content.Seek(ra.start, io.SeekStart); err != nil {
			h.errHandler(w, r, http.StatusRequestedRangeNotSatisfiable, err)
			return
		}

		sendSize = ra.length
		code = http.StatusPartialContent
		w.Header().Set("Content-Range", ra.contentRange(size))
	case len(ranges) > 1:
		sendSize = rangesMIMESize(ranges, ctype, size)
		code = http.StatusPartialContent

		pr, pw := io.Pipe()
		mw := multipart.NewWriter(pw)

		w.Header().Set("Content-Type", "multipart/byteranges; boundary="+mw.Boundary())
		sendContent = pr

		defer pr.Close()

		go func() {
			for _, ra := range ranges {
				part, err := mw.CreatePart(ra.mimeHeader(ctype, size))
				if err != nil {
					pw.CloseWithError(err)
					return
				}

				if _, err := content.Seek(ra.start, io.SeekStart); err != nil {
					pw.CloseWithError(err)
					return
				}

				if _, err := io.CopyN(part, content, ra.length); err != nil {
					pw.CloseWithError(err)
					return
				}
			}
			mw.Close()
			pw.Close()
		}()
	}

	w.Header().Set("Accept-Ranges", "bytes")

	if len(ranges) > 0 || w.Header().Get("Content-Encoding") == "" {
		w.Header().Set("Content-Length", strconv.FormatInt(sendSize, 10))
	}
	w.WriteHeader(code)

	if r.Method != http.MethodHead {
		io.CopyN(w, sendContent, sendSize) //nolint:errcheck
	}
}

func checkPreconditions(w http.ResponseWriter, r *http.Request, modtime time.Time) (bool, string) {
	ch := checkIfMatch(w, r)
	if ch == condNone {
		ch = checkIfUnmodifiedSince(r, modtime)
	}

	if ch == condFalse {
		w.WriteHeader(http.StatusPreconditionFailed)
		return true, ""
	}

	switch checkIfNoneMatch(w, r) {
	case condFalse:
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			writeNotModified(w)
			return true, ""
		}
	case condNone:
		if checkIfModifiedSince(r, modtime) == condFalse {
			writeNotModified(w)
			return true, ""
		}
	}

	rangeHeader := r.Header.Get("Range")
	if rangeHeader != "" && checkIfRange(w, r, modtime) == condFalse {
		rangeHeader = ""
	}

	return false, rangeHeader
}
