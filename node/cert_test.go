//go:build integration

// Legacy certificate test writes fixed paths; excluded from hermetic CI.
package node

import "testing"

func Test_generateSelfSslCertificate(t *testing.T) {
	t.Log(generateSelfSslCertificate("domain.com", "1.pem", "1.key"))
}
