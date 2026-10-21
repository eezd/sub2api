//go:build integration

package repository

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func chatFixture(t *testing.T) (context.Context, service.ChatRepository, int64, int64, int64) {
	t.Helper()
	ctx := context.Background()
	client := testEntClient(t)
	a := mustCreateUser(t, client, &service.User{})
	b := mustCreateUser(t, client, &service.User{})
	key, err := client.APIKey.Create().SetUserID(a.ID).SetKey(uuid.NewString()).SetName("Chat integration").Save(ctx)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(ctx, `DELETE FROM users WHERE id IN ($1,$2)`, a.ID, b.ID)
		require.NoError(t, err)
	})
	return ctx, NewChatRepository(integrationDB), a.ID, b.ID, key.ID
}
func TestChatOwnershipTurnsAndSnapshots(t *testing.T) {
	ctx, r, a, b, key := chatFixture(t)
	c, err := r.Create(ctx, a, uuid.NewString())
	require.NoError(t, err)
	_, err = r.Get(ctx, b, c.ID)
	require.ErrorIs(t, err, service.ErrChatNotFound)
	in := service.ChatTurnInput{RequestID: uuid.NewString(), APIKeyID: key, Model: "test-model", Content: "第一条消息", HistoryUpdatedAt: c.UpdatedAt}
	_, err = r.StartTurn(ctx, b, c.ID, in)
	require.ErrorIs(t, err, service.ErrChatNotFound)
	turn, err := r.StartTurn(ctx, a, c.ID, in)
	require.NoError(t, err)
	repeated, err := r.StartTurn(ctx, a, c.ID, in)
	require.NoError(t, err)
	require.True(t, repeated.Existing)
	require.Equal(t, turn.AssistantMessageID, repeated.AssistantMessageID)
	in.RequestID = uuid.NewString()
	_, err = r.StartTurn(ctx, a, c.ID, in)
	require.ErrorIs(t, err, service.ErrChatBusy)
	require.ErrorIs(t, r.SaveReply(ctx, b, c.ID, turn.AssistantMessageID, service.ChatSnapshot{Status: "complete", Revision: 1}), service.ErrChatNotFound)
	require.NoError(t, r.SaveReply(ctx, a, c.ID, turn.AssistantMessageID, service.ChatSnapshot{Content: "新内容", Status: "generating", Revision: 2}))
	require.NoError(t, r.SaveReply(ctx, a, c.ID, turn.AssistantMessageID, service.ChatSnapshot{Content: "旧内容", Status: "generating", Revision: 1}))
	d, err := r.Get(ctx, a, c.ID)
	require.NoError(t, err)
	require.Len(t, d.Messages, 2)
	require.Equal(t, "user", d.Messages[0].Role)
	require.Equal(t, "新内容", d.Messages[1].Content)
	require.Equal(t, "第一条消息", d.Conversation.Title)
	require.NoError(t, r.SaveReply(ctx, a, c.ID, turn.AssistantMessageID, service.ChatSnapshot{Content: "最终内容", Status: "complete", Revision: 3}))
	require.Error(t, r.SaveReply(ctx, a, c.ID, turn.AssistantMessageID, service.ChatSnapshot{Status: "generating", Revision: 4}))
	_, err = r.StartTurn(ctx, a, c.ID, in)
	require.ErrorIs(t, err, service.ErrChatHistory, "拒绝其他窗口完成一轮后仍使用旧历史的请求")
	d, err = r.Get(ctx, a, c.ID)
	require.NoError(t, err)
	require.Len(t, d.Messages, 2, "版本冲突不得写入新消息")
	in.HistoryUpdatedAt = d.Conversation.UpdatedAt
	_, err = r.StartTurn(ctx, a, c.ID, in)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, `UPDATE chat_messages SET updated_at=NOW()-INTERVAL '31 seconds' WHERE conversation_id=$1 AND status='generating'`, c.ID)
	require.NoError(t, err)
	// 相同时间戳也必须保留用户/助手及轮次顺序，不能依赖事务开始时间排序。
	_, err = integrationDB.ExecContext(ctx, `UPDATE chat_messages SET created_at='2026-10-20T00:00:00Z' WHERE conversation_id=$1`, c.ID)
	require.NoError(t, err)
	d, err = r.Get(ctx, a, c.ID)
	require.NoError(t, err)
	require.Equal(t, "interrupted", d.Messages[3].Status)
	require.Error(t, r.SaveReply(ctx, a, c.ID, d.Messages[3].ID, service.ChatSnapshot{Status: "generating", Revision: 1}))
	require.NoError(t, r.SaveReply(ctx, a, c.ID, d.Messages[3].ID, service.ChatSnapshot{Content: "恢复保存的最终回复", Status: "complete", Revision: 2}))
	d, err = r.Get(ctx, a, c.ID)
	require.NoError(t, err)
	require.Equal(t, "complete", d.Messages[3].Status)
	require.Equal(t, "恢复保存的最终回复", d.Messages[3].Content)
}
func TestChatQuotaConcurrentUploads(t *testing.T) {
	ctx, r, a, b, _ := chatFixture(t)
	c, err := r.Create(ctx, a, uuid.NewString())
	require.NoError(t, err)
	makeAttachment := func(size int64) service.ChatAttachment {
		return service.ChatAttachment{ID: uuid.NewString(), ConversationID: c.ID, Filename: "image.png", MimeType: "image/png", Size: size}
	}
	for i := 0; i < 4; i++ {
		require.NoError(t, r.AddAttachment(ctx, a, makeAttachment(20_000_000), func() error { return nil }))
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			attachment := makeAttachment(18_000_000)
			attachment.Filename = "document.pdf"
			attachment.MimeType = "application/pdf"
			attachment.ExtractedText = strings.Repeat("a", service.ChatTextLimit)
			errs <- r.AddAttachment(ctx, a, attachment, func() error { return nil })
		}()
	}
	wg.Wait()
	close(errs)
	success, quota := 0, 0
	for err := range errs {
		if err == nil {
			success++
		} else {
			require.ErrorIs(t, err, service.ErrChatStorage)
			quota++
		}
	}
	require.Equal(t, 1, success)
	require.Equal(t, 1, quota)
	used, err := r.StorageUsed(ctx, a)
	require.NoError(t, err)
	require.EqualValues(t, 100_000_000, used)
	_, err = r.Attachment(ctx, b, makeAttachment(1).ID)
	require.ErrorIs(t, err, service.ErrChatNotFound)
}
func TestChatAttachmentLifecycleAndCleanupFailures(t *testing.T) {
	ctx, r, a, b, key := chatFixture(t)
	cfg := &config.Config{Chat: config.ChatConfig{AttachmentDir: t.TempDir()}}
	svc := service.NewChatService(r, cfg)
	c, err := svc.Create(ctx, a)
	require.NoError(t, err)
	_, err = svc.Upload(ctx, b, c.ID, "not-owned.txt", "", strings.NewReader("test"))
	require.ErrorIs(t, err, service.ErrChatNotFound)
	file, err := svc.Upload(ctx, a, c.ID, "../../notes.txt", "", strings.NewReader("私有文件"))
	require.NoError(t, err)
	require.Equal(t, "notes.txt", file.Filename)
	_, _, err = svc.OpenAttachment(ctx, b, file.ID)
	require.ErrorIs(t, err, service.ErrChatNotFound)
	path := filepath.Join(cfg.Chat.AttachmentDir, c.ID, file.ID)
	require.NoError(t, os.Remove(path))
	require.NoError(t, os.Mkdir(path, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(path, "blocked"), []byte("x"), 0600))
	require.Error(t, svc.DeleteAttachment(ctx, a, file.ID))
	_, err = r.Attachment(ctx, a, file.ID)
	require.NoError(t, err, "清理失败保留记录和配额，允许重试")
	require.NoError(t, os.RemoveAll(path))
	require.NoError(t, os.WriteFile(path, []byte("私有文件"), 0600))
	turn, err := svc.StartTurn(ctx, a, c.ID, service.ChatTurnInput{RequestID: uuid.NewString(), APIKeyID: key, Model: "test", AttachmentIDs: []string{file.ID}, HistoryUpdatedAt: c.UpdatedAt})
	require.NoError(t, err)
	require.Error(t, svc.DeleteAttachment(ctx, a, file.ID))
	require.ErrorIs(t, svc.Delete(ctx, a, c.ID), service.ErrChatBusy)
	require.NoError(t, svc.SaveReply(ctx, a, c.ID, turn.AssistantMessageID, service.ChatSnapshot{Content: "好", Status: "complete", Revision: 1}))
	require.NoError(t, svc.Delete(ctx, a, c.ID))
	_, err = os.Stat(path)
	require.True(t, os.IsNotExist(err))
	used, err := svc.StorageUsed(ctx, a)
	require.NoError(t, err)
	require.Zero(t, used)
	_, err = r.Get(ctx, a, c.ID)
	require.ErrorIs(t, err, service.ErrChatNotFound)
	c, err = svc.Create(ctx, a)
	require.NoError(t, err)
	fail := errors.New("模拟落盘失败")
	err = r.AddAttachment(ctx, a, service.ChatAttachment{ID: uuid.NewString(), ConversationID: c.ID, Filename: "failed.txt", MimeType: "text/plain", Size: 100}, func() error { return fail })
	require.ErrorIs(t, err, fail)
	used, err = svc.StorageUsed(ctx, a)
	require.NoError(t, err)
	require.Zero(t, used)
}

func TestChatQuotaIncludesExtractedText(t *testing.T) {
	ctx, r, uid, _, _ := chatFixture(t)
	c, err := r.Create(ctx, uid, uuid.NewString())
	require.NoError(t, err)
	// 多字节文本按 UTF-8 字节计入配额；极小 PDF 头也不能放大数据库占用。
	text := strings.Repeat("文", 600_000)
	a := service.ChatAttachment{ConversationID: c.ID, Filename: "small.pdf", MimeType: "application/pdf", Size: 9, ExtractedText: text}
	for i := 0; i < 55; i++ {
		a.ID = uuid.NewString()
		require.NoError(t, r.AddAttachment(ctx, uid, a, func() error { return nil }))
	}
	used, err := r.StorageUsed(ctx, uid)
	require.NoError(t, err)
	require.EqualValues(t, 55*(9+len(text)), used)
	a.ID = uuid.NewString()
	called := false
	err = r.AddAttachment(ctx, uid, a, func() error { called = true; return nil })
	require.ErrorIs(t, err, service.ErrChatStorage)
	require.False(t, called, "超限时不得落盘")
	_, err = r.Attachment(ctx, uid, a.ID)
	require.ErrorIs(t, err, service.ErrChatNotFound)

	d, err := r.Get(ctx, uid, c.ID)
	require.NoError(t, err)
	require.NoError(t, r.DeleteAttachment(ctx, uid, d.Attachments[0].ID, func(service.ChatAttachment) error { return nil }))
	require.NoError(t, r.AddAttachment(ctx, uid, a, func() error { return nil }), "删除附件后同时释放文件与文本配额")
}

func TestChatMigrationCoexistsWithLaterUpstreamNumber(t *testing.T) {
	ctx, r, uid, _, _ := chatFixture(t)
	c, err := r.Create(ctx, uid, uuid.NewString())
	require.NoError(t, err)
	const custom = "custom_eezd_20261021_001_chat.sql"
	const upstream = "292_chat_upstream_test.sql"
	content, err := migrations.FS.ReadFile(custom)
	require.NoError(t, err)
	files := fstest.MapFS{
		custom:   &fstest.MapFile{Data: content},
		upstream: &fstest.MapFile{Data: []byte("CREATE TABLE chat_upstream_migration_test (id BIGINT PRIMARY KEY);")},
	}
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(ctx, `DROP TABLE IF EXISTS chat_upstream_migration_test`)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(ctx, `DELETE FROM schema_migrations WHERE filename=$1`, upstream)
		require.NoError(t, err)
	})
	for i := 0; i < 2; i++ {
		require.NoError(t, applyMigrationsFS(ctx, integrationDB, files))
		var applied int
		require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE filename IN ($1,$2)`, custom, upstream).Scan(&applied))
		require.Equal(t, 2, applied)
		d, err := r.Get(ctx, uid, c.ID)
		require.NoError(t, err)
		require.Equal(t, c.ID, d.Conversation.ID, "自有迁移不得重跑或破坏原有聊天数据")
	}
}
