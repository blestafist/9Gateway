package security

import "testing"

func TestValidateIdentifierAndRequestID(t *testing.T) {
	for _, value := range []string{"key-1", "request_01", "模型"} {
		if value == "模型" {
			if ValidateIdentifier(value) {
				t.Fatalf("unicode identifier %q accepted", value)
			}
			continue
		}
		if !ValidateIdentifier(value) {
			t.Fatalf("valid identifier %q rejected", value)
		}
	}
	for _, value := range []string{"../etc", `..\\etc`, "key/1", "key%2f1", "key\x00", "key\n1", "-key"} {
		if ValidateIdentifier(value) {
			t.Fatalf("unsafe identifier %q accepted", value)
		}
	}
	if !ValidateRequestID("a1111111111111111111111111111111") || ValidateRequestID("short") {
		t.Fatal("request ID length boundary is incorrect")
	}
}

func TestValidateCursorSyntax(t *testing.T) {
	for _, value := range []string{"", "YWJjZA", "YWJjZA==", "bad+", "bad=", "bad\n"} {
		want := value == "" || value == "YWJjZA"
		if got := ValidateCursorSyntax(value); got != want {
			t.Fatalf("cursor %q valid = %v, want %v", value, got, want)
		}
	}
}

func TestValidateUpstreamURL(t *testing.T) {
	for _, value := range []string{"file:///etc/passwd", "javascript:alert()", "/relative", "http://user:pass@example.test", "http://example.test/#fragment"} {
		if _, err := ValidateUpstreamURL(value); err == nil {
			t.Fatalf("unsafe upstream URL %q accepted", value)
		}
	}
	if _, err := ValidateUpstreamURL("https://router.example.test/base"); err != nil {
		t.Fatalf("valid upstream URL rejected: %v", err)
	}
}
