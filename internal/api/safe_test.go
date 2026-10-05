package api

import "testing"

func TestSafeZipEntryName(t *testing.T) {
	ok, err := safeZipEntryName("BASES/RX1/file.T02")
	if err != nil || ok != "BASES/RX1/file.T02" {
		t.Fatalf("got %q %v", ok, err)
	}
	if _, err := safeZipEntryName("../etc/passwd"); err == nil {
		t.Fatal("expected reject ..")
	}
	if _, err := safeZipEntryName("a/../../b"); err == nil {
		t.Fatal("expected reject nested ..")
	}
	if _, err := safeZipEntryName("/abs/path"); err == nil {
		t.Fatal("expected reject absolute")
	}
	if _, err := safeZipEntryName(""); err == nil {
		t.Fatal("expected reject empty")
	}
}

func TestContentDispositionFilename(t *testing.T) {
	if g := contentDispositionFilename(`evil"\r\nX: 1`); stringsContainsQuoteOrCRLF(g) {
		t.Fatalf("unsafe: %q", g)
	}
	if g := contentDispositionFilename("../../x.T02"); g != "x.T02" {
		t.Fatalf("got %q", g)
	}
	if g := contentDispositionFilename(""); g != "download" {
		t.Fatalf("got %q", g)
	}
}

func stringsContainsQuoteOrCRLF(s string) bool {
	for _, r := range s {
		if r == '"' || r == '\r' || r == '\n' {
			return true
		}
	}
	return false
}
