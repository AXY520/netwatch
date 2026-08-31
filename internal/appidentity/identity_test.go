package appidentity

import "testing"

func TestBuildKeepsSingleInstanceCompatible(t *testing.T) {
	if got := Build("cloud.lazycat.app.demo", "", "demo", false); got != "cloud.lazycat.app.demo" {
		t.Fatalf("single-instance id = %q", got)
	}
}

func TestBuildUsesUserAndProjectIdentities(t *testing.T) {
	if got := Build("cloud.lazycat.app.demo", "user one", "demo", true); got != "cloud.lazycat.app.demo@user:user+one" {
		t.Fatalf("user instance id = %q", got)
	}
	if got := Build("cloud.lazycat.app.demo", "", "demo-1", true); got != "cloud.lazycat.app.demo@project:demo-1" {
		t.Fatalf("project instance id = %q", got)
	}
}

func TestBaseExtractsApplicationID(t *testing.T) {
	for input, want := range map[string]string{
		"cloud.lazycat.app.demo":                  "cloud.lazycat.app.demo",
		"cloud.lazycat.app.demo@user:axy":         "cloud.lazycat.app.demo",
		"cloud.lazycat.app.demo@project:demo-two": "cloud.lazycat.app.demo",
	} {
		if got := Base(input); got != want {
			t.Errorf("Base(%q) = %q, want %q", input, got, want)
		}
	}
}
