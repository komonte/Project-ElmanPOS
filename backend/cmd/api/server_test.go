package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeCert escribe un certificado autofirmado para 127.0.0.1 y su clave en
// dir, y devuelve las rutas.
func writeCert(t *testing.T, dir, name string) (certFile, keyFile string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "elmanpos test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certFile, keyFile = filepath.Join(dir, name+".pem"), filepath.Join(dir, name+"-key.pem")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile
}

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadServerConfig(t *testing.T) {
	dir := t.TempDir()
	cert, key := writeCert(t, dir, "a")
	otherCert, _ := writeCert(t, dir, "b")

	t.Run("defaults: HTTP on every interface, port 8080", func(t *testing.T) {
		cfg, err := loadServerConfig(env(nil))
		if err != nil || cfg.Addr != ":8080" || cfg.HTTPS() {
			t.Errorf("got %+v, %v", cfg, err)
		}
	})
	t.Run("only this PC", func(t *testing.T) {
		cfg, err := loadServerConfig(env(map[string]string{"LISTEN_HOST": "127.0.0.1", "PORT": "9000"}))
		if err != nil || cfg.Addr != "127.0.0.1:9000" {
			t.Errorf("got %+v, %v", cfg, err)
		}
	})
	t.Run("HTTPS", func(t *testing.T) {
		cfg, err := loadServerConfig(env(map[string]string{"TLS_CERT_FILE": cert, "TLS_KEY_FILE": key}))
		if err != nil || !cfg.HTTPS() || len(cfg.TLS.Certificates) != 1 || cfg.TLS.MinVersion != tls.VersionTLS12 {
			t.Errorf("got %+v, %v", cfg, err)
		}
	})
	t.Run("only one of the two files", func(t *testing.T) {
		for _, m := range []map[string]string{{"TLS_CERT_FILE": cert}, {"TLS_KEY_FILE": key}} {
			if _, err := loadServerConfig(env(m)); !errors.Is(err, errHalfTLS) {
				t.Errorf("%v: got %v, want %v", m, err, errHalfTLS)
			}
		}
	})
	t.Run("missing file", func(t *testing.T) {
		_, err := loadServerConfig(env(map[string]string{"TLS_CERT_FILE": filepath.Join(dir, "nope.pem"), "TLS_KEY_FILE": key}))
		if err == nil {
			t.Error("want an error")
		}
	})
	t.Run("certificate and key do not match", func(t *testing.T) {
		_, err := loadServerConfig(env(map[string]string{"TLS_CERT_FILE": otherCert, "TLS_KEY_FILE": key}))
		if err == nil {
			t.Error("want an error")
		}
	})
}

// TestServeTLS levanta un server con la configuración y le habla por HTTPS,
// confiando solo en ese certificado (como un equipo con la CA de mkcert).
func TestServeTLS(t *testing.T) {
	cert, key := writeCert(t, t.TempDir(), "srv")
	cfg, err := loadServerConfig(env(map[string]string{"LISTEN_HOST": "127.0.0.1", "PORT": "0",
		"TLS_CERT_FILE": cert, "TLS_KEY_FILE": key}))
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{TLSConfig: cfg.TLS, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok")
	})}
	go srv.ServeTLS(ln, "", "")
	t.Cleanup(func() { srv.Close() })

	pemBytes, err := os.ReadFile(cert)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(pemBytes)
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}}

	res, err := client.Get("https://" + ln.Addr().String() + "/")
	if err != nil {
		t.Fatalf("https: %v", err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if string(body) != "ok" {
		t.Errorf("got %q", body)
	}

	// Por HTTP plano no contesta la página.
	res, err = http.Get("http://" + ln.Addr().String() + "/")
	if err == nil {
		body, _ = io.ReadAll(res.Body)
		res.Body.Close()
		if string(body) == "ok" {
			t.Error("plain HTTP must not get the page")
		}
	}
}
