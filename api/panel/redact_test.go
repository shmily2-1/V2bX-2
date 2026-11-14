package panel

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
)

func TestPanelErrorsRedactRawAndEscapedCredentials(t *testing.T) {
	key := "fixture-key&+value"
	message := fmt.Sprintf("GET /path?token=%s failed; body=%s", url.QueryEscape(key), key)
	clean := redactError(message, key)
	if strings.Contains(clean, key) || strings.Contains(clean, url.QueryEscape(key)) {
		t.Fatal("credential appeared in error")
	}
	if !strings.Contains(clean, "[redacted]") {
		t.Fatal("missing redaction marker")
	}
}
