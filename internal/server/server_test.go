package server

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The composed server must answer for exposed-port hosts itself when
// RouterHost is set: ip<A-B-C-D>-<id>-<port>.<RouterHost> proxies to that
// IP:port (in production a Pod IP; here a loopback backend), everything
// else falls through to the normal mux.
func TestServeProxiesExposedPortHosts(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "backend says %s", r.URL.Path)
	}))
	defer backend.Close()
	_, portStr, err := net.SplitHostPort(strings.TrimPrefix(backend.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}

	h, _, err := New(Config{RouterHost: "direct.dev.test:8080", Salt: "s"})
	if err != nil {
		t.Fatal(err)
	}
	front := httptest.NewServer(h)
	defer front.Close()

	get := func(host string) (int, string) {
		req, _ := http.NewRequest("GET", front.URL+"/status", nil)
		req.Host = host
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	// A session host (backend runs on 127.0.0.1) proxies through.
	code, body := get("ip127-0-0-1-iabc0-" + portStr + ".direct.dev.test:8080")
	if code != 200 || body != "backend says /status" {
		t.Errorf("proxied request = %d %q, want the backend response", code, body)
	}

	// A plain host falls through to the mux (the /healthz route).
	req, _ := http.NewRequest("GET", front.URL+"/healthz", nil)
	req.Host = "localhost:8080"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if b, _ := io.ReadAll(resp.Body); string(b) != "ok" {
		t.Errorf("plain host did not reach the mux: %q", b)
	}

	// A subdomain that doesn't decode falls through to the mux — it must
	// never reach the proxy/backend.
	if _, body := get("nonsense.direct.dev.test:8080"); strings.Contains(body, "backend says") {
		t.Errorf("undecodable session host reached the proxy backend: %q", body)
	}
}
