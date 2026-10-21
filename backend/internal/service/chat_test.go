//go:build unit

package service

import (
	"bytes"
	"image"
	"image/png"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestChatAttachmentContent(t *testing.T) {
	var picture bytes.Buffer
	require.NoError(t, png.Encode(&picture, image.NewRGBA(image.Rect(0, 0, 2, 2))))
	mime, text, err := chatAttachmentContent("测试.png", picture.Bytes(), "")
	require.NoError(t, err)
	require.Equal(t, "image/png", mime)
	require.Empty(t, text)
	_, text, err = chatAttachmentContent("代码.ts", []byte("const 名字 = '测试'"), "")
	require.NoError(t, err)
	require.Contains(t, text, "测试")
	_, text, err = chatAttachmentContent("文档.pdf", []byte("%PDF-1.7\n"), "有效文字")
	require.NoError(t, err)
	require.Equal(t, "有效文字", text)
	for _, tc := range []struct {
		name      string
		data      []byte
		extracted string
	}{
		{"伪图.png", []byte("not an image"), ""},
		{"empty.pdf", []byte("%PDF-1.7\n"), ""},
		{"bad.pdf", []byte("plain text"), "伪造提取文本"},
		{"bad.txt", []byte{0xff, 0xfe}, ""},
		{"binary.txt", []byte{'a', 0, 'b'}, ""},
		{"large.txt", []byte(strings.Repeat("a", ChatTextLimit+1)), ""},
		{"script.exe", []byte("test"), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := chatAttachmentContent(tc.name, tc.data, tc.extracted)
			require.Error(t, err)
		})
	}
}
func TestChatRequestLimitUsesIngressConfig(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.MaxBodySize = 256 << 20
	cfg.Server.MaxRequestBodySize = 100_000_000
	svc := NewChatService(nil, cfg)
	require.EqualValues(t, 95_000_000, svc.RequestLimit())
	cfg.Gateway.MaxBodySize = 10 << 20
	svc = NewChatService(nil, cfg)
	require.EqualValues(t, 10<<20, svc.RequestLimit())
}
