package common

// Access types a permission or access request can name.
const (
	AccessTypeSSH  = "ssh"
	AccessTypeSCP  = "scp"
	AccessTypeBoth = "both" // ssh and scp; never a tunnel protocol
	AccessTypeRDP  = "rdp"
	AccessTypeVNC  = "vnc"
	// AccessTypeK8s grants Kubernetes access through an in-cluster agent. Its
	// remote_users are namespace/role scopes (see pkg/kube).
	AccessTypeK8s = "k8s"
)

// AccessTypeCovers reports whether a grant of type granted allows access of
// type wanted. "both" predates the tunnel protocols and means ssh+scp only, so
// existing grants never widen to RDP or VNC.
func AccessTypeCovers(granted, wanted string) bool {
	if granted == wanted {
		return true
	}
	return granted == AccessTypeBoth && (wanted == AccessTypeSSH || wanted == AccessTypeSCP)
}

// IsTunnelAccessType reports whether accessType is relayed as raw TCP through
// the agent rather than run as an SSH session. Tunnel grants carry no remote
// user: the target authenticates the login itself.
func IsTunnelAccessType(accessType string) bool {
	return accessType == AccessTypeRDP || accessType == AccessTypeVNC
}

// TunnelChannelType is the channel the gateway opens to an agent to relay one
// TCP connection. Its extra data is TunnelChannelData.
const TunnelChannelType = "orion-tcpip"

// TunnelChannelData names the protocol to relay. The agent maps it to an
// address from its own config, so the gateway cannot point it elsewhere.
type TunnelChannelData struct {
	Protocol string
}

// tunnelPorts are the well-known ports a client names in a direct-tcpip
// request to pick the protocol: `ssh -L 13389:web-01:3389 alice@gateway`.
var tunnelPorts = map[uint32]string{
	3389: AccessTypeRDP,
	5900: AccessTypeVNC,
}

// TunnelProtocolForPort returns the tunnel protocol served on port, or "".
func TunnelProtocolForPort(port uint32) string {
	return tunnelPorts[port]
}

// TunnelPort returns the well-known port of a tunnel protocol, or 0.
func TunnelPort(protocol string) uint32 {
	for port, p := range tunnelPorts {
		if p == protocol {
			return port
		}
	}
	return 0
}

// DefaultAgentTunnels is where an agent relays each protocol unless its config
// says otherwise: the services on the agent's own host.
var DefaultAgentTunnels = map[string]string{
	AccessTypeRDP: "127.0.0.1:3389",
	AccessTypeVNC: "127.0.0.1:5900",
}
