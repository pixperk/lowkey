package client_test

import (
	"net"
	"testing"
	"time"
)

// requireServer skips the test if no lowkey server is listening on serverAddr.
// The percentile tests need a live server; in CI (and any dev machine that
// isn't running lowkey) they should skip rather than fail.
func requireServer(t *testing.T) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", serverAddr, 200*time.Millisecond)
	if err != nil {
		t.Skipf("no lowkey server at %s: %v", serverAddr, err)
	}
	conn.Close()
}
