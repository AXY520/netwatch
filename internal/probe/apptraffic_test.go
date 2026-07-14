package probe

import "testing"

func TestSanitizeIconURLUsesBoxDomain(t *testing.T) {
	got := sanitizeIconURL("https://$boxdomain/sys/icons/com.example.app.png", "box.example.com")
	want := "https://box.example.com/sys/icons/com.example.app.png"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestSanitizeIconURLKeepsAbsoluteURL(t *testing.T) {
	got := sanitizeIconURL("https://cdn.example.com/icon.png", "box.example.com")
	if got != "https://cdn.example.com/icon.png" {
		t.Fatalf("got %q", got)
	}
}

func TestSanitizeIconURLFallsBackToRelativePath(t *testing.T) {
	got := sanitizeIconURL("https://$boxdomain/sys/icons/com.example.app.png", "")
	if got != "/sys/icons/com.example.app.png" {
		t.Fatalf("got %q", got)
	}
}
