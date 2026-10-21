//go:build integration

package repository

import (
	"errors"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func workbenchRepoInput(key int64) service.WorkbenchInput {
	return service.WorkbenchInput{RequestID: uuid.NewString(), APIKeyID: key, Protocol: "images", Model: "mock-image", Prompt: "测试", Count: 1, Size: "auto", Quality: "auto", ReferenceIDs: []string{}}
}
func workbenchRepoAsset(size int64) service.WorkbenchAsset {
	return service.WorkbenchAsset{ID: uuid.NewString(), Filename: "test.png", MimeType: "image/png", Size: size, Width: 1, Height: 1, Draft: true}
}
func TestWorkbenchRepositoryIsolationAndIdempotency(t *testing.T) {
	ctx, _, a, b, key := chatFixture(t)
	r := NewImageWorkbenchRepository(integrationDB)
	in := workbenchRepoInput(key)
	asset := workbenchRepoAsset(123)
	require.NoError(t, r.AddAsset(ctx, a, asset, func() error { return nil }))
	in.ReferenceIDs = []string{asset.ID}
	in.Count = 2
	_, err := r.Asset(ctx, b, asset.ID)
	require.ErrorIs(t, err, service.ErrWorkbenchNotFound)
	_, _, err = r.Create(ctx, b, in)
	require.ErrorIs(t, err, service.ErrWorkbenchNotFound)
	job, existing, err := r.Create(ctx, a, in)
	require.NoError(t, err)
	require.False(t, existing)
	u, err := r.Usage(ctx, a)
	require.NoError(t, err)
	require.EqualValues(t, 123, u.UsedBytes)
	require.Equal(t, 2*service.WorkbenchFileLimit, u.ReservedBytes)
	repeated, existing, err := r.Create(ctx, a, in)
	require.NoError(t, err)
	require.True(t, existing)
	require.Equal(t, job.ID, repeated.ID)
	changed := in
	changed.Prompt = "changed"
	_, _, err = r.Create(ctx, a, changed)
	require.ErrorIs(t, err, service.ErrWorkbenchConflict)
	changed.RequestID = uuid.NewString()
	_, _, err = r.Create(ctx, a, changed)
	require.ErrorIs(t, err, service.ErrWorkbenchBusy)
	_, err = r.Get(ctx, b, job.ID)
	require.ErrorIs(t, err, service.ErrWorkbenchNotFound)
	require.ErrorIs(t, r.Finish(ctx, b, job.ID, "failed"), service.ErrWorkbenchNotFound)
	require.ErrorIs(t, r.Delete(ctx, a, job.ID, func(service.WorkbenchAsset) error { return nil }), service.ErrWorkbenchBusy)
	require.ErrorIs(t, r.DeleteAsset(ctx, a, asset.ID, func(service.WorkbenchAsset) error { return nil }), service.ErrWorkbenchBusy)
	result := workbenchRepoAsset(456)
	result.Draft = false
	item := service.WorkbenchItem{Position: 0, Status: "complete", AssetID: &result.ID}
	require.NoError(t, r.SaveItem(ctx, a, job.ID, item, &result, func() error { return nil }))
	require.Error(t, r.SaveItem(ctx, a, job.ID, item, &result, func() error { return nil }))
	require.NoError(t, r.Finish(ctx, a, job.ID, "partial"))
	detail, err := r.Get(ctx, a, job.ID)
	require.NoError(t, err)
	require.Len(t, detail.Items, 2)
	require.Equal(t, "failed", detail.Items[1].Status)
	require.Len(t, detail.Assets, 2)
	u, err = r.Usage(ctx, a)
	require.NoError(t, err)
	require.Zero(t, u.ReservedBytes)
	require.EqualValues(t, 579, u.UsedBytes)
	// 新任务引用旧结果时，删除旧任务不能删除共享图片；最后一个引用删除后回收。
	next := workbenchRepoInput(key)
	next.ReferenceIDs = []string{result.ID}
	second, _, err := r.Create(ctx, a, next)
	require.NoError(t, err)
	require.NoError(t, r.Finish(ctx, a, second.ID, "failed"))
	removed := []string{}
	require.NoError(t, r.Delete(ctx, a, job.ID, func(a service.WorkbenchAsset) error { removed = append(removed, a.ID); return nil }))
	require.Equal(t, []string{asset.ID}, removed)
	_, err = r.Asset(ctx, a, result.ID)
	require.NoError(t, err)
	require.NoError(t, r.Delete(ctx, a, second.ID, func(a service.WorkbenchAsset) error { removed = append(removed, a.ID); return nil }))
	require.Equal(t, []string{asset.ID, result.ID}, removed)
	u, err = r.Usage(ctx, a)
	require.NoError(t, err)
	require.Zero(t, u.UsedBytes)
}
func TestWorkbenchRepositoryQuotaAndRollback(t *testing.T) {
	ctx, _, a, _, key := chatFixture(t)
	r := NewImageWorkbenchRepository(integrationDB)
	for i := 0; i < 4; i++ {
		require.NoError(t, r.AddAsset(ctx, a, workbenchRepoAsset(20_000_000), func() error { return nil }))
	}
	_, _, err := r.Create(ctx, a, workbenchRepoInput(key))
	require.ErrorIs(t, err, service.ErrWorkbenchQuota)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- r.AddAsset(ctx, a, workbenchRepoAsset(15_000_000), func() error { return nil })
		}()
	}
	wg.Wait()
	close(errs)
	success := 0
	for err := range errs {
		if err == nil {
			success++
		} else {
			require.ErrorIs(t, err, service.ErrWorkbenchQuota)
		}
	}
	require.Equal(t, 1, success)
	u, err := r.Usage(ctx, a)
	require.NoError(t, err)
	require.EqualValues(t, 95_000_000, u.UsedBytes)
	writeErr := errors.New("磁盘写入失败")
	require.ErrorIs(t, r.AddAsset(ctx, a, workbenchRepoAsset(1), func() error { return writeErr }), writeErr)
	u, err = r.Usage(ctx, a)
	require.NoError(t, err)
	require.EqualValues(t, 95_000_000, u.UsedBytes)
}
func TestWorkbenchRepositoryRestartAndDeletionFailure(t *testing.T) {
	ctx, _, a, _, key := chatFixture(t)
	r := NewImageWorkbenchRepository(integrationDB)
	asset := workbenchRepoAsset(111)
	require.NoError(t, r.AddAsset(ctx, a, asset, func() error { return nil }))
	in := workbenchRepoInput(key)
	in.ReferenceIDs = []string{asset.ID}
	job, _, err := r.Create(ctx, a, in)
	require.NoError(t, err)
	require.NoError(t, r.Interrupt(ctx))
	detail, err := r.Get(ctx, a, job.ID)
	require.NoError(t, err)
	require.Equal(t, "interrupted", detail.Status)
	require.Equal(t, "interrupted", detail.Items[0].Status)
	u, err := r.Usage(ctx, a)
	require.NoError(t, err)
	require.Zero(t, u.ReservedBytes)
	removeErr := errors.New("文件清理失败")
	require.ErrorIs(t, r.Delete(ctx, a, job.ID, func(service.WorkbenchAsset) error { return removeErr }), removeErr)
	detail, err = r.Get(ctx, a, job.ID)
	require.NoError(t, err)
	require.True(t, detail.Deleting)
	next := workbenchRepoInput(key)
	next.ReferenceIDs = []string{asset.ID}
	_, _, err = r.Create(ctx, a, next)
	require.ErrorIs(t, err, service.ErrWorkbenchBusy)
	u, err = r.Usage(ctx, a)
	require.NoError(t, err)
	require.EqualValues(t, 111, u.UsedBytes)
	require.Zero(t, u.ReservedBytes)
	require.NoError(t, r.Delete(ctx, a, job.ID, func(service.WorkbenchAsset) error { return nil }))
	_, err = r.Get(ctx, a, job.ID)
	require.ErrorIs(t, err, service.ErrWorkbenchNotFound)
}
