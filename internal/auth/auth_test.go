package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"btcpp-web/internal/config"
)

func TestAuthRedirectInvalidLinkRedirectsToLoginWithError(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/auth?em=not-base64&hr=also-bad&next=/dashboard/talks", nil)
	rec := httptest.NewRecorder()

	AuthRedirect(rec, req, &config.AppContext{})

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	location := rec.Header().Get("Location")
	if !strings.HasPrefix(location, "/login?") {
		t.Fatalf("Location = %q, want /login redirect", location)
	}
	if !strings.Contains(location, "next=%2Fdashboard%2Ftalks") {
		t.Fatalf("Location = %q, missing preserved next", location)
	}
	if !strings.Contains(location, "error=") {
		t.Fatalf("Location = %q, missing error flash", location)
	}
}
