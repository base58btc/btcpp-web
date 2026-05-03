package helpers

import (
	"strings"
	"testing"
)

func TestSuccessApp(t *testing.T) {
	out := SuccessApp("Your message has been sent!")

	musts := []string{
		"Your message has been sent!", // user-visible message
		"form_message-success",        // success container hook
		"text-green-800",              // styled as green flash
		"role=\"status\"",             // a11y live region
		"f.reset()",                   // form is reset on success
	}
	for _, want := range musts {
		if !strings.Contains(out, want) {
			t.Errorf("SuccessApp output missing %q\nfull output:\n%s", want, out)
		}
	}
}

func TestSuccessApp_EscapesHTML(t *testing.T) {
	// SuccessApp passes the message through fmt.Sprintf and into a div, so the
	// caller is responsible for ensuring the message is plain text. This test
	// pins the current behavior: special characters are passed through verbatim.
	// If we ever start escaping, update this test alongside.
	out := SuccessApp("hi <b>there</b>")
	if !strings.Contains(out, "hi <b>there</b>") {
		t.Errorf("expected message passed through; got:\n%s", out)
	}
}

func TestErrApp(t *testing.T) {
	out := ErrApp("Bad captcha.", "speak")

	musts := []string{
		"Bad captcha.",
		"form_message-error",
		"text-red-700",
		"speak@btcpp.dev",
		"mailto:speak@btcpp.dev",
	}
	for _, want := range musts {
		if !strings.Contains(out, want) {
			t.Errorf("ErrApp output missing %q\nfull output:\n%s", want, out)
		}
	}
}

func TestErrSpeakerApp_UsesSpeakEmail(t *testing.T) {
	out := ErrSpeakerApp("nope")
	if !strings.Contains(out, "speak@btcpp.dev") {
		t.Errorf("ErrSpeakerApp should route to speak@; got:\n%s", out)
	}
}

func TestErrVolApp_UsesVolunteerEmail(t *testing.T) {
	out := ErrVolApp("nope")
	if !strings.Contains(out, "volunteer@btcpp.dev") {
		t.Errorf("ErrVolApp should route to volunteer@; got:\n%s", out)
	}
}
