package network

import (
	"context"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"os"
	"regexp"

	"github.com/smasonuk/falken-core/internal/permissions"

	"github.com/elazarl/goproxy"
)

// SandboxProxy is the permission-aware HTTP(S) proxy used by sandboxed commands.
type SandboxProxy struct {
	proxyServer *goproxy.ProxyHttpServer
	server      *http.Server
	Port        string
	CertPath    string
	PermManager *permissions.Manager
}

// NewSandboxProxy constructs a MITM proxy that consults the permissions manager
// before allowing outbound network requests from the sandbox.
func NewSandboxProxy(port string, pm *permissions.Manager, cacheDir string) (*SandboxProxy, error) {
	proxy := goproxy.NewProxyHttpServer()
	proxy.Verbose = false // Set to true if you want to debug every network request

	// Intercept HTTPS Traffic (MITM)
	// Extract the raw certificate bytes from goproxy's default CA
	if len(goproxy.GoproxyCa.Certificate) == 0 {
		return nil, fmt.Errorf("goproxy CA certificate is missing")
	}
	certBytes := goproxy.GoproxyCa.Certificate[0]

	// Encode to valid PEM format
	pemData := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certBytes,
	})

	// Ensure the cache directory exists
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create cache dir: %v", err)
	}

	certPath := cacheDir + "/proxy-ca.crt"
	if err := os.WriteFile(certPath, pemData, 0644); err != nil {
		return nil, fmt.Errorf("failed to write proxy cert: %v", err)
	}

	// Tell goproxy to MITM all connections
	proxy.OnRequest(goproxy.ReqHostMatches(regexp.MustCompile(`.*`))).
		HandleConnect(goproxy.AlwaysMitm)

	// Filter Requests based on Permissions Manager
	proxy.OnRequest().DoFunc(func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		// Ask the manager if this URL is allowed
		if !pm.CheckNetworkAccess(req.URL.String()) {
			// log.Printf("🔒 PROXY BLOCKED: %s", req.URL.String())
			return req, goproxy.NewResponse(req,
				goproxy.ContentTypeText, http.StatusForbidden,
				"Falken Sandbox Security: Access to this domain is blocked.")
		}

		// log.Printf("🌐 PROXY ALLOWED: %s", req.URL.String())
		return req, nil
	})

	return &SandboxProxy{
		proxyServer: proxy,
		Port:        port,
		CertPath:    certPath,
		PermManager: pm,
	}, nil
}

// Start begins listening and updates Port with the chosen listener port.
func (p *SandboxProxy) Start() error {
	addr := ":" + p.Port
	if p.Port == "" || p.Port == "0" {
		addr = ":0"
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	if tcpAddr, ok := listener.Addr().(*net.TCPAddr); ok {
		p.Port = fmt.Sprintf("%d", tcpAddr.Port)
	}
	p.server = &http.Server{
		Addr:    listener.Addr().String(),
		Handler: p.proxyServer,
	}
	go func() {
		if err := p.server.Serve(listener); err != nil && err != http.ErrServerClosed {
			// log.Printf("Proxy server failed: %v", err)
			//TODO: write to log
		}
	}()
	return nil
}

// Close gracefully shuts down the proxy server.
func (p *SandboxProxy) Close(ctx context.Context) error {
	if p.server == nil {
		return nil
	}
	return p.server.Shutdown(ctx)
}
