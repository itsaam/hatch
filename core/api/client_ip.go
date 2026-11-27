package main

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/httprate"
)

func proxyClientIPMiddleware(setting string) (func(http.Handler) http.Handler, error) {
	if strings.TrimSpace(setting) == "" {
		return middleware.ClientIPFromRemoteAddr, nil
	}
	values := strings.Split(setting, ",")
	prefixes := make([]netip.Prefix, 0, len(values))
	for i, value := range values {
		values[i] = strings.TrimSpace(value)
		prefix, err := netip.ParsePrefix(values[i])
		if err != nil {
			return nil, fmt.Errorf("invalid HATCH_TRUSTED_PROXY_CIDRS entry %q: %w", value, err)
		}
		prefixes = append(prefixes, prefix)
	}
	fromProxy := middleware.ClientIPFromXFF(values...)
	return func(next http.Handler) http.Handler {
		proxied := fromProxy(next)
		direct := middleware.ClientIPFromRemoteAddr(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			host, _, err := net.SplitHostPort(r.RemoteAddr)
			if err == nil {
				ip, err := netip.ParseAddr(host)
				if err == nil {
					for _, prefix := range prefixes {
						if prefix.Contains(ip.Unmap().WithZone("")) {
							proxied.ServeHTTP(w, r)
							return
						}
					}
				}
			}
			direct.ServeHTTP(w, r)
		})
	}, nil
}

func previewRateKey(r *http.Request) (string, error) {
	if ip := middleware.GetClientIP(r.Context()); ip != "" {
		return ip, nil
	}
	return httprate.KeyByIP(r)
}
