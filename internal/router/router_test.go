package router

import "testing"

func TestDecodeHost(t *testing.T) {
	// Host format: ip<A-B-C-D>-<sessionId>(-<encodedPort>)?(.<tld>)?(:<port>)?
	cases := []struct {
		host     string
		defPort  int
		wantIP   string
		wantPort int
		wantErr  bool
	}{
		{"ip10-244-0-16-abcd1234.direct.example.com", 80, "10.244.0.16", 80, false},
		{"ip10-244-0-16-abcd1234-8080.direct.example.com", 80, "10.244.0.16", 8080, false},
		// {:data-host-prefix="…"} links prepend "<prefix>-" to the encoding.
		{"exampleprefix-ip10-244-0-16-abcd1234-8080.direct.example.com", 80, "10.244.0.16", 8080, false},
		// A prefix containing "ip" must not confuse the decoder.
		{"shipping-ip10-244-0-16-abcd1234.direct.example.com", 80, "10.244.0.16", 80, false},
		{"ip192-168-1-1-sess1.direct.example.com:443", 80, "192.168.1.1", 80, false}, // trailing :443 is the browser port, not the instance port
		{"pwdsomething.direct.example.com", 80, "", 0, true},                         // no ip-encoding
		{"ip10-244-0-999-sess1.direct.example.com", 80, "", 0, true},                 // octet > 255
		{"plainhost.example.com", 80, "", 0, true},
	}
	for _, c := range cases {
		got, err := DecodeHost(c.host, c.defPort)
		if c.wantErr {
			if err == nil {
				t.Errorf("DecodeHost(%q): expected error, got %+v", c.host, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("DecodeHost(%q): unexpected error %v", c.host, err)
			continue
		}
		if got.IP != c.wantIP || got.Port != c.wantPort {
			t.Errorf("DecodeHost(%q) = %s:%d, want %s:%d", c.host, got.IP, got.Port, c.wantIP, c.wantPort)
		}
	}
}

func TestTargetAddr(t *testing.T) {
	if a := (Target{IP: "10.0.0.1", Port: 8080}).Addr(); a != "10.0.0.1:8080" {
		t.Errorf("Addr() = %q", a)
	}
}
