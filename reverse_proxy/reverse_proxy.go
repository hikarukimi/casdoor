package proxy

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"

	"github.com/casdoor/casdoor/conf"
	"github.com/casdoor/casdoor/object"
)

var (
	// domainToAppMap maps domain names to applications
	domainToAppMap = make(map[string]*object.Application)
	// appCacheMutex protects domainToAppMap from concurrent access
	appCacheMutex sync.RWMutex
)

type ReverseProxyHandler struct {
	Next http.Handler
}

func NewReverseProxyHandler(next http.Handler) *ReverseProxyHandler {
	return &ReverseProxyHandler{Next: next}
}

func (h *ReverseProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	// Remove port if present
	host = strings.Split(host, ":")[0]

	app := findApplicationByDomain(host)
	if app == nil || app.UpstreamHost == "" {
		// No matching application found, pass through to next handler
		if h.Next != nil {
			h.Next.ServeHTTP(w, r)
		} else {
			http.Error(w, "Not Found", http.StatusNotFound)
		}
		return
	}

	proxy, err := createReverseProxy(app.UpstreamHost)
	if err != nil {
		// Failed to create reverse proxy
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	proxy.ServeHTTP(w, r)
}

func findApplicationByDomain(domain string) *object.Application {
	appCacheMutex.RLock()
	app, found := domainToAppMap[domain]
	appCacheMutex.RUnlock()

	if found {
		return app
	}

	return nil
}

func createReverseProxy(upstreamHost string) (*httputil.ReverseProxy, error) {
	// Parse the upstream host URL
	targetURL, err := url.Parse(upstreamHost)
	if err != nil {
		return nil, err
	}

	// Create reverse proxy
	proxy := httputil.NewSingleHostReverseProxy(targetURL)

	// Modify the rewrite function to update the request
	proxy.Rewrite = func(req *httputil.ProxyRequest) {
		// Set target URL
		req.Out.URL = targetURL

		// Set standard reverse proxy headers
		// X-Real-IP
		if clientIP, _, err := net.SplitHostPort(req.In.RemoteAddr); err == nil {
			req.Out.Header.Set("X-Real-IP", clientIP)

			// X-Forwarded-For
			if xff := req.In.Header.Get("X-Forwarded-For"); xff != "" {
				req.Out.Header.Set("X-Forwarded-For", xff+", "+clientIP)
			} else {
				req.Out.Header.Set("X-Forwarded-For", clientIP)
			}
		}

		// X-Forwarded-Proto
		proto := "http"
		if req.In.TLS != nil {
			proto = "https"
		}
		req.Out.Header.Set("X-Forwarded-Proto", proto)

		// X-Forwarded-Host
		req.Out.Header.Set("X-Forwarded-Host", req.In.Host)

		// Support WebSocket upgrade
		if req.In.Header.Get("Upgrade") == "websocket" {
			req.Out.Header.Set("Upgrade", "websocket")
			req.Out.Header.Set("Connection", "upgrade")
		}

		// Pass through other Connection headers
		if connHeader := req.In.Header.Get("Connection"); connHeader != "" {
			req.Out.Header.Set("Connection", connHeader)
		}
	}

	return proxy, nil
}

// Todo 仍未实现合适的时机刷新缓存
// RefreshAppCache refreshes the domain to application mapping cache
func RefreshAppCache() {
	applications, err := object.GetApplications("admin")
	if err != nil {
		fmt.Printf("Error refreshing app cache: %v\n", err)
		return
	}

	appCacheMutex.Lock()
	defer appCacheMutex.Unlock()

	// Clear existing cache
	for k := range domainToAppMap {
		delete(domainToAppMap, k)
	}

	// Populate new cache
	for _, app := range applications {
		// Add primary domain
		if app.Domain != "" {
			domainToAppMap[app.Domain] = app
		}

		// Add other domains
		for _, otherDomain := range app.OtherDomains {
			if otherDomain != "" {
				domainToAppMap[otherDomain] = app
			}
		}
	}

	fmt.Printf("App cache refreshed with %d domain mappings\n", len(domainToAppMap))
}

func Start() {
	proxyHttpPort := conf.GetConfigString("proxyHttpPort")
	proxyHttpsPort := conf.GetConfigString("proxyHttpsPort")

	// Check if proxy is enabled
	if proxyHttpPort == "" && proxyHttpsPort == "" {
		fmt.Printf("Casdoor proxy not enabled (proxyHttpPort, proxyHttpsPort all empty)\n")
		return
	}

	// Initialize app cache
	RefreshAppCache()

	// Create reverse proxy handler
	reverseProxyHandler := NewReverseProxyHandler(nil)

	// Start HTTP listener if configured
	if proxyHttpPort != "" {
		go func() {
			fmt.Printf("Casdoor proxy running on: http://0.0.0.0:%s\n", proxyHttpPort)
			err := http.ListenAndServe(fmt.Sprintf(":%s", proxyHttpPort), reverseProxyHandler)
			if err != nil {
				panic(err)
			}
		}()
	}

	// Start HTTPS listener if configured
	if proxyHttpsPort != "" {
		go func() {
			fmt.Printf("Casdoor proxy running on: https://0.0.0.0:%s\n", proxyHttpsPort)
			server := &http.Server{
				Handler: reverseProxyHandler,
				Addr:    fmt.Sprintf(":%s", proxyHttpsPort),
				TLSConfig: &tls.Config{
					// Minimum TLS version 1.2, TLS 1.3 is automatically supported
					MinVersion: tls.VersionTLS12,
					// Prefer server's cipher suite order for better security
					PreferServerCipherSuites: true,
					// Secure cipher suites for TLS 1.2 (excluding 3DES to prevent Sweet32 attack)
					// TLS 1.3 cipher suites are automatically configured by Go
					CipherSuites: []uint16{
						tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
						tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
						tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
						tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
						tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
						tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
					},
					// Prefer strong elliptic curves
					CurvePreferences: []tls.CurveID{
						tls.X25519,
						tls.CurveP256,
						tls.CurveP384,
					},
				},
			}

			// start https server
			// TODO: 证书相关
			err := server.ListenAndServeTLS("", "")
			if err != nil {
				panic(err)
			}
		}()
	}
}
