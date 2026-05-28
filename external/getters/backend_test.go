package getters

import (
	"strings"
	"testing"

	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
)

func TestUsePostgresBackend(t *testing.T) {
	tests := []struct {
		name string
		ctx  *config.AppContext
		want bool
	}{
		{name: "nil context", ctx: nil, want: false},
		{name: "nil env", ctx: &config.AppContext{}, want: false},
		{name: "empty backend", ctx: appContextWithBackend(""), want: false},
		{name: "notion backend", ctx: appContextWithBackend(dataBackendNotion), want: false},
		{name: "postgres backend", ctx: appContextWithBackend(dataBackendPostgres), want: true},
		{name: "postgres backend mixed case", ctx: appContextWithBackend("Postgres"), want: true},
		{name: "postgres backend with spaces", ctx: appContextWithBackend(" postgres "), want: true},
		{name: "unknown backend", ctx: appContextWithBackend("sqlite"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := usePostgresBackend(tt.ctx); got != tt.want {
				t.Fatalf("usePostgresBackend() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestUnsupportedPostgresBackend(t *testing.T) {
	err := unsupportedPostgresBackend("ListOrgs")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "ListOrgs") {
		t.Fatalf("expected function name in error, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "postgres backend not implemented") {
		t.Fatalf("expected backend message in error, got %q", err.Error())
	}
}

func appContextWithBackend(backend string) *config.AppContext {
	return &config.AppContext{Env: &types.EnvConfig{DataBackend: backend}}
}
