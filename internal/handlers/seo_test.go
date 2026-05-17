package handlers

import (
	"os"
	"path/filepath"
	"testing"

	"btcpp-web/internal/config"
)

func TestTemplatesParse(t *testing.T) {
	repoRoot := findRepoRoot(t)
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() = %v", err)
	}
	if err := os.Chdir(repoRoot); err != nil {
		t.Fatalf("Chdir(%q) = %v", repoRoot, err)
	}
	defer func() {
		if err := os.Chdir(oldWD); err != nil {
			t.Fatalf("restore working directory: %v", err)
		}
	}()

	ctx := &config.AppContext{}
	if err := loadTemplates(ctx); err != nil {
		t.Fatalf("loadTemplates() = %v", err)
	}
}

func findRepoRoot(t *testing.T) string {
	t.Helper()

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() = %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(wd, "templates")); err == nil {
			return wd
		}
		next := filepath.Dir(wd)
		if next == wd {
			t.Fatalf("could not find repo root from %q", wd)
		}
		wd = next
	}
}

func TestShouldNoIndexPath(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/", false},
		{"/berlin26", false},
		{"/talk/berlin26", false},
		{"/volunteer/berlin26", false},
		{"/auth", true},
		{"/dashboard", true},
		{"/dashboard/berlin26/edit", true},
		{"/logout", true},
		{"/invite-speaker/proposal-id", true},
		{"/ticket/abc", true},
		{"/tix/ticket+default+fiat", true},
		{"/conf/berlin26/success", true},
		{"/berlin26/admin/applicants", true},
		{"/berlin26/volcoord", true},
		{"/berlin26/success", true},
		{"/berlin26/talk/session/calendar.ics", true},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := shouldNoIndexPath(tt.path); got != tt.want {
				t.Fatalf("shouldNoIndexPath(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestConfSocialImage(t *testing.T) {
	tests := []struct {
		tag  string
		card string
		want string
	}{
		{"atx22", "standard", SEOHost + "/static/img/atxpromo.png"},
		{"berlin24", "twitter", SEOHost + "/static/img/berlin24_promo.png"},
		{"berlin25", "standard", SEOHost + "/static/img/berlin25/og_card_standard.png"},
		{"berlin25", "twitter", SEOHost + "/static/img/berlin25/og_card_twitter.png"},
	}

	for _, tt := range tests {
		t.Run(tt.tag+"/"+tt.card, func(t *testing.T) {
			if got := confSocialImage(tt.tag, tt.card); got != tt.want {
				t.Fatalf("confSocialImage(%q, %q) = %q, want %q", tt.tag, tt.card, got, tt.want)
			}
		})
	}
}

func TestAbsoluteSEOURL(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"", SEOHost + "/"},
		{"/talk", SEOHost + "/talk"},
		{"talk", SEOHost + "/talk"},
		{"https://example.com/card.png", "https://example.com/card.png"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := absoluteSEOURL(tt.path); got != tt.want {
				t.Fatalf("absoluteSEOURL(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}
