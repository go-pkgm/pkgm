package main

import (
	"crypto/tls"
	"crypto/x509"
	_ "embed"
	"net/http"
	"time"
)

// cacert is the Mozilla CA bundle, compiled into the binary so TLS works on a
// bare `FROM scratch` image with no system trust store.
//
//go:embed cacert.pem
var cacert []byte

// httpClient is an HTTP client that trusts the embedded CA bundle (falling
// back to the system pool if, somehow, the embed is empty).
var httpClient = newHTTPClient()

func newHTTPClient() *http.Client {
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	pool.AppendCertsFromPEM(cacert)
	return &http.Client{
		Timeout: 5 * time.Minute,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool},
		},
	}
}
