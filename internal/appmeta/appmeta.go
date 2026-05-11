// Package appmeta reads app display names from each application's package.yml
// mounted under /lzcapp/run/pkgm/<appid>/pkg/package.yml.
//
// This is a backup data source for the lzcsdk PackageManager.QueryApplication
// call, whose Title field has been observed to be empty on real boxes. The
// package.yml `name` field (e.g. "懒猫开发者工具") is what users see in the
// app store, so it's the right string to surface.
package appmeta

import (
	"os"
	"path/filepath"
	"strings"
)

const pkgmDir = "/lzcapp/run/pkgm"

// Available returns true when the pkgm mount is present.
func Available() bool {
	_, err := os.Stat(pkgmDir)
	return err == nil
}

// LoadTitles scans /lzcapp/run/pkgm/<appid>/pkg/package.yml for every installed
// application and returns a map keyed by appid.
//
// Best-effort: any read error for an individual app is swallowed; the function
// only returns an error when the pkgm directory itself isn't readable.
func LoadTitles() (map[string]string, error) {
	entries, err := os.ReadDir(pkgmDir)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		appid := e.Name()
		name := parsePackageName(filepath.Join(pkgmDir, appid, "pkg", "package.yml"))
		if name != "" {
			out[appid] = name
		}
	}
	return out, nil
}

// parsePackageName extracts the top-level `name:` field from a package.yml.
// We avoid pulling in a full YAML library — the format is simple enough.
func parsePackageName(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		// Skip indented lines (locales.zh.name etc.) — we only want the
		// top-level name. Top-level keys start at column 0.
		if len(line) == 0 || line[0] == ' ' || line[0] == '\t' || line[0] == '#' {
			continue
		}
		if !strings.HasPrefix(line, "name:") {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(line, "name:"))
		value = strings.Trim(value, `"'`)
		return value
	}
	return ""
}
