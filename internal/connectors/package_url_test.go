package connectors

import "testing"

// Package results without a project or home page must still link to their
// registry page, because results without a URL host are dropped (S3).
func TestNuGetPackageURLFallsBackToGallery(t *testing.T) {
	if got := nugetPackageURL("", "Serilog"); got != "https://www.nuget.org/packages/Serilog" {
		t.Fatalf("got %q", got)
	}
	if got := nugetPackageURL("https://serilog.net/", "Serilog"); got != "https://serilog.net/" {
		t.Fatalf("project URL must win, got %q", got)
	}
}

func TestPyPIPackageURLFallsBackToProjectPage(t *testing.T) {
	if got := pypiPackageURL("", "", "", "httpx"); got != "https://pypi.org/project/httpx/" {
		t.Fatalf("got %q", got)
	}
	if got := pypiPackageURL("", "https://github.com/encode/httpx", "", "httpx"); got != "https://github.com/encode/httpx" {
		t.Fatalf("project URL must win, got %q", got)
	}
}
