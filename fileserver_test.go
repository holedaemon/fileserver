package fileserver

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"testing"
)

func TestFileServer(t *testing.T) {
	fs := FileServer(
		http.Dir("./testdata"),
		func(w http.ResponseWriter, r *http.Request, i int) {
			http.Error(w, http.StatusText(i), i)
		},
		func(w http.ResponseWriter, r *http.Request, fe []FileEntry) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprintf(w, "<!DOCTYPE html>\n")
			fmt.Fprintf(w, "<meta name=\"viewport\" content=\"width=device-width\">\n")
			fmt.Fprintf(w, "<img src=\"https://holedaemon.net/images/yousuck.jpg\">")
			fmt.Fprintf(w, "<pre>\n")

			for _, f := range fe {
				fmt.Fprintf(w, "<a href=\"%s\">%s</a>\n", f.URL, f.Name)
			}

			fmt.Fprintf(w, "</pre>")
		},
	)

	srv := httptest.NewServer(fs)

	t.Logf("Started server on %s", srv.URL)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	<-ctx.Done()

	srv.Close()
}
