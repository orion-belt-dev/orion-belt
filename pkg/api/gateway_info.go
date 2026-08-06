package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// gatewayInfo returns the advertised addresses operators and agents should use
// to reach this gateway (not the bind address).
func (s *APIServer) gatewayInfo(c *gin.Context) {
	cfg := s.serverCfg
	c.JSON(http.StatusOK, gin.H{
		"public_url":      cfg.AdvertisedURL(),
		"ui_url":          cfg.UIURL(),
		"ssh_host":        cfg.AdvertisedSSHHost(),
		"ssh_port":        cfg.AdvertisedSSHPort(),
		"api_port":        cfg.EffectiveAPIPort(),
		"public_ssh_host": cfg.PublicSSHHost,
		"public_ssh_port": cfg.PublicSSHPort,
	})
}
