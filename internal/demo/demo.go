// Package demo is a small target system for trying the tool: an orders REST
// API behind an OAuth2 token endpoint, a gRPC health service with
// reflection, and an HTTPS endpoint that requires a client certificate.
package demo

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

const (
	ClientID     = "demo-client"
	ClientSecret = "demo-secret"
	token        = "demo-access-token"
)

type order struct {
	ID       string `json:"id"`
	Item     string `json:"item"`
	Quantity int    `json:"quantity"`
	Status   string `json:"status"`
}

func send(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// Handler returns the orders API.
func Handler() http.Handler {
	var mu sync.Mutex
	orders := map[string]order{}
	next := 1000
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		send(w, 200, map[string]string{"status": "up"})
	})
	mux.HandleFunc("POST /oauth/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		id, secret := r.Form.Get("client_id"), r.Form.Get("client_secret")
		if u, p, ok := r.BasicAuth(); ok {
			id, secret = u, p
		}
		if r.Form.Get("grant_type") != "client_credentials" || id != ClientID || secret != ClientSecret {
			send(w, 401, map[string]string{"error": "invalid_client"})
			return
		}
		send(w, 200, map[string]any{"access_token": token, "token_type": "Bearer", "expires_in": 3600})
	})
	authed := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer "+token {
				send(w, 401, map[string]string{"error": "unauthorized"})
				return
			}
			h(w, r)
		}
	}
	mux.HandleFunc("POST /orders", authed(func(w http.ResponseWriter, r *http.Request) {
		var o order
		if err := json.NewDecoder(r.Body).Decode(&o); err != nil {
			send(w, 400, map[string]string{"error": "body must be JSON"})
			return
		}
		if o.Item == "" || o.Quantity <= 0 {
			send(w, 400, map[string]string{"error": "item is required and quantity must be positive"})
			return
		}
		mu.Lock()
		next++
		o.ID, o.Status = strconv.Itoa(next), "created"
		orders[o.ID] = o
		mu.Unlock()
		w.Header().Set("Location", "/orders/"+o.ID)
		send(w, 201, o)
	}))
	mux.HandleFunc("GET /orders/{id}", authed(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		o, ok := orders[r.PathValue("id")]
		mu.Unlock()
		if !ok {
			send(w, 404, map[string]string{"error": "order not found"})
			return
		}
		send(w, 200, o)
	}))
	mux.HandleFunc("DELETE /orders/{id}", authed(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		_, ok := orders[r.PathValue("id")]
		delete(orders, r.PathValue("id"))
		mu.Unlock()
		if !ok {
			send(w, 404, map[string]string{"error": "order not found"})
			return
		}
		w.WriteHeader(204)
	}))
	return mux
}

// GRPCServer returns a gRPC server exposing the standard health service
// (service "orders" is SERVING) and server reflection.
func GRPCServer() *grpc.Server {
	s := grpc.NewServer()
	h := health.NewServer()
	h.SetServingStatus("orders", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(s, h)
	reflection.Register(s)
	return s
}

// Certs is a throwaway PKI: one CA, a server certificate for localhost and
// a client certificate, all PEM encoded.
type Certs struct {
	CA, ServerCert, ServerKey, ClientCert, ClientKey []byte
}

func keyPEM(k *ecdsa.PrivateKey) []byte {
	b, _ := x509.MarshalPKCS8PrivateKey(k)
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: b})
}

func certPEM(der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

// NewCerts generates the demo PKI.
func NewCerts() (*Certs, error) {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial := func() *big.Int { n, _ := rand.Int(rand.Reader, big.NewInt(1<<62)); return n }
	now := time.Now().Add(-time.Hour)
	caTpl := &x509.Certificate{SerialNumber: serial(), Subject: pkix.Name{CommonName: "Testplane Demo CA"}, NotBefore: now, NotAfter: now.AddDate(1, 0, 0),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	caDER, err := x509.CreateCertificate(rand.Reader, caTpl, caTpl, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, err
	}
	issue := func(cn string, usage x509.ExtKeyUsage, server bool) ([]byte, []byte, error) {
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return nil, nil, err
		}
		tpl := &x509.Certificate{SerialNumber: serial(), Subject: pkix.Name{CommonName: cn}, NotBefore: now, NotAfter: now.AddDate(1, 0, 0),
			KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage}}
		if server {
			tpl.DNSNames = []string{"localhost"}
			tpl.IPAddresses = []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}
		}
		der, err := x509.CreateCertificate(rand.Reader, tpl, caTpl, &k.PublicKey, caKey)
		if err != nil {
			return nil, nil, err
		}
		return certPEM(der), keyPEM(k), nil
	}
	c := &Certs{CA: certPEM(caDER)}
	if c.ServerCert, c.ServerKey, err = issue("localhost", x509.ExtKeyUsageServerAuth, true); err != nil {
		return nil, err
	}
	if c.ClientCert, c.ClientKey, err = issue("testplane-demo-client", x509.ExtKeyUsageClientAuth, false); err != nil {
		return nil, err
	}
	return c, nil
}

// WriteClientFiles saves what a test client needs into dir.
func (c *Certs) WriteClientFiles(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for name, data := range map[string][]byte{"demo-client.crt": c.ClientCert, "demo-client.key": c.ClientKey, "demo-ca.crt": c.CA} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			return err
		}
	}
	return nil
}

// ServerTLS is a TLS config that demands a client certificate from the CA.
func (c *Certs) ServerTLS() (*tls.Config, error) {
	pair, err := tls.X509KeyPair(c.ServerCert, c.ServerKey)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(c.CA)
	return &tls.Config{Certificates: []tls.Certificate{pair}, ClientCAs: pool, ClientAuth: tls.RequireAndVerifyClientCert, MinVersion: tls.VersionTLS12}, nil
}

// SecureHandler answers on the mTLS listener.
func SecureHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /secure/ping", func(w http.ResponseWriter, r *http.Request) {
		cn := ""
		if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
			cn = r.TLS.PeerCertificates[0].Subject.CommonName
		}
		send(w, 200, map[string]string{"pong": "true", "client": cn})
	})
	return mux
}

// Run starts all three listeners and blocks. certsDir, when set, receives
// the client certificate, key and CA for the mTLS endpoint.
func Run(httpAddr, grpcAddr, mtlsAddr, certsDir string) error {
	errc := make(chan error, 3)
	go func() { errc <- http.ListenAndServe(httpAddr, Handler()) }()
	lis, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		return err
	}
	go func() { errc <- GRPCServer().Serve(lis) }()
	fmt.Printf("demo orders API   http://%s  (token: POST /oauth/token, client %s / %s)\n", display(httpAddr), ClientID, ClientSecret)
	fmt.Printf("demo gRPC         %s  (plaintext, reflection, grpc.health.v1.Health)\n", display(grpcAddr))
	if mtlsAddr != "" {
		certs, err := NewCerts()
		if err != nil {
			return err
		}
		cfg, err := certs.ServerTLS()
		if err != nil {
			return err
		}
		if certsDir != "" {
			if err := certs.WriteClientFiles(certsDir); err != nil {
				return err
			}
			fmt.Printf("demo client certs written to %s\n", certsDir)
		}
		srv := &http.Server{Addr: mtlsAddr, Handler: SecureHandler(), TLSConfig: cfg}
		go func() { errc <- srv.ListenAndServeTLS("", "") }()
		fmt.Printf("demo mTLS API     https://%s/secure/ping  (client certificate required)\n", display(mtlsAddr))
	}
	return <-errc
}

func display(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "localhost" + addr
	}
	return addr
}
