package mcp

import (
	"context"
	"os"
	"testing"
	"time"
)

// Manager.CloseAll used to close servers one after another. Each stdio Close
// waits for the process to exit, so slow-shutdown servers stacked up: four
// servers cost four times the wait and quitting looked like a hang.
func TestManagerCloseAllIsConcurrent(t *testing.T) {
	const (
		servers  = 3
		slowExit = 1500 * time.Millisecond
	)

	configs := make([]ServerConfig, 0, servers)
	for range servers {
		configs = append(configs, ServerConfig{
			Name:         "slow",
			ProtoVersion: protocolVersion20250326,
			Command:      os.Args[0],
			Args:         []string{"-test.run=^TestManagerCloseAllIsConcurrent$"},
			Env: map[string]string{
				"MCP_TEST_SERVER":               "1",
				"MCP_TEST_SERVER_WAIT_EOF":      "1",
				"MCP_TEST_SERVER_EXIT_DELAY_MS": "1500",
			},
		})
	}

	m := NewManager(configs)
	for _, c := range m.Clients() {
		if err := c.Connect(context.Background()); err != nil {
			m.CloseAll()
			t.Skipf("test server did not handshake (%v); shutdown timing not measurable", err)
		}
	}

	start := time.Now()
	m.CloseAll()
	elapsed := time.Since(start)

	// Serial shutdown stacks to servers*slowExit (4.5s here); concurrent
	// shutdown pays slowExit once (1.5s). Two of the three delays is a wide
	// gap from both, so scheduler noise and -race overhead cannot decide it.
	serial := time.Duration(servers) * slowExit
	if elapsed >= 2*slowExit {
		t.Errorf("CloseAll took %v, near the %v serial bound — shutdown is not concurrent", elapsed, serial)
	}
	t.Logf("CloseAll of %d slow servers took %v (serial would be ~%v)", servers, elapsed.Round(10*time.Millisecond), serial)
}
