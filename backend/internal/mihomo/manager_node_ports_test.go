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
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

type nodeListener struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Host  string `json:"listen"`
	Port  int    `json:"port"`
	UDP   *bool  `json:"udp"`
	Proxy string `json:"proxy"`
}

// runningKernelManager simulates a running kernel: "-t" validation succeeds
// and the controller accepts reloads, recording the last loaded configuration.
func runningKernelManager(t *testing.T) (*Manager, func() []nodeListener) {
	t.Helper()
	var mu sync.Mutex
	var loaded string
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && r.URL.Path == "/configs" {
			body, _ := io.ReadAll(r.Body)
			var payload struct {
				Payload string `json:"payload"`
			}
			if json.Unmarshal(body, &payload) != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			mu.Lock()
			loaded = payload.Payload
			mu.Unlock()
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(controller.Close)
	m := New(t.TempDir())
	t.Cleanup(m.Close)
	require.NoError(t, os.WriteFile(filepath.Join(m.dir, "mihomo"), []byte("#!/bin/sh\nexit 0\n"), 0700))
	m.state.Installed = true
	m.state.Supported = true
	m.state.Running = true
	m.listenerProbe = func(context.Context, string) error { return nil }
	m.controllerURL = controller.URL
	listeners := func() []nodeListener {
		mu.Lock()
		defer mu.Unlock()
		var cfg struct {
			Listeners []nodeListener `json:"listeners"`
		}
		require.NoError(t, json.Unmarshal([]byte(loaded), &cfg))
		return cfg.Listeners
	}
	return m, listeners
}

func applyDynamic(t *testing.T, m *Manager, proxies ...string) {
	t.Helper()
	clean, err := normalizeDynamicProxies(proxies)
	require.NoError(t, err)
	next := m.saved
	next.URLs = nil
	next.DynamicProxies = clean
	require.NoError(t, m.run(context.Background(), "apply_dynamic", next))
}

func statusNodePorts(t *testing.T, m *Manager) map[string]int {
	t.Helper()
	ports := map[string]int{}
	for _, node := range m.Status().NodeStates {
		ports[node.Name] = node.LocalPort
	}
	return ports
}

func TestNodePortsStayStableAndAreNeverReused(t *testing.T) {
	m, listeners := runningKernelManager(t)
	a, b, c := "http://u:p@127.0.0.1:18001", "http://u:p@127.0.0.1:18002", "http://u:p@127.0.0.1:18003"

	applyDynamic(t, m, a, b)
	removedName, ok := m.saved.Nodes[0]["name"].(string) // dynamic nodes keep input order
	require.True(t, ok)
	first := statusNodePorts(t, m)
	require.Len(t, first, 2)
	keptName, ok := m.saved.Nodes[1]["name"].(string)
	require.True(t, ok)
	require.Equal(t, map[string]int{removedName: nodePortBase, keptName: nodePortBase + 1}, m.saved.NodePorts)

	applyDynamic(t, m, b, a)
	require.Equal(t, first, statusNodePorts(t, m), "re-applying the same nodes keeps their ports")

	removedPort := m.saved.NodePorts[removedName]
	applyDynamic(t, m, b, c)
	ports := m.Status().NodeStates
	require.Len(t, ports, 2)
	seen := map[int]bool{}
	for _, node := range ports {
		require.NotEqual(t, removedPort, node.LocalPort, "a removed node's port is never handed to another node")
		seen[node.LocalPort] = true
	}
	require.True(t, seen[nodePortBase+2], "a new node gets the next unused port")
	require.Equal(t, removedPort, m.saved.NodePorts[removedName], "removed nodes keep their reservation")

	byPort := map[int]nodeListener{}
	for _, l := range listeners() {
		require.Equal(t, "127.0.0.1", l.Host)
		require.Equal(t, "mixed", l.Type)
		require.Equal(t, "NODE-"+strconv.Itoa(l.Port), l.Name)
		// Mihomo defaults mixed listeners to UDP; a failed UDP bind leaves a stray TCP listener.
		require.NotNil(t, l.UDP, "node listeners must set udp explicitly")
		require.False(t, *l.UDP)
		byPort[l.Port] = l
	}
	require.Len(t, byPort, 3)
	require.Equal(t, "REJECT", byPort[removedPort].Proxy, "a removed node's listener rejects instead of falling back")
	for _, node := range ports {
		require.Equal(t, node.Name, byPort[node.LocalPort].Proxy)
	}

	stored, err := os.ReadFile(filepath.Join(m.dir, "settings.json"))
	require.NoError(t, err)
	var persisted saved
	require.NoError(t, json.Unmarshal(stored, &persisted))
	require.Equal(t, m.saved.NodePorts, persisted.NodePorts)
	m.Close()
	restarted := New(m.dir)
	t.Cleanup(restarted.Close)
	for _, node := range restarted.Status().NodeStates {
		require.Equal(t, persisted.NodePorts[node.Name], node.LocalPort, "ports survive a restart")
	}
}

func TestDisabledNodeListenerRejects(t *testing.T) {
	m, listeners := runningKernelManager(t)
	applyDynamic(t, m, "http://u:p@127.0.0.1:18001", "http://u:p@127.0.0.1:18002")
	target, ok := m.saved.Nodes[0]["name"].(string)
	require.True(t, ok)
	port := m.saved.NodePorts[target]

	require.NoError(t, m.run(context.Background(), "disable/"+target, m.saved))
	for _, l := range listeners() {
		if l.Port == port {
			require.Equal(t, "REJECT", l.Proxy)
		} else {
			require.NotEqual(t, "REJECT", l.Proxy)
		}
	}
	require.NoError(t, m.run(context.Background(), "recover/"+target, m.saved))
	for _, l := range listeners() {
		if l.Port == port {
			require.Equal(t, target, l.Proxy)
		}
	}
	require.Equal(t, port, m.saved.NodePorts[target], "disable and recover keep the port")
}

func TestAssignNodePortsDoesNotMutateCommittedMap(t *testing.T) {
	committed := saved{Nodes: []map[string]any{{"name": "node-a"}}, NodePorts: map[string]int{"node-a": nodePortBase}}
	next := committed
	next.Nodes = append(next.Nodes, map[string]any{"name": "node-b"})
	require.NoError(t, assignNodePorts(&next))
	require.Equal(t, map[string]int{"node-a": nodePortBase}, committed.NodePorts)
	require.Equal(t, nodePortBase+1, next.NodePorts["node-b"])
}

func TestAssignNodePortsCapacity(t *testing.T) {
	s := saved{NodePorts: map[string]int{}}
	for i := range maxNodePorts {
		s.NodePorts["old-"+strconv.Itoa(i)] = nodePortBase + i
	}
	s.Nodes = []map[string]any{{"name": "old-0"}}
	require.NoError(t, assignNodePorts(&s), "existing nodes need no new port")
	s.Nodes = append(s.Nodes, map[string]any{"name": "node-new"})
	require.EqualError(t, assignNodePorts(&s), "node port capacity exceeded")
}

func TestStartRejectsOccupiedNodePort(t *testing.T) {
	for _, address := range []string{"127.0.0.1:3101", "127.0.0.1:9098"} {
		ln, err := net.Listen("tcp", address)
		if err != nil {
			t.Skipf("%s is in use on this host", address)
		}
		_ = ln.Close()
	}
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = occupied.Close() }()
	tcpAddr, ok := occupied.Addr().(*net.TCPAddr)
	require.True(t, ok)
	port := tcpAddr.Port
	require.Contains(t, kernelPorts(map[string]int{"node-a": port}), "127.0.0.1:"+strconv.Itoa(port))

	m := New(t.TempDir())
	t.Cleanup(m.Close)
	err = m.start(context.Background(), filepath.Join(m.dir, "candidate.json"), "secret", map[string]int{"node-a": port})
	require.ErrorContains(t, err, "port occupied")
	require.False(t, m.Status().Running)
}

func TestHotReloadRejectsOccupiedNewPortBeforeController(t *testing.T) {
	m, _ := runningKernelManager(t)
	applyDynamic(t, m, "http://u:p@127.0.0.1:18001")
	old := m.saved
	occupied, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", nodePortBase+1))
	require.NoError(t, err)
	defer func() { _ = occupied.Close() }()
	err = m.run(context.Background(), "apply_dynamic", saved{DynamicProxies: []string{
		"http://u:p@127.0.0.1:18001", "http://u:p@127.0.0.1:18002",
	}})
	require.ErrorContains(t, err, "occupied")
	require.Equal(t, old.NodePorts, m.saved.NodePorts)
	require.Equal(t, old.DynamicProxies, m.saved.DynamicProxies)
}
