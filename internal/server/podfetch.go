package server

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/kalw/training-platform/internal/session"
)

// maxVerifyBody caps how much of a learner's page we read — the assertion
// only needs the page, and an exercise Pod could serve anything.
const maxVerifyBody = 1 << 20 // 1 MiB

// podFetcher implements scoring.PodFetcher against the session engine: it
// resolves an instance Pod to its IP and GETs the challenge's fixed
// port/path in-cluster (Pod IPs are directly routable from here, the same
// property the exposed-port router relies on).
//
// Security: the pod name is the only client-supplied input, so it is checked
// against the session engine before dialing — Status only reports Pods the
// platform manages in the session namespace. Port and path come from the
// challenge definition, so this cannot be steered at arbitrary hosts.
type podFetcher struct {
	eng    *session.Engine
	ns     string
	client *http.Client
}

func newPodFetcher(eng *session.Engine, ns string) *podFetcher {
	return &podFetcher{
		eng: eng,
		ns:  ns,
		client: &http.Client{
			Timeout: 8 * time.Second,
			// Never follow a redirect off the Pod: the assertion must be about
			// the page the learner's own service served.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

func (p *podFetcher) FetchPod(ctx context.Context, pod string, port int, path string) (int, []byte, error) {
	if !termPodRe.MatchString(pod) {
		return 0, nil, fmt.Errorf("invalid pod name")
	}
	if port <= 0 || port > 65535 {
		return 0, nil, fmt.Errorf("invalid port")
	}
	// Resolve through the session engine: this both gets the IP and proves the
	// Pod is one we manage (Status lists only managed instance Pods in ns).
	st, err := p.eng.Status(ctx, p.ns, pod)
	if err != nil {
		return 0, nil, fmt.Errorf("no such session")
	}
	if !st.Ready || st.IP == "" {
		return 0, nil, fmt.Errorf("session not ready")
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	u := url.URL{Scheme: "http", Host: net.JoinHostPort(st.IP, strconv.Itoa(port)), Path: path}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return 0, nil, err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("could not reach the page in your session: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxVerifyBody))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, body, nil
}
