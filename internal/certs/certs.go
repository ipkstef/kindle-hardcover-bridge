// Package certs holds a bundled set of trusted root CAs.
// Old Kindle firmware has old or missing system CAs, so we never use them.
//
// cacert.pem is exported from golang.org/x/crypto/x509roots/fallback
// (Mozilla roots). Constrained roots are left out.
package certs

import (
	"crypto/x509"
	_ "embed"
)

//go:embed cacert.pem
var pemData []byte

// Pool returns a new pool with only the bundled roots.
func Pool() *x509.CertPool {
	p := x509.NewCertPool()
	if !p.AppendCertsFromPEM(pemData) {
		panic("certs: no roots in bundled cacert.pem")
	}
	return p
}
