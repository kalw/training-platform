package router

import (
	"io"
	"net"
	"net/http"
	"time"
)

// HTTPProxy is a reverse proxy that routes each request to the Pod IP:port
// encoded in its Host header (see DecodeHost). It's the HTTP face of the
// exposed-port router; run it as an in-cluster Pod so Pod IPs are directly
// dialable.
type HTTPProxy struct {
	defaultPort int
	dial        func(network, addr string) (net.Conn, error)
}

// NewHTTPProxy returns a proxy that defaults to defaultPort when a host
// encodes no explicit port.
func NewHTTPProxy(defaultPort int) *HTTPProxy {
	d := &net.Dialer{Timeout: 5 * time.Second}
	return &HTTPProxy{defaultPort: defaultPort, dial: d.Dial}
}

func (p *HTTPProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	target, err := DecodeHost(r.Host, p.defaultPort)
	if err != nil {
		http.Error(w, "unrecognized session host", http.StatusBadGateway)
		return
	}

	if r.Header.Get("Upgrade") != "" {
		p.proxyUpgrade(w, r, target)
		return
	}

	outReq := r.Clone(r.Context())
	outReq.RequestURI = ""
	outReq.URL.Scheme = "http"
	outReq.URL.Host = target.Addr()
	resp, err := http.DefaultTransport.RoundTrip(outReq)
	if err != nil {
		http.Error(w, "session unreachable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// proxyUpgrade handles protocol upgrades (WebSocket, etc.) by hijacking the
// client connection and splicing it to a raw dial of the target.
func (p *HTTPProxy) proxyUpgrade(w http.ResponseWriter, r *http.Request, target Target) {
	upstream, err := p.dial("tcp", target.Addr())
	if err != nil {
		http.Error(w, "session unreachable", http.StatusBadGateway)
		return
	}
	defer upstream.Close()

	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "upgrade unsupported", http.StatusInternalServerError)
		return
	}
	client, _, err := hj.Hijack()
	if err != nil {
		return
	}
	defer client.Close()

	if err := r.Write(upstream); err != nil {
		return
	}
	errc := make(chan error, 2)
	go func() { _, e := io.Copy(upstream, client); errc <- e }()
	go func() { _, e := io.Copy(client, upstream); errc <- e }()
	<-errc
}
