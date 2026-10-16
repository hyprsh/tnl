package caddyx

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"path"
	"strings"
	"time"

	"github.com/caddyserver/certmagic"
)

// Caddy keeps a certificate at certificates/<issuer>/<dir>/<dir>.crt, where dir
// is the name with a leading "*" written "wildcard_". These helpers read them.

const certsKey = "certificates"

func certDir(name string) string { return strings.ReplaceAll(name, "*", "wildcard_") }

// parseNotAfter is the expiry of the first certificate in the PEM; the zero
// time if it holds none.
func parseNotAfter(b []byte) time.Time {
	block, _ := pem.Decode(b)
	if block == nil {
		return time.Time{}
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return time.Time{}
	}
	return leaf.NotAfter
}

// loadNotAfter reads the certificate at a storage key and returns its expiry,
// or the zero time.
func loadNotAfter(st certmagic.Storage, key string) time.Time {
	if st == nil || key == "" {
		return time.Time{}
	}
	b, err := st.Load(context.Background(), key)
	if err != nil {
		return time.Time{}
	}
	return parseNotAfter(b)
}

// storedNotAfter is the newest expiry among the certificates storage holds for
// exactly this name, expired or not; the zero time if there is none.
func storedNotAfter(st certmagic.Storage, name string) (notAfter time.Time) {
	if st == nil {
		return
	}
	issuers, err := st.List(context.Background(), certsKey, false)
	if err != nil {
		return
	}
	dir := certDir(name)
	for _, issuer := range issuers {
		if t := loadNotAfter(st, path.Join(issuer, dir, dir+".crt")); t.After(notAfter) {
			notAfter = t
		}
	}
	return
}

// storedWildcardZones lists the Zones storage holds a wildcard certificate
// for, expired or not.
func storedWildcardZones(st certmagic.Storage) []string {
	issuers, err := st.List(context.Background(), certsKey, false)
	if err != nil {
		return nil
	}
	var zones []string
	seen := map[string]bool{}
	for _, issuer := range issuers {
		dirs, _ := st.List(context.Background(), issuer, false)
		for _, d := range dirs {
			if zone, ok := strings.CutPrefix(path.Base(d), "wildcard_."); ok && !seen[zone] {
				seen[zone] = true
				zones = append(zones, zone)
			}
		}
	}
	return zones
}
