package gh

import "testing"

// TestHTTPClientHasTimeout guards against an accidental regression where
// someone reverts the shared client back to http.DefaultClient (which has
// no timeout). A CLI tool with no network timeout can block indefinitely
// on a stalled connection — especially painful in CI.
func TestHTTPClientHasTimeout(t *testing.T) {
	if httpClient.Timeout <= 0 {
		t.Errorf("httpClient.Timeout = %v, want > 0", httpClient.Timeout)
	}
}
