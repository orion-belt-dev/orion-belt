package common

import "testing"

func TestAdvertisedURLPrefersPublicURL(t *testing.T) {
	s := ServerConfig{
		PublicURL:   "https://orion.example.com/",
		APIEndpoint: "http://localhost:8080/api",
	}
	if got := s.AdvertisedURL(); got != "https://orion.example.com" {
		t.Fatalf("AdvertisedURL = %q", got)
	}
}

func TestAdvertisedSSHHostFromPublicURL(t *testing.T) {
	s := ServerConfig{PublicURL: "https://orion.example.com:8443"}
	if got := s.AdvertisedSSHHost(); got != "orion.example.com" {
		t.Fatalf("AdvertisedSSHHost = %q", got)
	}
	if got := s.AdvertisedSSHPort(); got != 2222 {
		t.Fatalf("AdvertisedSSHPort default = %d", got)
	}
	s.PublicSSHPort = 22222
	if got := s.AdvertisedSSHPort(); got != 22222 {
		t.Fatalf("AdvertisedSSHPort override = %d", got)
	}
}

func TestUIURLFallsBackToLocalhost(t *testing.T) {
	s := ServerConfig{APIPort: 9080}
	if got := s.UIURL(); got != "http://localhost:9080/ui" {
		t.Fatalf("UIURL = %q", got)
	}
}

func TestWebAuthnDefaultsFromPublicURL(t *testing.T) {
	s := ServerConfig{PublicURL: "https://gw.example.com"}
	wa := &WebAuthnConfig{Enabled: true}
	s.WebAuthnDefaultsFromPublicURL(wa)
	if wa.RPID != "gw.example.com" {
		t.Fatalf("RPID = %q", wa.RPID)
	}
	if len(wa.Origins) != 1 || wa.Origins[0] != "https://gw.example.com" {
		t.Fatalf("Origins = %#v", wa.Origins)
	}
	wa.RPID = "custom.example"
	wa.Origins = []string{"https://custom.example"}
	s.WebAuthnDefaultsFromPublicURL(wa)
	if wa.RPID != "custom.example" {
		t.Fatalf("explicit RPID overwritten: %q", wa.RPID)
	}
}
