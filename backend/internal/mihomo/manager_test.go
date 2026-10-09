package mihomo

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func proxyTunnel(w http.ResponseWriter, r *http.Request, targetAddr string) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	client, _, err := hj.Hijack()
	if err != nil {
		return
	}
	upstream, err := net.Dial("tcp", targetAddr)
	if err != nil {
		_ = client.Close()
		return
	}
	_, _ = client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
	go func() { _, _ = io.Copy(upstream, client); _ = upstream.Close() }()
	go func() { _, _ = io.Copy(client, upstream); _ = client.Close() }()
}

func TestSubscriptionsDeduplicateAndRedact(t *testing.T) {
	urls, err := normalizeURLs([]string{" https://example.org/sub?token=secret ", "https://example.org/sub?token=secret", ""})
	require.NoError(t, err)
	require.Len(t, urls, 1)
	m := New(t.TempDir())
	t.Cleanup(m.Close)
	m.saved.URLs = urls
	m.saved.Secret = "controller-secret"
	b, err := json.Marshal(m.Status())
	require.NoError(t, err)
	require.NotContains(t, string(b), "secret")
	require.NotContains(t, string(b), "example.org")
	for _, raw := range []string{"file:///etc/passwd", "http://user:pass@example.org", "https://example.org/#secret"} {
		_, err = normalizeURLs([]string{raw})
		require.Error(t, err)
	}
}

func TestSubscriptionImportsOnlyNodes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "clash.meta", r.Header.Get("User-Agent"))
		_, _ = w.Write([]byte("external-controller: 0.0.0.0:1234\nproxies:\n  - {name: private-name, type: socks5, server: example.org, port: 1080}\n  - {name: duplicate, type: socks5, server: example.org, port: 1080}\n"))
	}))
	defer server.Close()
	m := New(t.TempDir())
	t.Cleanup(m.Close)
	nodes, names, err := m.fetchNodes(context.Background(), []string{server.URL})
	require.NoError(t, err)
	require.Len(t, nodes, 1)
	nodeID, ok := nodes[0]["name"].(string)
	require.True(t, ok)
	require.Equal(t, "private-name", names[nodeID])
	b, err := m.config(saved{Nodes: nodes, Secret: "test"})
	require.NoError(t, err)
	require.NotContains(t, string(b), "private-name")
	require.NotContains(t, string(b), "0.0.0.0")
	require.Contains(t, string(b), "127.0.0.1:9098")
}

func TestSubscriptionDownloadCanUseManagedProxy(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "clash.meta", r.Header.Get("User-Agent"))
		_, _ = w.Write([]byte("proxies:\n  - {name: proxied-node, type: socks5, server: example.org, port: 1080}\n"))
	}))
	defer proxy.Close()
	m := New(t.TempDir())
	t.Cleanup(m.Close)
	body, err := m.getViaProxy(context.Background(), "http://subscription.invalid/sub", 4<<20, "clash.meta", proxy.URL)
	require.NoError(t, err)
	require.Contains(t, string(body), "proxied-node")
}

func TestSubscriptionDirectFallbackWithDynamicExit(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusBadGateway) }))
	defer proxy.Close()
	sub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "clash.meta", r.Header.Get("User-Agent"))
		_, _ = w.Write([]byte("proxies:\n  - {name: airport, type: http, server: example.org, port: 2000}\n"))
	}))
	defer sub.Close()
	m := New(t.TempDir())
	t.Cleanup(m.Close)
	m.state.Running = true
	m.subscriptionProxyURL = proxy.URL
	m.saved.DynamicProxies = []string{"http://user:private-password@example.org:2000"}
	nodes, names, err := m.fetchNodes(context.Background(), []string{sub.URL + "/?token=private-token"})
	require.NoError(t, err)
	require.Len(t, nodes, 1)
	require.Len(t, names, 1)
	require.Len(t, m.saved.DynamicProxies, 1)
	// A failed direct fetch must not leak subscription or proxy credentials.
	_, _, err = m.fetchNodes(context.Background(), []string{proxy.URL + "/?token=private-token"})
	require.ErrorContains(t, err, "and direct")
	require.NotContains(t, err.Error(), "private-token")
	require.NotContains(t, err.Error(), "private-password")
}

func TestFailedSubscriptionPreservesSavedConfiguration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) }))
	defer server.Close()
	m := New(t.TempDir())
	t.Cleanup(m.Close)
	m.state.Installed = true
	m.saved = saved{URLs: []string{"https://old.example/sub?token=old-secret"}}
	err := m.run(context.Background(), "apply", saved{URLs: []string{server.URL + "/?token=new-secret"}})
	require.Error(t, err)
	require.NotContains(t, err.Error(), "secret")
	require.Equal(t, []string{"https://old.example/sub?token=old-secret"}, m.saved.URLs)
	_, err = os.Stat(filepath.Join(m.dir, "settings.json"))
	require.True(t, os.IsNotExist(err))
}

func TestFailedSubscriptionKeepsRunningPhase(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) }))
	defer server.Close()
	m := New(t.TempDir())
	t.Cleanup(m.Close)
	m.state.Installed = true
	m.state.Running = true
	// This test uses an HTTP stub and never starts the platform-specific kernel.
	m.state.Supported = true
	m.subscriptionProxyURL = server.URL
	m.saved = saved{URLs: []string{"https://old.example/sub"}, Nodes: []map[string]any{{"name": "node-one", "type": "socks5", "server": "example.org", "port": 1080}}}

	require.NoError(t, m.Submit("apply", []string{server.URL}, false))
	require.Eventually(t, func() bool { return !m.Status().Busy }, 2*time.Second, 10*time.Millisecond)
	status := m.Status()
	require.True(t, status.Running)
	require.Equal(t, "running", status.Phase)
	require.Contains(t, status.Error, "HTTP 403")
}

func TestTasksSerializeAndCancel(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done() }))
	defer server.Close()
	m := New(t.TempDir())
	m.state.Installed = true
	m.state.Supported = true
	require.NoError(t, m.Submit("apply", []string{server.URL}, false))
	<-started
	require.Error(t, m.Submit("install", nil, false))
	m.Close()
	require.False(t, m.Status().Busy)
	require.Error(t, m.Submit("install", nil, false))
}

func TestAtomicWritePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	require.NoError(t, atomicWrite(path, []byte("secret"), 0600))
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), info.Mode().Perm())
	require.NoError(t, atomicWrite(path, []byte("updated"), 0600))
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "updated", string(b))
}

func TestDownloadBoundedAndTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(strings.Repeat("x", 200))) }))
	defer server.Close()
	m := New(t.TempDir())
	t.Cleanup(m.Close)
	_, err := m.get(context.Background(), server.URL, 100, "test")
	require.Error(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	<-ctx.Done()
	_, err = m.get(ctx, server.URL, 100, "test")
	require.Error(t, err)
}

// Opt-in installation smoke test: no subscription credentials or model calls.
func TestOfficialKernelInstallation(t *testing.T) {
	if os.Getenv("MIHOMO_INSTALL_SMOKE") != "1" {
		t.Skip("set MIHOMO_INSTALL_SMOKE=1 to verify official download")
	}
	m := New(t.TempDir())
	t.Cleanup(m.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	require.NoError(t, m.run(ctx, "install", saved{}))
	b, err := exec.CommandContext(ctx, filepath.Join(m.dir, "mihomo"), "-v").Output()
	require.NoError(t, err)
	require.Contains(t, string(b), strings.TrimPrefix(Version, "v"))
	t.Logf("official kernel: %s", strings.TrimSpace(string(b)))
	// Both provider and outbound proxy point at this isolated local server.
	// No model endpoint or external proxy is contacted.
	var subscription string
	var twoSubscription string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodConnect {
			proxyTunnel(w, r, r.Host)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/route-") {
			_, _ = w.Write([]byte("old-route"))
			return
		}
		if r.URL.Path == "/invalid" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if r.URL.Path == "/two" {
			_, _ = w.Write([]byte(twoSubscription))
			return
		}
		_, _ = w.Write([]byte(subscription))
	}))
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodConnect {
			proxyTunnel(w, r, r.Host)
			return
		}
		_, _ = w.Write([]byte("new-route"))
	}))
	defer second.Close()
	secondURL, err := url.Parse(second.URL)
	require.NoError(t, err)
	defer server.Close()
	u, err := url.Parse(server.URL)
	require.NoError(t, err)
	subscription = "proxies:\n  - name: mock\n    type: http\n    server: 127.0.0.1\n    port: " + u.Port() + "\n"
	require.NoError(t, m.run(ctx, "apply", saved{URLs: []string{server.URL}}))
	require.True(t, m.Status().Running)
	require.Equal(t, 1, m.Status().Nodes)

	twoSubscription = "proxies:\n  - name: mock\n    type: http\n    server: 127.0.0.1\n    port: " + u.Port() + "\n  - name: mock-two\n    type: http\n    server: 127.0.0.1\n    port: " + secondURL.Port() + "\n"
	require.NoError(t, probeSOCKSListener(ctx, "127.0.0.1:19000"))
	proxyClient := &http.Client{Transport: &http.Transport{Proxy: func(*http.Request) (*url.URL, error) { return url.Parse("http://127.0.0.1:19000") }}}
	defer proxyClient.CloseIdleConnections()
	routeResp, err := proxyClient.Get(server.URL + "/route-before-conflict")
	require.NoError(t, err)
	_ = routeResp.Body.Close()
	require.Equal(t, http.StatusOK, routeResp.StatusCode)
	occupied, err := net.Listen("tcp", "127.0.0.1:19001")
	require.NoError(t, err)
	next := m.saved
	next.URLs = []string{server.URL + "/two"}
	err = m.run(ctx, "apply", next)
	require.ErrorContains(t, err, "occupied")
	require.Equal(t, 1, m.Status().Nodes)
	require.NoError(t, probeSOCKSListener(ctx, "127.0.0.1:19000"))
	oldRoute, err := proxyClient.Get(server.URL + "/route-after-conflict")
	require.NoError(t, err)
	oldBody, err := io.ReadAll(oldRoute.Body)
	_ = oldRoute.Body.Close()
	require.NoError(t, err)
	require.Equal(t, "old-route", string(oldBody))
	_ = occupied.Close()
	// Node listeners are TCP-only: an occupied UDP port must not leave a
	// half-bound mixed listener that blocks disable/recover reloads.
	udpOccupied, err := net.ListenPacket("udp", "127.0.0.1:19001")
	require.NoError(t, err)
	defer func() { _ = udpOccupied.Close() }()
	require.NoError(t, m.run(ctx, "apply", next))
	require.Equal(t, 2, m.Status().Nodes)
	require.NoError(t, probeSOCKSListener(ctx, "127.0.0.1:19001"))
	newClient := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: http.ProxyURL(&url.URL{Scheme: "http", Host: "127.0.0.1:19001"})}}
	defer newClient.CloseIdleConnections()
	newRoute, err := newClient.Get(second.URL + "/route-after-release")
	require.NoError(t, err)
	newBody, err := io.ReadAll(newRoute.Body)
	_ = newRoute.Body.Close()
	require.NoError(t, err)
	require.Equal(t, "new-route", string(newBody))
	// Disable/recover rewrites an existing listener without allocating a port.
	var nodeName string
	for name, port := range m.saved.NodePorts {
		if port == nodePortBase+1 {
			nodeName = name
			break
		}
	}
	require.NotEmpty(t, nodeName)
	require.NoError(t, m.run(ctx, "disable/"+nodeName, m.saved))
	require.NoError(t, probeSOCKSListener(ctx, "127.0.0.1:19001"))
	newClient.CloseIdleConnections()
	rejected, err := newClient.Get(second.URL + "/route-disabled")
	require.NoError(t, err)
	_ = rejected.Body.Close()
	require.Equal(t, http.StatusBadGateway, rejected.StatusCode)
	require.NoError(t, m.run(ctx, "recover/"+nodeName, m.saved))
	newClient.CloseIdleConnections()
	recovered, err := newClient.Get(second.URL + "/route-recovered")
	require.NoError(t, err)
	recoveredBody, err := io.ReadAll(recovered.Body)
	_ = recovered.Body.Close()
	require.NoError(t, err)
	require.Equal(t, "new-route", string(recoveredBody))
	require.Equal(t, nodePortBase+1, m.saved.NodePorts[nodeName])
	// The released port is accepted and the second listener becomes usable.
	old := m.saved
	require.Error(t, m.run(ctx, "apply", saved{URLs: []string{server.URL + "/invalid"}, Nodes: nil}))
	require.Equal(t, old.URLs, m.saved.URLs)
	m.Close()
	restored := New(m.dir)
	defer restored.Close()
	require.Eventually(t, func() bool { return restored.Status().Running }, 10*time.Second, 100*time.Millisecond)
}

func TestDisabledNodesAreExcludedFromRotation(t *testing.T) {
	m := New(t.TempDir())
	t.Cleanup(m.Close)
	b, err := m.config(saved{Secret: "test", Nodes: []map[string]any{{"name": "node-one", "type": "http", "server": "localhost", "port": 1}}, Disabled: map[string]string{"node-one": "failed"}})
	require.NoError(t, err)
	var cfg struct {
		Groups []struct {
			Proxies []string `json:"proxies"`
		} `json:"proxy-groups"`
	}
	require.NoError(t, json.Unmarshal(b, &cfg))
	require.Equal(t, []string{"REJECT"}, cfg.Groups[0].Proxies)
}
