package main

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
)

// Dónde escucha el server y si usa HTTPS (2026-11-26).
//
// La red del almacén es la de la casa y la usa gente de afuera: sin HTTPS,
// la contraseña del login (F-13) viajaría sin cifrar por esa red. Con
// TLS_CERT_FILE y TLS_KEY_FILE el server sirve HTTPS (el certificado se hace
// con mkcert para la IP o el nombre de la PC, ver el README). Sin ellos
// sigue en HTTP, para desarrollar.
//
// LISTEN_HOST limita dónde escucha: vacío es en toda la red; 127.0.0.1 es
// solo esta PC (nadie de la red llega).

var errHalfTLS = errors.New("TLS_CERT_FILE and TLS_KEY_FILE go together: set both or neither")

// serverConfig es cómo arranca el server.
type serverConfig struct {
	Addr string      // host:puerto
	TLS  *tls.Config // nil: HTTP
}

// HTTPS dice si el server sirve HTTPS (la cookie de sesión va Secure).
func (c serverConfig) HTTPS() bool { return c.TLS != nil }

// loadServerConfig lee LISTEN_HOST, PORT (8080 si falta), TLS_CERT_FILE y
// TLS_KEY_FILE. El certificado se carga acá, así un archivo que falta o no
// corresponde a la clave frena el arranque en vez de fallar al primer
// pedido.
func loadServerConfig(getenv func(string) string) (serverConfig, error) {
	port := getenv("PORT")
	if port == "" {
		port = "8080"
	}
	cfg := serverConfig{Addr: net.JoinHostPort(getenv("LISTEN_HOST"), port)}

	certFile, keyFile := getenv("TLS_CERT_FILE"), getenv("TLS_KEY_FILE")
	switch {
	case certFile == "" && keyFile == "":
		return cfg, nil
	case certFile == "" || keyFile == "":
		return serverConfig{}, errHalfTLS
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return serverConfig{}, fmt.Errorf("load TLS certificate: %w", err)
	}
	cfg.TLS = &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}
	return cfg, nil
}
