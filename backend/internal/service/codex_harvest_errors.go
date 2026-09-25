package service

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"strings"
	"syscall"
)

// Only controlled categories may cross the admin/log boundary. Never retain an
// upstream error string: it may contain a URL, proxy credentials or response data.
type codexMintError struct{ kind, detail string }

func (e *codexMintError) Error() string { return e.detail }

func mintRouteError(detail string) error {
	return &codexMintError{kind: "invalid_route", detail: detail}
}

func mintTransportError(err error) error {
	var safe *codexMintError
	if errors.As(err, &safe) {
		return safe
	}
	category := "transport_error"
	var dns *net.DNSError
	var certificate *tls.CertificateVerificationError
	var unknownCA x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var netErr net.Error
	switch {
	case errors.Is(err, context.Canceled):
		category = "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		category = "timeout"
	case errors.As(err, &dns):
		category = "dns_error"
	case errors.As(err, &certificate), errors.As(err, &unknownCA), errors.As(err, &hostname):
		category = "tls_certificate_error"
	case errors.Is(err, syscall.ECONNREFUSED):
		category = "connection_refused"
	case errors.Is(err, syscall.ECONNRESET), errors.Is(err, syscall.EPIPE), errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		category = "connection_closed"
	case errors.As(err, &netErr) && netErr.Timeout():
		category = "timeout"
	case err != nil && strings.Contains(strings.ToLower(err.Error()), "tls:"):
		category = "tls_error"
	}
	return &codexMintError{kind: "network_error", detail: "mint transport: " + category}
}

func safeCodexHarvestError(err error) string {
	if err == nil {
		return ""
	}
	var safe *codexMintError
	if errors.As(err, &safe) {
		return safe.Error()
	}
	return mintTransportError(err).Error()
}
