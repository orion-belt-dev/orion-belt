package common

import "testing"

func TestAccessTypeCovers(t *testing.T) {
	cases := []struct {
		granted, wanted string
		want            bool
	}{
		{"ssh", "ssh", true},
		{"both", "ssh", true},
		{"both", "scp", true},
		{"both", "rdp", false},
		{"both", "vnc", false},
		{"ssh", "rdp", false},
		{"rdp", "rdp", true},
		{"rdp", "vnc", false},
		{"vnc", "ssh", false},
	}
	for _, tc := range cases {
		if got := AccessTypeCovers(tc.granted, tc.wanted); got != tc.want {
			t.Errorf("AccessTypeCovers(%q, %q) = %v, want %v", tc.granted, tc.wanted, got, tc.want)
		}
	}
}

func TestTunnelPorts(t *testing.T) {
	for _, p := range []string{AccessTypeRDP, AccessTypeVNC} {
		if !IsTunnelAccessType(p) {
			t.Errorf("%s should be a tunnel type", p)
		}
		if got := TunnelProtocolForPort(TunnelPort(p)); got != p {
			t.Errorf("port round trip for %s gave %q", p, got)
		}
	}
	if IsTunnelAccessType("both") || TunnelPort("ssh") != 0 || TunnelProtocolForPort(22) != "" {
		t.Error("ssh types must not be tunnel types")
	}
}

func TestAgentTunnelAddress(t *testing.T) {
	var c AgentConfig
	if got := c.TunnelAddress(AccessTypeRDP); got != "127.0.0.1:3389" {
		t.Errorf("default rdp = %q", got)
	}
	c.Tunnels = map[string]string{AccessTypeRDP: "win-01.lan:3389", AccessTypeVNC: "off"}
	if got := c.TunnelAddress(AccessTypeRDP); got != "win-01.lan:3389" {
		t.Errorf("configured rdp = %q", got)
	}
	if got := c.TunnelAddress(AccessTypeVNC); got != "" {
		t.Errorf("disabled vnc = %q", got)
	}
	if got := c.TunnelAddress("ssh"); got != "" {
		t.Errorf("ssh = %q, want no tunnel", got)
	}
}
