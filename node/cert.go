package node

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"time"

	"github.com/shmily2-1/V2bX-2/common/file"
	log "github.com/sirupsen/logrus"
)

func (c *Controller) renewCertTask() error {
	l, err := NewLego(c.CertConfig)
	if err != nil {
		log.WithField("tag", c.tag).Info("new lego error: ", err)
		return nil
	}
	err = l.RenewCert()
	if err != nil {
		log.WithField("tag", c.tag).Info("renew cert error: ", err)
		return nil
	}
	return nil
}

func (c *Controller) requestCert() error {
	switch c.CertConfig.CertMode {
	case "none", "":
	case "file":
		if c.CertConfig.CertFile == "" || c.CertConfig.KeyFile == "" {
			return fmt.Errorf("cert file path or key file path not exist")
		}
		return validateCertificate(c.CertConfig.CertFile, c.CertConfig.KeyFile, c.CertConfig.CertDomain)
	case "dns", "http":
		if c.CertConfig.CertFile == "" || c.CertConfig.KeyFile == "" {
			return fmt.Errorf("cert file path or key file path not exist")
		}
		if file.IsExist(c.CertConfig.CertFile) && file.IsExist(c.CertConfig.KeyFile) {
			return validateCertificate(c.CertConfig.CertFile, c.CertConfig.KeyFile, c.CertConfig.CertDomain)
		}
		l, err := NewLego(c.CertConfig)
		if err != nil {
			return fmt.Errorf("create lego object error: %s", err)
		}
		err = l.CreateCert()
		if err != nil {
			return fmt.Errorf("create lego cert error: %s", err)
		}
		if err := validateCertificate(c.CertConfig.CertFile, c.CertConfig.KeyFile, c.CertConfig.CertDomain); err != nil {
			return fmt.Errorf("validate lego cert error: %s", err)
		}
	case "self":
		if c.CertConfig.CertFile == "" || c.CertConfig.KeyFile == "" {
			return fmt.Errorf("cert file path or key file path not exist")
		}
		if file.IsExist(c.CertConfig.CertFile) && file.IsExist(c.CertConfig.KeyFile) {
			return nil
		}
		err := generateSelfSslCertificate(
			c.CertConfig.CertDomain,
			c.CertConfig.CertFile,
			c.CertConfig.KeyFile)
		if err != nil {
			return fmt.Errorf("generate self cert error: %s", err)
		}
		if err := validateCertificate(c.CertConfig.CertFile, c.CertConfig.KeyFile, c.CertConfig.CertDomain); err != nil {
			return fmt.Errorf("validate self cert error: %s", err)
		}
	default:
		return fmt.Errorf("unsupported certmode: %s", c.CertConfig.CertMode)
	}
	return nil
}

func generateSelfSslCertificate(domain, certPath, keyPath string) error {
	if err := checkPath(certPath); err != nil {
		return err
	}
	if err := checkPath(keyPath); err != nil {
		return err
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}
	tmpl := &x509.Certificate{
		Version:      3,
		SerialNumber: big.NewInt(time.Now().Unix()),
		Subject: pkix.Name{
			CommonName: domain,
		},
		DNSNames:              []string{domain},
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(30, 0, 0),
	}
	cert, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	if err != nil {
		return err
	}
	if err = os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert}), 0644); err != nil {
		return err
	}
	return writePrivateFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
}

func validateCertificate(certPath, keyPath, domain string) error {
	pair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return fmt.Errorf("load certificate/key: %w", err)
	}
	cert, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return err
	}
	if time.Now().Before(cert.NotBefore) || time.Now().After(cert.NotAfter) {
		return fmt.Errorf("certificate outside validity period; renew it before starting")
	}
	if domain != "" {
		return cert.VerifyHostname(domain)
	}
	return nil
}
