package main

import (
	"os"
	"path/filepath"
	"strings"
)

// New environment variables take precedence, including explicit empty values.
func brandEnv(name string) string {
	suffix := strings.TrimPrefix(name, "BUILDHEADWAY_")
	for _, prefix := range []string{"BUILDHEADWAY_", "CODENTRAIL_", "FORGE_"} {
		if value, ok := os.LookupEnv(prefix + suffix); ok {
			return value
		}
	}
	return ""
}
func directoryExists(path string) bool   { info, err := os.Stat(path); return err == nil && info.IsDir() }
func legacyBrandPath(path string) string { return previousBrandPath(path, "Forge") }
func previousBrandPath(path, brand string) string {
	base := filepath.Base(path)
	if base == "BuildHeadway" {
		base = brand
	} else if base == "buildheadway" {
		base = strings.ToLower(brand)
	}
	return filepath.Join(filepath.Dir(path), base)
}
