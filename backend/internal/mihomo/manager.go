// Package mihomo manages a private, unprivileged Mihomo kernel for administrator IP management.
package mihomo

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

const Version = "v1.19.31"
const Endpoint = "http://127.0.0.1:3101"

var managers sync.Map

// CloseAll is called after HTTP shutdown so no new management work can enter.
func CloseAll() {
	managers.Range(func(key, value any) bool {
		if m, ok := key.(*Manager); ok {
			m.Close()
		}
		managers.Delete(key)
		return true
	})
}

type Status struct {
	DownloadMode      SubscriptionDownloadMode `json:"subscription_download_mode"`
	SubscriptionItems []SubscriptionStatus     `json:"subscription_items"`
	Installed         bool                     `json:"installed"`
	Running           bool                     `json:"running"`
	Busy              bool                     `json:"busy"`
	Phase             string                   `json:"phase"`
	Error             string                   `json:"error,omitempty"`
	Subscriptions     int                      `json:"subscriptions"`
	DynamicProxies    int                      `json:"dynamic_proxies"`
	Nodes             int                      `json:"nodes"`
	Endpoint          string                   `json:"endpoint"`
	Supported         bool                     `json:"supported"`
	NodeStates        []NodeStatus             `json:"node_states"`
}

type NodeStatus struct {
	SubscriptionIDs []string   `json:"subscription_ids,omitempty"`
	Dynamic         bool       `json:"dynamic"`
	Name            string     `json:"name"`
	DisplayName     string     `json:"display_name,omitempty"`
	State           string     `json:"state"`
	LocalPort       int        `json:"local_port,omitempty"`
	Check           *NodeCheck `json:"check,omitempty"`
}

type saved struct {
	DownloadMode          SubscriptionDownloadMode     `json:"subscription_download_mode"`
	SubscriptionCache     map[string]subscriptionCache `json:"subscription_cache,omitempty"`
	DisabledSubscriptions map[string]bool              `json:"disabled_subscriptions,omitempty"`
	SubscriptionLabels    map[string]string            `json:"subscription_labels,omitempty"`
	URLs                  []string                     `json:"urls"`
	DynamicProxies        []string                     `json:"dynamic_proxies,omitempty"`
	Nodes                 []map[string]any             `json:"nodes"`
	NodeNames             map[string]string            `json:"node_names,omitempty"`
	Secret                string                       `json:"secret"`
	Disabled              map[string]string            `json:"disabled,omitempty"`
	// NodePorts pins every node ever configured to its own loopback listener.
	// Ports are never reused, so a removed node cannot hand its port to another exit.
	NodePorts map[string]int `json:"node_ports,omitempty"`
}

type Manager struct {
	controllerURL        string // optional override for isolated controller tests
	subscriptionProxyURL string // test-only override for subscription download proxy
	gate                 chan struct{}
	mu                   sync.Mutex
	dir                  string
	state                Status
	saved                saved
	nodeChecks           map[string]NodeCheck // latest diagnostics by node name; protected by mu, never persisted
	cmd                  *exec.Cmd
	done                 chan struct{}
	cancel               context.CancelFunc
	closed               bool
	wg                   sync.WaitGroup
	client               *http.Client
	listenerProbe        func(context.Context, string) error // test-only readiness probe override
}

func New(dir string) *Manager {
	m := &Manager{dir: dir, client: &http.Client{Timeout: 30 * time.Second}, gate: make(chan struct{}, 1)}
	m.state = Status{Endpoint: Endpoint, Phase: "not_installed", Supported: runtime.GOOS == "linux" && (runtime.GOARCH == "amd64" || runtime.GOARCH == "arm64")}
	if b, err := os.ReadFile(filepath.Join(dir, "settings.json")); err == nil {
		_ = json.Unmarshal(b, &m.saved)
	}
	if fi, err := os.Stat(filepath.Join(dir, "mihomo")); err == nil && fi.Mode().Perm()&0111 != 0 {
		m.state.Installed = true
		m.state.Phase = "ready"
	}
	managers.Store(m, struct{}{})
	if m.state.Installed && len(m.saved.Nodes) > 0 {
		_ = m.Submit("start", nil, false)
	}
	return m
}

func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.state
	s.DownloadMode = m.saved.DownloadMode
	if s.DownloadMode == "" {
		s.DownloadMode = SubscriptionDownloadAuto
	}
	s.Subscriptions = len(m.saved.URLs)
	s.SubscriptionItems = subscriptionStatuses(m.saved)
	s.DynamicProxies = len(m.saved.DynamicProxies)
	s.Nodes = len(m.saved.Nodes)
	sources := subscriptionNodeSourceIndex(m.saved)
	for _, n := range m.saved.Nodes {
		if name, ok := n["name"].(string); ok {
			state := "enabled"
			if disabled := m.saved.Disabled[name]; disabled != "" {
				state = disabled
			}
			node := NodeStatus{SubscriptionIDs: sources[name], Dynamic: strings.HasPrefix(name, "DYNAMIC-"), Name: name, DisplayName: m.saved.NodeNames[name], State: state, LocalPort: m.saved.NodePorts[name]}
			if check, ok := m.nodeChecks[name]; ok {
				node.Check = check.clone()
			}
			s.NodeStates = append(s.NodeStates, node)
		}
	}
	return s
}

// Submit serializes long-running work and never returns subprocess output or URLs.
func (m *Manager) Submit(action string, urls []string, appendURLs bool) error {
	return m.SubmitWithDynamicProxies(action, urls, nil, appendURLs)
}

// SubmitWithDynamicProxies accepts raw proxy URLs in addition to remote
// Clash/Mihomo subscriptions. The legacy Submit method remains unchanged for
// callers that do not use the dynamic source.
func (m *Manager) SubmitWithDynamicProxies(action string, urls, dynamicProxies []string, appendURLs bool) error {
	return m.SubmitSourceManagement(action, urls, dynamicProxies, appendURLs, "")
}

// SubmitSourceManagement keeps subscription labels separate from secret URLs.
func (m *Manager) SubmitSourceManagement(action string, urls, dynamicProxies []string, appendURLs bool, label string) error {
	op, node, hasNode := strings.Cut(action, "/")
	label = strings.TrimSpace(label)
	if op == "subscription_rename" && label == "" {
		return errors.New("subscription name is required")
	}
	if len([]rune(label)) > 80 || strings.ContainsAny(label, "\r\n") || strings.Contains(label, "://") {
		return errors.New("subscription name must be plain text, at most 80 characters, without a URL")
	}
	if label != "" && op != "subscription_add" && op != "subscription_update" && op != "subscription_rename" {
		return errors.New("subscription name is not supported for this operation")
	}
	if !sourceOperation(op) && op != "install" && op != "apply" && op != "apply_dynamic" && op != "start" && op != "disable" && op != "recover" && op != "probe" {
		return errors.New("unknown operation")
	}
	if (op == "disable" || op == "recover" || op == "probe" || sourceTargetRequired(op)) != hasNode || (hasNode && (node == "" || strings.Contains(node, "/"))) {
		return errors.New("invalid operation target")
	}
	clean, err := normalizeURLs(urls)
	if err != nil {
		return err
	}
	cleanDynamic, err := normalizeDynamicProxies(dynamicProxies)
	if err != nil {
		return err
	}
	m.mu.Lock()
	if m.closed || m.state.Busy {
		m.mu.Unlock()
		return errors.New("another operation is running or service is stopping")
	}
	if !m.state.Supported {
		m.mu.Unlock()
		return errors.New("requires Linux amd64 or arm64")
	}
	next := m.saved
	if sourceOperation(op) {
		next, err = prepareSourceChange(next, op, node, clean, cleanDynamic)
		if err == nil && label != "" {
			if op == "subscription_rename" {
				next.SubscriptionLabels[node] = label
			} else if len(clean) == 1 {
				next.SubscriptionLabels[subscriptionID(clean[0])] = label
			} else {
				err = errors.New("name requires exactly one subscription")
			}
		}
		if err != nil {
			m.mu.Unlock()
			return err
		}
	}
	if op == "apply_dynamic" {
		if len(cleanDynamic) == 0 {
			m.mu.Unlock()
			return errors.New("a dynamic proxy is required")
		}
		next.URLs = nil
		next.DynamicProxies = cleanDynamic
	}
	if action == "apply" && len(clean) > 0 {
		if appendURLs {
			merged, mergeErr := normalizeURLs(append(append([]string{}, next.URLs...), clean...))
			if mergeErr != nil {
				m.mu.Unlock()
				return mergeErr
			}
			next.URLs = merged
		} else {
			next.URLs = clean
		}
	}
	if action == "apply" && len(cleanDynamic) > 0 {
		if appendURLs {
			merged, mergeErr := normalizeDynamicProxies(append(append([]string{}, next.DynamicProxies...), cleanDynamic...))
			if mergeErr != nil {
				m.mu.Unlock()
				return mergeErr
			}
			next.DynamicProxies = merged
		} else {
			next.DynamicProxies = cleanDynamic
		}
	}
	m.state.Busy = true
	m.wg.Add(1)
	m.state.Error = ""
	m.state.Phase = action
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	m.cancel = cancel
	m.mu.Unlock()
	go func() {
		defer m.wg.Done()
		defer cancel()
		err := m.acquire(ctx)
		if err == nil {
			// A queued operation must see node states committed by the preceding one.
			m.mu.Lock()
			next.Disabled = m.saved.Disabled
			m.mu.Unlock()
			err = m.run(ctx, action, next)
			m.release()
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		m.state.Busy = false
		if err != nil {
			m.state.Error = err.Error()
			// A failed subscription/configuration operation does not imply that
			// the previously loaded kernel stopped. Keep the runtime state
			// truthful while exposing the operation error to the admin UI.
			if m.state.Running {
				m.state.Phase = "running"
			} else {
				m.state.Phase = "failed"
			}
		} else if m.state.Running {
			m.state.Phase = "running"
		} else {
			m.state.Phase = "ready"
		}
	}()
	return nil
}

func normalizeURLs(raw []string) ([]string, error) {
	if len(raw) > 32 {
		return nil, errors.New("at most 32 subscriptions")
	}
	result := []string{}
	seen := map[string]bool{}
	for _, v := range raw {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		u, err := url.Parse(v)
		if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.Fragment != "" || len(v) > 8192 {
			return nil, errors.New("invalid HTTP(S) subscription address")
		}
		if !seen[v] {
			seen[v] = true
			result = append(result, v)
		}
	}
	return result, nil
}

func (m *Manager) run(ctx context.Context, action string, next saved) error {
	if err := os.MkdirAll(m.dir, 0700); err != nil {
		return errors.New("data directory is not writable")
	}
	if action == "install" {
		m.mu.Lock()
		installed := m.state.Installed
		m.mu.Unlock()
		if !installed {
			if err := m.install(ctx); err != nil {
				return err
			}
		}
		m.mu.Lock()
		m.state.Installed = true
		m.mu.Unlock()
		return nil
	}
	m.mu.Lock()
	installed := m.state.Installed
	old := m.saved
	m.mu.Unlock()
	if !installed {
		return errors.New("install the kernel first")
	}
	op, _, _ := strings.Cut(action, "/")
	if op == "subscription_rename" {
		b, _ := json.Marshal(next)
		if err := atomicWrite(filepath.Join(m.dir, "settings.json"), b, 0600); err != nil {
			return errors.New("cannot save subscription name")
		}
		m.mu.Lock()
		m.saved = next
		m.mu.Unlock()
		return nil
	}
	if action == "apply" || action == "apply_dynamic" || sourceOperation(op) {
		if err := m.resolveSources(ctx, &next, action == "apply" || action == "apply_dynamic"); err != nil {
			return err
		}
		if len(next.Nodes) == 0 && !sourceOperation(op) {
			return errors.New("save a valid subscription or dynamic proxy first")
		}
	}
	// Removing the last source installs a REJECT-only configuration so stale
	// exits cannot remain active and traffic cannot fall back to direct access.
	if len(next.Nodes) == 0 && !sourceOperation(op) {
		return errors.New("save a valid subscription first")
	}
	if op, name, ok := strings.Cut(action, "/"); ok && !sourceOperation(op) {
		found := false
		for _, n := range next.Nodes {
			if n["name"] == name {
				found = true
			}
		}
		if !found {
			return errors.New("unknown node")
		}
		states := map[string]string{}
		for k, v := range next.Disabled {
			states[k] = v
		}
		next.Disabled = states
		switch op {
		case "disable":
			next.Disabled[name] = "disabled"
		case "recover":
			delete(next.Disabled, name)
		case "probe":
			if err := m.control(ctx, http.MethodGet, "/proxies/"+url.PathEscape(name)+"/delay?timeout=1000&url=https%3A%2F%2Fwww.gstatic.com%2Fgenerate_204", next.Secret, nil); err != nil {
				next.Disabled[name] = "failed"
			} else {
				delete(next.Disabled, name)
			}
		}
	}
	if next.Secret == "" {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			return errors.New("cannot generate controller secret")
		}
		next.Secret = hex.EncodeToString(b)
	}
	if err := assignNodePorts(&next); err != nil {
		return err
	}
	m.mu.Lock()
	running := m.state.Running
	m.mu.Unlock()
	newPorts := addedNodePorts(old.NodePorts, next.NodePorts)
	if running {
		if err := preflightPorts(newPorts); err != nil {
			return err
		}
	}
	candidate, err := m.config(next)
	if err != nil {
		return err
	}
	candidatePath := filepath.Join(m.dir, "candidate.json")
	if err = atomicWrite(candidatePath, candidate, 0600); err != nil {
		return errors.New("cannot write candidate configuration")
	}
	cmd := exec.CommandContext(ctx, filepath.Join(m.dir, "mihomo"), "-d", m.dir, "-f", candidatePath, "-t")
	if err = cmd.Run(); err != nil {
		return errors.New("kernel rejected subscription configuration")
	}
	if !running {
		if err = m.start(ctx, candidatePath, next.Secret, next.NodePorts); err != nil {
			return err
		}
	} else {
		reloadErr := m.reload(ctx, candidate, old.Secret)
		if reloadErr == nil {
			reloadErr = m.awaitListeners(ctx, sortedNodePorts(next.NodePorts))
		}
		if reloadErr != nil {
			if rollbackErr := m.restoreOldConfig(old); rollbackErr != nil {
				m.stop()
				m.mu.Lock()
				m.state.Running = false
				m.state.Phase = "stopped"
				m.state.Error = "configuration rollback failed; managed kernel stopped"
				m.mu.Unlock()
				return errors.New("configuration reload failed and rollback could not be verified; kernel stopped")
			}
			return errors.New("configuration reload failed; previous configuration restored")
		}
	}
	b, _ := json.Marshal(next)
	if err = atomicWrite(filepath.Join(m.dir, "settings.json"), b, 0600); err != nil {
		if running {
			if rollbackErr := m.restoreOldConfig(old); rollbackErr != nil {
				m.stop()
				m.mu.Lock()
				m.state.Running = false
				m.state.Phase = "stopped"
				m.state.Error = "configuration rollback failed; managed kernel stopped"
				m.mu.Unlock()
				return errors.New("cannot save configuration; rollback failed and kernel stopped")
			}
		} else {
			m.stop()
		}
		return errors.New("cannot save configuration; change rolled back")
	}
	m.mu.Lock()
	m.saved = next
	m.mu.Unlock()
	return nil
}

func addedNodePorts(old, next map[string]int) []int {
	seen := make(map[int]bool, len(old))
	for _, port := range old {
		seen[port] = true
	}
	ports := make([]int, 0, len(next))
	for _, port := range next {
		if !seen[port] {
			ports = append(ports, port)
		}
	}
	sort.Ints(ports)
	return ports
}

func preflightPorts(ports []int) error {
	for _, port := range ports {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			return errors.New("new node listener port is occupied")
		}
		_ = ln.Close()
	}
	return nil
}

func (m *Manager) restoreOldConfig(old saved) error {
	previous, err := m.config(old)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := m.reload(ctx, previous, old.Secret); err != nil {
		return err
	}
	return m.awaitListeners(ctx, sortedNodePorts(old.NodePorts))
}

func sortedNodePorts(nodePorts map[string]int) []int {
	ports := make([]int, 0, len(nodePorts))
	for _, port := range nodePorts {
		ports = append(ports, port)
	}
	sort.Ints(ports)
	return ports
}

func (m *Manager) awaitListeners(ctx context.Context, ports []int) error {
	probe := m.listenerProbe
	if probe == nil {
		probe = probeSOCKSListener
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		ready := true
		for _, port := range ports {
			if err := ctx.Err(); err != nil {
				return err
			}
			probeCtx, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
			err := probe(probeCtx, fmt.Sprintf("127.0.0.1:%d", port))
			cancel()
			if err != nil {
				ready = false
				break
			}
		}
		if ready {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func probeSOCKSListener(ctx context.Context, address string) error {
	conn, err := (&net.Dialer{Timeout: 150 * time.Millisecond}).DialContext(ctx, "tcp", address)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	deadline, _ := ctx.Deadline()
	if deadline.IsZero() || deadline.After(time.Now().Add(150*time.Millisecond)) {
		deadline = time.Now().Add(150 * time.Millisecond)
	}
	_ = conn.SetDeadline(deadline)
	if _, err := conn.Write([]byte{5, 1, 0}); err != nil {
		return err
	}
	var response [2]byte
	if _, err := io.ReadFull(conn, response[:]); err != nil {
		return err
	}
	if response != [2]byte{5, 0} {
		return errors.New("listener did not complete SOCKS handshake")
	}
	return nil
}

func atomicWrite(path string, b []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".pending-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if err = f.Chmod(mode); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}

func (m *Manager) get(ctx context.Context, address string, limit int64, ua string) ([]byte, error) {
	return download(ctx, m.client, address, limit, ua)
}

// download 拉取有大小上限的响应体。订阅地址可能含凭据，错误中只保留原因、不保留 URL。
func download(ctx context.Context, client *http.Client, address string, limit int64, ua string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, errors.New("invalid download address")
	}
	req.Header.Set("User-Agent", ua)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download failed: %w", stripURLFromError(err))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download returned HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("download incomplete: %w", stripURLFromError(err))
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("download exceeds %d bytes", limit)
	}
	return b, nil
}

// stripURLFromError 去掉 *url.Error 携带的完整请求地址，只保留底层原因。
func stripURLFromError(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Err != nil {
		return urlErr.Err
	}
	return err
}

func (m *Manager) getViaProxy(ctx context.Context, address string, limit int64, ua, proxyAddress string) ([]byte, error) {
	var proxyURL *url.URL
	if proxyAddress != "" {
		var err error
		proxyURL, err = url.Parse(proxyAddress)
		if err != nil || proxyURL.Scheme == "" || proxyURL.Host == "" {
			return nil, errors.New("invalid subscription proxy")
		}
	}
	baseTransport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, errors.New("subscription proxy transport unavailable")
	}
	transport := baseTransport.Clone()
	transport.Proxy = nil // Explicit direct access must not inherit HTTP_PROXY.
	if proxyURL != nil {
		transport.Proxy = http.ProxyURL(proxyURL)
	}
	client := &http.Client{Transport: transport, Timeout: m.client.Timeout}
	defer client.CloseIdleConnections()
	return download(ctx, client, address, limit, ua)
}

func (m *Manager) install(ctx context.Context) error {
	asset := "mihomo-linux-" + runtime.GOARCH + "-" + Version + ".gz"
	if runtime.GOARCH == "amd64" {
		asset = "mihomo-linux-amd64-compatible-" + Version + ".gz"
	}
	manifest, err := m.get(ctx, "https://api.github.com/repos/MetaCubeX/mihomo/releases/tags/"+Version, 4<<20, "Sub2API")
	if err != nil {
		return err
	}
	var release struct {
		Assets []struct {
			Name   string `json:"name"`
			Digest string `json:"digest"`
		} `json:"assets"`
	}
	if err = json.Unmarshal(manifest, &release); err != nil {
		return fmt.Errorf("invalid release manifest: %w", err)
	}
	expected := ""
	for _, a := range release.Assets {
		if a.Name == asset {
			expected = strings.TrimPrefix(a.Digest, "sha256:")
		}
	}
	if len(expected) != 64 {
		return errors.New("verified checksum unavailable for kernel")
	}
	compressed, err := m.get(ctx, "https://github.com/MetaCubeX/mihomo/releases/download/"+Version+"/"+asset, 64<<20, "Sub2API")
	if err != nil {
		return err
	}
	hash := sha256.Sum256(compressed)
	if hex.EncodeToString(hash[:]) != expected {
		return errors.New("kernel checksum mismatch")
	}
	gz, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return errors.New("invalid kernel archive")
	}
	defer func() { _ = gz.Close() }()
	binary, err := io.ReadAll(io.LimitReader(gz, (192<<20)+1))
	if err != nil || len(binary) > 192<<20 {
		return errors.New("invalid kernel size")
	}
	if err = atomicWrite(filepath.Join(m.dir, "mihomo"), binary, 0700); err != nil {
		return fmt.Errorf("cannot install kernel: %w", err)
	}
	return nil
}

func (m *Manager) fetchNodes(ctx context.Context, urls []string) ([]map[string]any, map[string]string, error) {
	nodes := []map[string]any{}
	names := map[string]string{}
	seen := map[string]bool{}
	proxy := ""
	m.mu.Lock()
	mode, modeErr := normalizeSubscriptionDownloadMode(m.saved.DownloadMode)
	running := m.state.Running
	if running && mode != SubscriptionDownloadDirect {
		proxy = m.subscriptionProxyURL
		if proxy == "" {
			proxy = Endpoint
		}
	}
	m.mu.Unlock()
	if modeErr != nil {
		return nil, nil, modeErr
	}
	if mode == SubscriptionDownloadProxy && !running {
		return nil, nil, errors.New("subscription proxy is not running")
	}
	for _, address := range urls {
		imported, err := m.downloadSubscriptionNodes(ctx, address, mode, proxy)
		if err != nil {
			return nil, nil, err
		}
		for _, node := range imported {
			// Only outbound entries are imported; never accept a provider's listeners,
			// rules, external controller or executable configuration.
			kind, _ := node["type"].(string)
			if kind == "" || strings.EqualFold(kind, "direct") || strings.EqualFold(kind, "reject") {
				continue
			}
			displayName, _ := node["name"].(string)
			delete(node, "name")
			delete(node, "dialer-proxy")
			encoded, err := json.Marshal(node)
			if err != nil {
				return nil, nil, errors.New("invalid proxy entry")
			}
			hash := sha256.Sum256(encoded)
			id := hex.EncodeToString(hash[:])
			if seen[id] {
				continue
			}
			seen[id] = true
			nodeID := "node-" + id[:16]
			node["name"] = nodeID
			names[nodeID] = sanitizeNodeDisplayName(displayName)
			nodes = append(nodes, node)
			if len(nodes) > 1000 {
				return nil, nil, errors.New("at most 1000 nodes")
			}
		}
	}
	if len(nodes) == 0 {
		return nil, nil, errors.New("subscription has no usable nodes")
	}
	return nodes, names, nil
}

func (m *Manager) config(s saved) ([]byte, error) {
	names := []string{}
	for _, n := range s.Nodes {
		name, ok := n["name"].(string)
		if !ok || name == "" {
			return nil, errors.New("invalid saved node")
		}
		if s.Disabled[name] != "" {
			continue
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		names = []string{"REJECT"}
	}
	group := map[string]any{"name": "CODEX-ROTATE", "type": "load-balance", "strategy": "round-robin", "proxies": names, "url": "https://www.gstatic.com/generate_204", "interval": 300}
	return json.Marshal(map[string]any{"mixed-port": 3101, "allow-lan": false, "bind-address": "127.0.0.1", "mode": "rule", "log-level": "silent", "external-controller": "127.0.0.1:9098", "secret": s.Secret, "proxies": s.Nodes, "proxy-groups": []any{group}, "listeners": nodeListeners(s), "rules": []string{"MATCH,CODEX-ROTATE"}})
}

const (
	// nodePortBase is the first loopback port handed to a node listener.
	nodePortBase = 19000
	// maxNodePorts bounds the ever-growing, never-reused port range.
	maxNodePorts = 4096
)

// assignNodePorts gives every node without a port the next unused port.
// Ports only grow and are never reused; removed nodes keep their entry so
// a configured static proxy cannot silently switch to a different exit.
func assignNodePorts(s *saved) error {
	var ports map[string]int
	for _, n := range s.Nodes {
		name, _ := n["name"].(string)
		if name == "" {
			continue
		}
		if _, ok := s.NodePorts[name]; ok {
			continue
		}
		if ports == nil {
			// saved is copied by value; never mutate the committed map in place.
			ports = make(map[string]int, len(s.NodePorts)+1)
			for k, v := range s.NodePorts {
				ports[k] = v
			}
			s.NodePorts = ports
		}
		if len(s.NodePorts) >= maxNodePorts {
			return errors.New("node port capacity exceeded")
		}
		s.NodePorts[name] = nodePortBase + len(s.NodePorts)
	}
	return nil
}

// nodeListeners exposes each pinned node on its own loopback port. A port whose
// node was removed or disabled rejects traffic instead of falling back.
// UDP is disabled explicitly: Mihomo's mixed listener defaults to UDP and keeps
// the bound TCP socket when the UDP bind fails, leaving an unregistered listener
// that later reloads cannot replace.
func nodeListeners(s saved) []any {
	present := make(map[string]bool, len(s.Nodes))
	for _, n := range s.Nodes {
		if name, ok := n["name"].(string); ok {
			present[name] = true
		}
	}
	type pinned struct {
		name string
		port int
	}
	ordered := make([]pinned, 0, len(s.NodePorts))
	for name, port := range s.NodePorts {
		ordered = append(ordered, pinned{name: name, port: port})
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].port < ordered[j].port })
	listeners := make([]any, 0, len(ordered))
	for _, p := range ordered {
		target := "REJECT"
		if present[p.name] && s.Disabled[p.name] == "" {
			target = p.name
		}
		listeners = append(listeners, map[string]any{"name": fmt.Sprintf("NODE-%d", p.port), "type": "mixed", "listen": "127.0.0.1", "port": p.port, "udp": false, "proxy": target})
	}
	return listeners
}

// kernelPorts lists every loopback address the managed kernel binds.
func kernelPorts(nodePorts map[string]int) []string {
	ports := []string{"127.0.0.1:3101", "127.0.0.1:9098"}
	for _, port := range nodePorts {
		ports = append(ports, fmt.Sprintf("127.0.0.1:%d", port))
	}
	return ports
}

func (m *Manager) control(ctx context.Context, method, path, secret string, payload []byte) error {
	base := m.controllerURL
	if base == "" {
		base = "http://127.0.0.1:9098"
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+secret)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	resp, err := client.Do(req)
	if err != nil {
		return errors.New("controller unavailable")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return errors.New("controller rejected operation")
	}
	return nil
}
func (m *Manager) reload(ctx context.Context, b []byte, secret string) error {
	payload, _ := json.Marshal(map[string]string{"payload": string(b)})
	return m.control(ctx, http.MethodPut, "/configs?force=true", secret, payload)
}

func (m *Manager) start(ctx context.Context, path, secret string, nodePorts map[string]int) error {
	for _, port := range kernelPorts(nodePorts) {
		ln, err := net.Listen("tcp", port)
		if err != nil {
			return errors.New("proxy/controller port occupied; stop the process using it first")
		}
		_ = ln.Close()
	}
	cmd := exec.Command(filepath.Join(m.dir, "mihomo"), "-d", m.dir, "-f", path)
	configureChild(cmd)
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return errors.New("service is stopping")
	}
	if err := cmd.Start(); err != nil {
		m.mu.Unlock()
		return fmt.Errorf("kernel failed to start: %w", err)
	}
	m.cmd = cmd
	m.done = make(chan struct{})
	done := m.done
	m.mu.Unlock()
	go func() {
		_ = cmd.Wait()
		m.mu.Lock()
		if m.cmd == cmd {
			m.state.Running = false
			if !m.closed {
				m.state.Phase = "stopped"
				m.state.Error = "kernel exited; check configuration and start again"
			}
		}
		close(done)
		m.mu.Unlock()
	}()
	for i := 0; i < 40; i++ {
		if m.control(ctx, http.MethodGet, "/version", secret, nil) == nil {
			conn, dialErr := (&net.Dialer{Timeout: 200 * time.Millisecond}).DialContext(ctx, "tcp", "127.0.0.1:3101")
			if dialErr == nil {
				_ = conn.Close()
				m.mu.Lock()
				select {
				case <-done:
					m.mu.Unlock()
					return errors.New("kernel exited during startup")
				default:
				}
				m.state.Running = true
				m.mu.Unlock()
				return nil
			}
		}
		select {
		case <-ctx.Done():
			m.stop()
			return errors.New("kernel startup cancelled")
		case <-done:
			return errors.New("kernel exited during startup")
		case <-time.After(100 * time.Millisecond):
		}
	}
	m.stop()
	return errors.New("kernel did not become ready")
}
func (m *Manager) stop() {
	m.mu.Lock()
	cmd, done := m.cmd, m.done
	m.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
		<-done
	}
}
func (m *Manager) Close() {
	m.mu.Lock()
	m.closed = true
	if m.cancel != nil {
		m.cancel()
	}
	m.mu.Unlock()
	m.wg.Wait()
	m.stop()
}

func (m *Manager) acquire(ctx context.Context) error {
	select {
	case m.gate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Manager) release() { <-m.gate }
