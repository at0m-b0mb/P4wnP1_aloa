package tmp_optcheck

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/improbable-eng/grpc-web/go/grpcweb"
	"google.golang.org/grpc"
)

func router() http.HandlerFunc {
	s := grpc.NewServer()
	gw := grpcweb.WrapServer(s,
		grpcweb.WithWebsockets(true),
		grpcweb.WithOriginFunc(func(string) bool { return false }),
	)
	fs := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("STATIC"))
	})
	return func(resp http.ResponseWriter, req *http.Request) {
		if strings.Contains(req.Header.Get("Content-Type"), "application/grpc") ||
			req.Method == "OPTIONS" ||
			strings.Contains(req.Header.Get("Sec-Websocket-Protocol"), "grpc-websockets") {
			gw.ServeHTTP(resp, req)
			return
		}
		if req.URL.Path == "/" {
			http.Redirect(resp, req, "/app/", http.StatusFound)
			return
		}
		fs.ServeHTTP(resp, req)
	}
}

func TestOptions(t *testing.T) {
	h := router()
	cases := []struct {
		name string
		req  *http.Request
	}{
		{"bare OPTIONS /app/", httptest.NewRequest("OPTIONS", "/app/", nil)},
		{"bare OPTIONS /", httptest.NewRequest("OPTIONS", "/", nil)},
		{"bare OPTIONS /app/index.html", httptest.NewRequest("OPTIONS", "/app/index.html", nil)},
		{"OPTIONS with Origin only", httptest.NewRequest("OPTIONS", "/app/", nil)},
		{"OPTIONS preflight x-grpc-web", httptest.NewRequest("OPTIONS", "/app/", nil)},
		{"GET /app/ (control)", httptest.NewRequest("GET", "/app/", nil)},
	}
	cases[3].req.Header.Set("Origin", "http://evil.example")
	cases[3].req.Header.Set("Access-Control-Request-Method", "POST")
	cases[4].req.Header.Set("Origin", "http://evil.example")
	cases[4].req.Header.Set("Access-Control-Request-Headers", "x-grpc-web,content-type")
	cases[4].req.Header.Set("Access-Control-Request-Method", "POST")

	for _, c := range cases {
		rec := httptest.NewRecorder()
		h(rec, c.req)
		t.Logf("%-34s proto=%d -> %d body=%q", c.name, c.req.ProtoMajor, rec.Code, strings.TrimSpace(rec.Body.String()))
	}
}
