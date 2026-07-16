package probe

import (
	"testing"

	"netwatch/internal/dockerlzc"
)

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

func TestIsNetwatchApp(t *testing.T) {
	if !isNetwatchApp(dockerlzc.BridgeAppInfo{AppID: "cloud.lazycat.app.netwatch"}) {
		t.Fatal("app id should match")
	}
	if !isNetwatchApp(dockerlzc.BridgeAppInfo{Project: "cloudlazycatappnetwatch"}) {
		t.Fatal("compose project should match")
	}
	if isNetwatchApp(dockerlzc.BridgeAppInfo{Project: "cloudlazycatappphoto"}) {
		t.Fatal("unrelated project must not match")
	}
	if isNetwatchApp(dockerlzc.BridgeAppInfo{Project: "something-netwatch-ish"}) {
		t.Fatal("loose substring must not match")
	}
}
