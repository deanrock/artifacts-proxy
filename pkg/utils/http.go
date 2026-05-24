package utils

import (
	"fmt"
	"net"
	"net/http"
	"strings"
)

// GetRequestIP tries to fetch client IP. It uses either the first X-Forwarded-For IP (if provided and enabled), or RemoteAddr.
func GetRequestIP(xForwardedForEnabled bool, r *http.Request) (string, error) {
	if xForwardedForEnabled {
		ips := r.Header.Get("X-Forwarded-For")
		splitIps := strings.Split(ips, ", ")

		netIP := net.ParseIP(splitIps[len(splitIps)-1])
		if netIP != nil {
			return netIP.String(), nil
		}
	}

	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return "", err
	}

	netIP := net.ParseIP(ip)
	if netIP != nil {
		return ip, nil
	}

	return "", fmt.Errorf("couldn't determine request IP")
}
