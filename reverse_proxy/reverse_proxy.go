package reverse_proxy

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/casdoor/casdoor/conf"
	"github.com/casdoor/casdoor/object"
	"github.com/casdoor/casdoor/util"
	"github.com/casvisor/casvisor-go-sdk/casvisorsdk"
)

var (
	// domainToAppMap maps domain names to applications
	domainToAppMap = make(map[string]*object.Application)
	// appCacheMutex protects domainToAppMap from concurrent access
	appCacheMutex sync.RWMutex
	// logFile is the file handle for reverse proxy logs
	logFile *os.File
	// logMutex protects logFile from concurrent access
	logMutex sync.Mutex
)

type ReverseProxyHandler struct {
	Next http.Handler
}

func NewReverseProxyHandler(next http.Handler) *ReverseProxyHandler {
	return &ReverseProxyHandler{Next: next}
}

func (h *ReverseProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Extract client IP
	clientIP := ""
	if ip, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		clientIP = ip
	} else {
		clientIP = r.RemoteAddr
	}

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

	// Record request start time
	startTime := time.Now()

	// Create response recorder to capture status code
	recorder := newResponseRecorder(w)

	// Serve the request
	proxy.ServeHTTP(recorder, r)

	// Calculate request duration
	duration := time.Since(startTime)

	// Log the request and response
	logRequest(clientIP, r, app.UpstreamHost, recorder.statusCode, duration)
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

// func createReverseProxy(upstreamHost string) (*httputil.ReverseProxy, error) {
// 	// Parse the upstream host URL
// 	targetURL, err := url.Parse(upstreamHost)
// 	if err != nil {
// 		return nil, err
// 	}

// 	// Create reverse proxy
// 	proxy := httputil.NewSingleHostReverseProxy(targetURL)

// 	// Modify the rewrite function to update the request
// 	proxy.Rewrite = func(req *httputil.ProxyRequest) {
// 		// Set target URL
// 		req.Out.URL = targetURL

// 		// Set standard reverse proxy headers
// 		// X-Real-IP
// 		if clientIP, _, err := net.SplitHostPort(req.In.RemoteAddr); err == nil {
// 			req.Out.Header.Set("X-Real-IP", clientIP)

// 			// X-Forwarded-For
// 			if xff := req.In.Header.Get("X-Forwarded-For"); xff != "" {
// 				req.Out.Header.Set("X-Forwarded-For", xff+", "+clientIP)
// 			} else {
// 				req.Out.Header.Set("X-Forwarded-For", clientIP)
// 			}
// 		}

// 		// X-Forwarded-Proto
// 		proto := "http"
// 		if req.In.TLS != nil {
// 			proto = "https"
// 		}
// 		req.Out.Header.Set("X-Forwarded-Proto", proto)

// 		// X-Forwarded-Host
// 		req.Out.Header.Set("X-Forwarded-Host", req.In.Host)

// 		// Support WebSocket upgrade
// 		if req.In.Header.Get("Upgrade") == "websocket" {
// 			req.Out.Header.Set("Upgrade", "websocket")
// 			req.Out.Header.Set("Connection", "upgrade")
// 		}

// 		// Pass through other Connection headers
// 		if connHeader := req.In.Header.Get("Connection"); connHeader != "" {
// 			req.Out.Header.Set("Connection", connHeader)
// 		}
// 	}

// 	return proxy, nil
// }

// reverse_proxy/reverse_proxy.go

func createReverseProxy(upstreamHost string) (*httputil.ReverseProxy, error) {
    targetURL, err := url.Parse(upstreamHost)
    if err != nil {
        return nil, err
    }

    // 1. 创建基础代理
    proxy := httputil.NewSingleHostReverseProxy(targetURL)

    // 2. 【核心修复】清除默认生成的 Director，因为我们要用 Rewrite
    proxy.Director = nil 

    // 3. 设置 Rewrite 逻辑
    proxy.Rewrite = func(req *httputil.ProxyRequest) {
        // 设置目标 URL
        req.SetURL(targetURL)
        
        // 确保 Host 头部被正确重写为上游主机的地址
        req.Out.Host = targetURL.Host

        // 填充标准代理头（使用中文注释：这里确保后端能拿到真实 IP）
        if clientIP, _, err := net.SplitHostPort(req.In.RemoteAddr); err == nil {
            req.Out.Header.Set("X-Real-IP", clientIP)
            req.Out.Header.Set("X-Forwarded-For", clientIP)
        }
        
        req.Out.Header.Set("X-Forwarded-Proto", "http")
    }

    // 4. 设置错误处理器，方便以后调试
    proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
        fmt.Printf("反向代理网络传输错误: %v\n", err)
        w.WriteHeader(http.StatusBadGateway)
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

// initLogFile initializes the reverse proxy log file
func initLogFile() {
	logDir := "logs"
	logPath := filepath.Join(logDir, "casdoor_revser_proxy.log")

	// Create logs directory if it doesn't exist
	if _, err := os.Stat(logDir); os.IsNotExist(err) {
		if err := os.Mkdir(logDir, 0755); err != nil {
			fmt.Printf("Error creating logs directory: %v\n", err)
			return
		}
	}

	// Open or create log file
	var err error
	logFile, err = os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Printf("Error opening log file: %v\n", err)
		return
	}

	fmt.Printf("Reverse proxy log initialized: %s\n", logPath)
}

// logRequest logs proxy requests and responses
func logRequest(clientIP string, r *http.Request, upstreamHost string, statusCode int, duration time.Duration) {
	// Skip Uptime-Kuma requests
	if strings.Contains(r.UserAgent(), "Uptime-Kuma") {
		return
	}

	// Log to console
	logMsg := fmt.Sprintf("[%s] %s %s %s -> %s %d %v %s",
		time.Now().Format("2006-01-02 15:04:05"),
		r.Method,
		r.Host,
		r.RequestURI,
		upstreamHost,
		statusCode,
		duration,
		clientIP,
	)
	fmt.Println(logMsg)

	// Log to file
	logMutex.Lock()
	defer logMutex.Unlock()

	if logFile != nil {
		_, err := logFile.WriteString(logMsg + "\n")
		if err != nil {
			fmt.Printf("Error writing to log file: %v\n", err)
		}
		logFile.Sync()
	}

	// Record to database
	record := &casvisorsdk.Record{
		Owner:        "admin",
		Organization: "admin",
		Name:         util.GenerateId(),
		CreatedTime:  util.GetCurrentTime(),
		ClientIp:     clientIP,
		User:         "",
		Method:       r.Method,
		RequestUri:   r.RequestURI,
		Action:       fmt.Sprintf("reverse-proxy:%s", r.Host),
		Language:     "",
		Object:       fmt.Sprintf("{\"host\":\"%s\",\"upstreamHost\":\"%s\",\"duration\":%d}", r.Host, upstreamHost, duration.Milliseconds()),
		Response:     fmt.Sprintf("{\"statusCode\":%d,\"duration\":%d}", statusCode, duration.Milliseconds()),
		StatusCode:   statusCode,
		IsTriggered:  false,
	}
	object.AddRecord(record)
}

// responseRecorder wraps http.ResponseWriter to capture status code and body
type responseRecorder struct {
	http.ResponseWriter
	statusCode int
	body       []byte
}

func newResponseRecorder(w http.ResponseWriter) *responseRecorder {
	return &responseRecorder{
		ResponseWriter: w,
		statusCode:     http.StatusOK,
	}
}

func (r *responseRecorder) WriteHeader(code int) {
	r.statusCode = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	r.body = append(r.body, b...)
	return r.ResponseWriter.Write(b)
}

func Start() {
	proxyHttpPort := conf.GetConfigString("proxyHttpPort")
	proxyHttpsPort := conf.GetConfigString("proxyHttpsPort")

	// Check if proxy is enabled
	if proxyHttpPort == "" && proxyHttpsPort == "" {
		fmt.Printf("Casdoor proxy not enabled (proxyHttpPort, proxyHttpsPort all empty)\n")
		return
	}

	// Initialize log file
	initLogFile()

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
			// 建议：你需要实现 tls.Config 中的 GetCertificate 回调函数，根据 ClientHelloInfo.ServerName 动态从数据库或缓存中获取对应的 SSL 证书。
			err := server.ListenAndServeTLS("", "")
			if err != nil {
				panic(err)
			}
		}()
	}
}
