package control

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"math/big"
	"meldnet/internal/securefs"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

func certificate(dir string) (tls.Certificate, string, error) {
	path := filepath.Join(dir, "primary-tls.pem")
	b, err := securefs.Read(path)
	if errors.Is(err, os.ErrNotExist) {
		pub, key, e := ed25519.GenerateKey(rand.Reader)
		if e != nil {
			return tls.Certificate{}, "", e
		}
		serial, e := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
		if e != nil {
			return tls.Certificate{}, "", e
		}
		t := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "Meldnet primary"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(5, 0, 0), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
		der, e := x509.CreateCertificate(rand.Reader, t, t, pub, key)
		if e != nil {
			return tls.Certificate{}, "", e
		}
		priv, e := x509.MarshalPKCS8PrivateKey(key)
		if e != nil {
			return tls.Certificate{}, "", e
		}
		b = append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: priv})...)
		err = securefs.Write(path, b)
	}
	if err != nil {
		return tls.Certificate{}, "", err
	}
	c, err := tls.X509KeyPair(b, b)
	if err != nil {
		return c, "", errors.New("invalid primary TLS identity")
	}
	h := sha256.Sum256(c.Certificate[0])
	return c, hex.EncodeToString(h[:]), nil
}
func pinnedClient(pin string) *http.Client {
	return &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect refused") }, Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, InsecureSkipVerify: true, // Certificate identity is authenticated by the enrollment pin below.
		VerifyConnection: func(s tls.ConnectionState) error {
			if len(s.PeerCertificates) == 0 {
				return errors.New("missing certificate")
			}
			c := s.PeerCertificates[0]
			h := sha256.Sum256(c.Raw)
			if hex.EncodeToString(h[:]) != pin || time.Now().Before(c.NotBefore) || time.Now().After(c.NotAfter) {
				return errors.New("primary certificate pin or validity mismatch")
			}
			return nil
		}}, DisableCompression: true}}
}
