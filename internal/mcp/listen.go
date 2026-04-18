package mcp

import (
	"net"
	"strings"
)

const DefaultListenAddress = "127.0.0.1:0"

func SanitizeListenAddress(addr string, enableLAN bool) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return DefaultListenAddress
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return DefaultListenAddress
	}
	host = strings.TrimSpace(host)
	if enableLAN {
		return addr
	}
	if host == "" || host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return addr
	}
	return DefaultListenAddress
}
