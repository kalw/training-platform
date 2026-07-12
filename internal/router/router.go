// Package router exposes learner session ports to the outside world.
//
// A session instance's exposed services are reached at hostnames that encode
// the target Pod IP and port, e.g. ip10-244-0-16-<session>.direct.<domain>
// or ip10-244-0-16-8080-<session>...; the router decodes the host, then
// proxies straight to that IP:port. On Kubernetes this Just Works when the
// router runs as an in-cluster Pod: the CNI's flat Pod network makes every
// Pod IP directly routable, with no per-session network attachment — proven
// against kind (see training-deployment/K8S-SANDBOX-DESIGN.md, "the L2 fix").
package router

import (
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
)

// Target is a decoded proxy destination.
type Target struct {
	IP        string
	Port      int
	SessionID string
}

// Addr returns the dial address.
func (t Target) Addr() string { return net.JoinHostPort(t.IP, strconv.Itoa(t.Port)) }

// hostRe matches the host-encoding scheme the console emits (kept
// byte-compatible with training-console-pwd's router/host.go):
//
//	ip<A-B-C-D>-<sessionId>(-<encodedPort>)?(.<tld>)?(:<port>)?
//
// The IP's dashes convert back to dots; the sessionId segment is mandatory.
var hostRe = regexp.MustCompile(`^.*ip(\d{1,3}-\d{1,3}-\d{1,3}-\d{1,3})-([0-9a-z]+)(?:-(\d{1,5}))?(?:\.[a-zA-Z0-9_\-.]+)?(?::\d{1,5})?$`)

// DecodeHost parses a request host into a proxy Target. defaultPort is used
// when the host doesn't encode an explicit (instance) port.
func DecodeHost(host string, defaultPort int) (Target, error) {
	m := hostRe.FindStringSubmatch(host)
	if m == nil {
		return Target{}, fmt.Errorf("host %q does not encode an instance", host)
	}
	ip := strings.ReplaceAll(m[1], "-", ".")
	for _, oct := range strings.Split(ip, ".") {
		if n, _ := strconv.Atoi(oct); n > 255 {
			return Target{}, fmt.Errorf("host %q has an octet > 255", host)
		}
	}
	port := defaultPort
	if m[3] != "" {
		p, err := strconv.Atoi(m[3])
		if err != nil || p < 1 || p > 65535 {
			return Target{}, fmt.Errorf("host %q has an invalid port", host)
		}
		port = p
	}
	return Target{IP: ip, Port: port, SessionID: m[2]}, nil
}
