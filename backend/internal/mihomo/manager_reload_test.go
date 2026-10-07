package mihomo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func writeValidationKernel(t *testing.T, m *Manager, longRunning bool) {
	t.Helper()
	script := "#!/bin/sh\nexit 0\n"
	if longRunning {
		script = "#!/bin/sh\nwhile :; do sleep 1; done\n"
	}
	require.NoError(t, os.WriteFile(filepath.Join(m.dir, "mihomo"), []byte(script), 0700))
}

func socksFixture(t *testing.T, port int) func() {
	t.Helper()
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	require.NoError(t, err)
	stop := make(chan struct{})
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				select {
				case <-stop:
					return
				default:
				}
				continue
			}
			go func(c net.Conn) {
				defer c.Close()
				var hello [3]byte
				if _, err := io.ReadFull(c, hello[:]); err != nil || hello != [3]byte{5, 1, 0} {
					return
				}
				_, _ = c.Write([]byte{5, 0})
			}(conn)
		}
	}()
	return func() { close(stop); _ = ln.Close() }
}

func reloadFixture(t *testing.T, status int, rollbackStatus int, startOld, restoreOld bool) (*Manager, func(), func() []string) {
	t.Helper()
	m := New(t.TempDir())
	writeValidationKernel(t, m, false)
	m.state.Installed = true
	m.state.Running = true
	m.saved = saved{Secret: "old-secret", Nodes: []map[string]any{{"name": "node-a", "type": "http", "server": "127.0.0.1", "port": 1}}, NodePorts: map[string]int{"node-a": nodePortBase}}
	var mu sync.Mutex
	var payloads []string
	var closeOld func()
	if startOld {
		closeOld = socksFixture(t, nodePortBase)
	}
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var payload struct {
			Payload string `json:"payload"`
		}
		_ = json.Unmarshal(body, &payload)
		mu.Lock()
		payloads = append(payloads, payload.Payload)
		n := len(payloads)
		if n == 1 && status >= 200 && status < 300 && closeOld != nil {
			closeOld()
			closeOld = nil
		}
		code := status
		if n > 1 {
			code = rollbackStatus
			if code >= 200 && code < 300 && restoreOld && closeOld == nil {
				closeOld = socksFixture(t, nodePortBase)
			}
		}
		mu.Unlock()
		w.WriteHeader(code)
	}))
	m.controllerURL = controller.URL
	cleanup := func() {
		controller.Close()
		mu.Lock()
		defer mu.Unlock()
		if closeOld != nil {
			closeOld()
		}
		m.Close()
	}
	loaded := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), payloads...)
	}
	return m, cleanup, loaded
}

func applyReloadFixture(t *testing.T, m *Manager) error {
	next := m.saved
	next.Nodes = append(append([]map[string]any{}, m.saved.Nodes...), map[string]any{"name": "node-b", "type": "http", "server": "127.0.0.1", "port": 2})
	return m.run(context.Background(), "recover/node-b", next)
}

func listenerProxyAtPort(t *testing.T, payload string, port int) string {
	t.Helper()
	var cfg struct {
		Listeners []nodeListener `json:"listeners"`
	}
	require.NoError(t, json.Unmarshal([]byte(payload), &cfg))
	for _, listener := range cfg.Listeners {
		if listener.Port == port {
			return listener.Proxy
		}
	}
	require.FailNow(t, "listener missing from loaded configuration", "port %d", port)
	return ""
}

func persistReloadFixtureSettings(t *testing.T, m *Manager) []byte {
	t.Helper()
	b, err := json.Marshal(m.saved)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(m.dir, "settings.json"), b, 0600))
	return b
}

func TestReloadChangedExistingListenerRollsBackAndRestoresRoute(t *testing.T) {
	m, cleanup, loaded := reloadFixture(t, http.StatusNoContent, http.StatusNoContent, true, true)
	defer cleanup()
	old := m.saved
	oldSettings := persistReloadFixtureSettings(t, m)

	err := m.run(context.Background(), "disable/node-a", m.saved)
	require.ErrorContains(t, err, "previous configuration restored")
	require.Equal(t, old, m.saved)
	settings, readErr := os.ReadFile(filepath.Join(m.dir, "settings.json"))
	require.NoError(t, readErr)
	require.Equal(t, oldSettings, settings)
	require.NoError(t, probeSOCKSListener(context.Background(), "127.0.0.1:19000"))
	payloads := loaded()
	require.Len(t, payloads, 2)
	require.Equal(t, "REJECT", listenerProxyAtPort(t, payloads[0], nodePortBase))
	require.Equal(t, "node-a", listenerProxyAtPort(t, payloads[1], nodePortBase))
}

func TestReloadNewListenerReadinessFailureRollsBack(t *testing.T) {
	m, cleanup, loaded := reloadFixture(t, http.StatusNoContent, http.StatusNoContent, true, true)
	defer cleanup()
	old := m.saved
	oldSettings := persistReloadFixtureSettings(t, m)
	require.ErrorContains(t, applyReloadFixture(t, m), "previous configuration restored")
	require.Equal(t, old, m.saved)
	settings, err := os.ReadFile(filepath.Join(m.dir, "settings.json"))
	require.NoError(t, err)
	require.Equal(t, oldSettings, settings)
	require.NoError(t, probeSOCKSListener(context.Background(), "127.0.0.1:19000"))
	payloads := loaded()
	require.Len(t, payloads, 2)
	require.Equal(t, "node-b", listenerProxyAtPort(t, payloads[0], nodePortBase+1))
	require.Equal(t, "node-a", listenerProxyAtPort(t, payloads[1], nodePortBase))
}

func TestReloadNon2xxRollsBackAndKeepsOldListener(t *testing.T) {
	m, cleanup, loaded := reloadFixture(t, http.StatusBadGateway, http.StatusNoContent, true, true)
	defer cleanup()
	err := applyReloadFixture(t, m)
	require.ErrorContains(t, err, "previous configuration restored")
	require.NoError(t, probeSOCKSListener(context.Background(), "127.0.0.1:19000"))
	payloads := loaded()
	require.Len(t, payloads, 2)
	require.Equal(t, "node-a", listenerProxyAtPort(t, payloads[1], nodePortBase))
}

func TestReloadRollbackFailureStopsManagedChild(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
	}{
		{name: "controller rejected restore", status: http.StatusBadGateway},
		{name: "restore accepted but listener absent", status: http.StatusNoContent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, cleanup, loaded := reloadFixture(t, http.StatusNoContent, tc.status, true, false)
			defer cleanup()
			old := m.saved
			oldSettings := persistReloadFixtureSettings(t, m)
			cmd := exec.Command("/bin/sh", "-c", "while :; do sleep 1; done")
			require.NoError(t, cmd.Start())
			done := make(chan struct{})
			m.cmd, m.done = cmd, done
			go func() { _ = cmd.Wait(); close(done) }()

			err := m.run(context.Background(), "disable/node-a", m.saved)
			require.ErrorContains(t, err, "kernel stopped")
			require.Eventually(t, func() bool {
				select {
				case <-done:
					return true
				default:
					return false
				}
			}, time.Second, 10*time.Millisecond)
			require.False(t, m.Status().Running)
			require.Equal(t, old, m.saved)
			settings, readErr := os.ReadFile(filepath.Join(m.dir, "settings.json"))
			require.NoError(t, readErr)
			require.Equal(t, oldSettings, settings)
			require.Error(t, probeSOCKSListener(context.Background(), "127.0.0.1:19000"))
			payloads := loaded()
			require.Len(t, payloads, 2)
			require.Equal(t, "REJECT", listenerProxyAtPort(t, payloads[0], nodePortBase))
			require.Equal(t, "node-a", listenerProxyAtPort(t, payloads[1], nodePortBase))
		})
	}
}
