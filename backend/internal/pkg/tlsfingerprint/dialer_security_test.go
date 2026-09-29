package tlsfingerprint

import (
	"bufio"
	"context"
	"crypto/tls"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestHTTPProxyDialerHonorsContextWhileReadingConnectResponse(t *testing.T) {
	proxyURL := startConnectProxy(t, func(conn net.Conn) {
		var buffer [1]byte
		_, _ = conn.Read(buffer[:])
	})
	dialer := NewHTTPProxyDialer(&Profile{Name: "test"}, proxyURL)
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	started := time.Now()
	conn, err := dialer.DialTLSContext(ctx, "tcp", "example.com:443")
	require.Error(t, err)
	require.Nil(t, conn)
	require.Less(t, time.Since(started), 2*time.Second)
}

func TestHTTPProxyDialerRejectsOversizedConnectResponseHeaders(t *testing.T) {
	proxyURL := startConnectProxy(t, func(conn net.Conn) {
		_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nX-Fill: "+strings.Repeat("a", int(maxHTTPConnectResponseBytes))+"\r\n\r\n")
	})
	dialer := NewHTTPProxyDialer(&Profile{Name: "test"}, proxyURL)

	conn, err := dialer.DialTLSContext(t.Context(), "tcp", "example.com:443")
	require.ErrorContains(t, err, "response headers exceed")
	require.Nil(t, conn)
}

func TestHTTPProxyDialerDoesNotExposeConnectReasonPhrase(t *testing.T) {
	const secret = "proxy-secret-reason"
	proxyURL := startConnectProxy(t, func(conn net.Conn) {
		_, _ = io.WriteString(conn, "HTTP/1.1 407 "+secret+"\r\nContent-Length: 0\r\n\r\n")
	})
	dialer := NewHTTPProxyDialer(&Profile{Name: "test"}, proxyURL)

	conn, err := dialer.DialTLSContext(t.Context(), "tcp", "example.com:443")
	require.ErrorContains(t, err, "status 407")
	require.NotContains(t, err.Error(), secret)
	require.Nil(t, conn)
}

func TestSOCKS5ProxyDialerHonorsContextDuringHandshake(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	go acceptOneStalledConnection(listener)

	proxyURL, err := url.Parse("socks5h://" + listener.Addr().String())
	require.NoError(t, err)
	dialer := NewSOCKS5ProxyDialer(&Profile{Name: "test"}, proxyURL)
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	started := time.Now()
	conn, err := dialer.DialTLSContext(ctx, "tcp", "example.com:443")
	require.Error(t, err)
	require.Nil(t, conn)
	require.Less(t, time.Since(started), 2*time.Second)
}

func TestTLSFingerprintHandshakeSendsSNIAndVerifiesCertificate(t *testing.T) {
	sni := make(chan string, 1)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.TLS = &tls.Config{
		GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
			sni <- hello.ServerName
			return nil, nil
		},
	}
	server.StartTLS()
	t.Cleanup(server.Close)

	dialer := NewDialer(&Profile{Name: "test"}, func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	})
	conn, err := dialer.DialTLSContext(t.Context(), "tcp", "example.com:443")
	require.Error(t, err)
	require.Nil(t, conn)
	require.Contains(t, err.Error(), "certificate")
	select {
	case observedSNI := <-sni:
		require.Equal(t, "example.com", observedSNI)
	case <-time.After(time.Second):
		t.Fatal("TLS server did not observe ClientHello SNI")
	}
}

func TestClientHelloNeverOffersTLSBelow12(t *testing.T) {
	spec := buildClientHelloSpecFromProfile(&Profile{
		Name:              "weak-version-request",
		SupportedVersions: []uint16{tls.VersionTLS10},
	})

	require.Equal(t, uint16(tls.VersionTLS12), spec.TLSVersMin)
	require.Equal(t, uint16(tls.VersionTLS13), spec.TLSVersMax)
}

func startConnectProxy(t *testing.T, respond func(net.Conn)) *url.URL {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		reader := bufio.NewReader(conn)
		for {
			line, readErr := reader.ReadString('\n')
			if readErr != nil {
				return
			}
			if line == "\r\n" {
				break
			}
		}
		respond(conn)
	}()
	proxyURL, err := url.Parse("http://" + listener.Addr().String())
	require.NoError(t, err)
	return proxyURL
}

func acceptOneStalledConnection(listener net.Listener) {
	conn, err := listener.Accept()
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()
	_, _ = io.Copy(io.Discard, conn)
}
