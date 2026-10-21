package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

const (
	WorkbenchStorageLimit     int64 = 100_000_000
	WorkbenchFileLimit        int64 = 20 << 20
	WorkbenchRequestLimit     int64 = 100_000_000
	workbenchExecutionTimeout       = 30 * time.Minute
)

var (
	ErrWorkbenchNotFound = infraerrors.NotFound("IMAGE_WORKBENCH_NOT_FOUND", "生图任务或图片不存在")
	ErrWorkbenchBusy     = infraerrors.Conflict("IMAGE_WORKBENCH_BUSY", "已有任务正在生成或资源仍被使用，请稍后重试")
	ErrWorkbenchQuota    = infraerrors.BadRequest("IMAGE_WORKBENCH_QUOTA", "生图存储空间不足，参考图与生成结果合计最多 100 MB，请先清理历史或参考图")
	ErrWorkbenchConflict = infraerrors.Conflict("IMAGE_WORKBENCH_REQUEST_CONFLICT", "请求标识对应的参数不一致，请刷新后重试")
)

type WorkbenchInput struct {
	RequestID    string   `json:"request_id"`
	APIKeyID     int64    `json:"api_key_id"`
	Protocol     string   `json:"protocol"`
	Model        string   `json:"model"`
	Prompt       string   `json:"prompt"`
	Count        int      `json:"count"`
	Size         string   `json:"size"`
	Quality      string   `json:"quality"`
	AspectRatio  string   `json:"aspect_ratio"`
	ImageSize    string   `json:"image_size"`
	ReferenceIDs []string `json:"reference_ids"`
}

type WorkbenchAsset struct {
	ID       string `json:"id"`
	Filename string `json:"filename"`
	MimeType string `json:"mime_type"`
	Size     int64  `json:"size"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	Draft    bool   `json:"draft"`
}

type WorkbenchItem struct {
	Position   int     `json:"position"`
	Status     string  `json:"status"`
	AssetID    *string `json:"asset_id"`
	Error      string  `json:"error"`
	DurationMS int64   `json:"duration_ms"`
}

type WorkbenchJob struct {
	ID        string           `json:"id"`
	APIKeyID  *int64           `json:"api_key_id"`
	Input     WorkbenchInput   `json:"input"`
	Status    string           `json:"status"`
	Deleting  bool             `json:"deleting"`
	CreatedAt time.Time        `json:"created_at"`
	UpdatedAt time.Time        `json:"updated_at"`
	Items     []WorkbenchItem  `json:"items"`
	Assets    []WorkbenchAsset `json:"assets"`
}

type WorkbenchUsage struct {
	UsedBytes     int64 `json:"used_bytes"`
	ReservedBytes int64 `json:"reserved_bytes"`
}

// 所有读写均按用户隔离，文件回调在配额事务内执行。
type ImageWorkbenchRepository interface {
	Usage(context.Context, int64) (WorkbenchUsage, error)
	List(context.Context, int64, int) ([]WorkbenchJob, error)
	Get(context.Context, int64, string) (WorkbenchJob, error)
	Drafts(context.Context, int64) ([]WorkbenchAsset, error)
	Asset(context.Context, int64, string) (WorkbenchAsset, error)
	AddAsset(context.Context, int64, WorkbenchAsset, func() error) error
	DeleteAsset(context.Context, int64, string, func(WorkbenchAsset) error) error
	Create(context.Context, int64, WorkbenchInput) (WorkbenchJob, bool, error)
	SaveItem(context.Context, int64, string, WorkbenchItem, *WorkbenchAsset, func() error) error
	Finish(context.Context, int64, string, string) error
	Delete(context.Context, int64, string, func(WorkbenchAsset) error) error
	Interrupt(context.Context) error
}

type ImageWorkbenchService struct {
	repo         ImageWorkbenchRepository
	keys         *APIKeyService
	upstream     HTTPUpstream
	gateway      http.Handler
	dir          string
	requestLimit int64
	mu           sync.Mutex
	stopping     bool
	cancels      map[string]context.CancelFunc
	wg           sync.WaitGroup
}

func NewImageWorkbenchService(repo ImageWorkbenchRepository, keys *APIKeyService, upstream HTTPUpstream, cfg *config.Config) (*ImageWorkbenchService, error) {
	base := strings.TrimSpace(os.Getenv("DATA_DIR"))
	if base == "" {
		base = "./data"
	}
	limit := WorkbenchRequestLimit
	for _, configured := range []int64{cfg.Gateway.MaxBodySize, cfg.Server.MaxRequestBodySize} {
		if configured > 0 && configured < limit {
			limit = configured
		}
	}
	s := &ImageWorkbenchService{repo: repo, keys: keys, upstream: upstream, dir: filepath.Join(base, "image-workbench"), requestLimit: limit, cancels: make(map[string]context.CancelFunc)}
	// 当前单实例部署不接管旧进程的付费请求，启动时明确中断并归还预留空间。
	if cfg.RunsBackgroundJobs() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := repo.Interrupt(ctx); err != nil {
			return nil, fmt.Errorf("恢复生图任务状态失败: %w", err)
		}
	}
	return s, nil
}

// BindGateway 在路由注册完成后绑定同一个路由器，内部请求仍经过完整网关中间件。
func (s *ImageWorkbenchService) BindGateway(gateway http.Handler) { s.gateway = gateway }

func (s *ImageWorkbenchService) RequestLimit() int64 { return s.requestLimit }

func (s *ImageWorkbenchService) Usage(ctx context.Context, uid int64) (WorkbenchUsage, error) {
	return s.repo.Usage(ctx, uid)
}
func (s *ImageWorkbenchService) List(ctx context.Context, uid int64, page int) ([]WorkbenchJob, error) {
	return s.repo.List(ctx, uid, page)
}
func (s *ImageWorkbenchService) Get(ctx context.Context, uid int64, id string) (WorkbenchJob, error) {
	return s.repo.Get(ctx, uid, id)
}
func (s *ImageWorkbenchService) Drafts(ctx context.Context, uid int64) ([]WorkbenchAsset, error) {
	return s.repo.Drafts(ctx, uid)
}

func (s *ImageWorkbenchService) assetPath(uid int64, id string) string {
	return filepath.Join(s.dir, fmt.Sprint(uid), id)
}

func workbenchImageInfo(data []byte) (string, int, int, error) {
	mime := http.DetectContentType(data)
	if mime != "image/png" && mime != "image/jpeg" && mime != "image/webp" {
		return "", 0, 0, errors.New("仅支持有效的 PNG、JPEG、WebP 图片")
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return "", 0, 0, errors.New("无法读取图片尺寸")
	}
	return mime, cfg.Width, cfg.Height, nil
}

func (s *ImageWorkbenchService) writeAsset(uid int64, a WorkbenchAsset, data []byte) error {
	dir := filepath.Dir(s.assetPath(uid, a.ID))
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("创建生图目录失败: %w", err)
	}
	if err := os.WriteFile(s.assetPath(uid, a.ID), data, 0600); err != nil {
		return fmt.Errorf("保存图片失败: %w", err)
	}
	return nil
}

func (s *ImageWorkbenchService) removeAsset(uid int64, a WorkbenchAsset) error {
	if err := os.Remove(s.assetPath(uid, a.ID)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("删除图片失败: %w", err)
	}
	return nil
}

func (s *ImageWorkbenchService) Upload(ctx context.Context, uid int64, name string, r io.Reader) (WorkbenchAsset, error) {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	if name == "." || name == "" || len(name) > 255 || !chatTextValid(name) {
		return WorkbenchAsset{}, infraerrors.BadRequest("IMAGE_WORKBENCH_FILENAME", "图片名称无效")
	}
	data, err := io.ReadAll(io.LimitReader(r, WorkbenchFileLimit+1))
	if err != nil {
		return WorkbenchAsset{}, fmt.Errorf("读取图片失败: %w", err)
	}
	if len(data) == 0 || int64(len(data)) > WorkbenchFileLimit {
		return WorkbenchAsset{}, infraerrors.BadRequest("IMAGE_WORKBENCH_FILE_LIMIT", "单张图片必须非空且不超过 20 MiB")
	}
	mime, w, h, err := workbenchImageInfo(data)
	if err != nil {
		return WorkbenchAsset{}, infraerrors.BadRequest("IMAGE_WORKBENCH_FILE_TYPE", err.Error())
	}
	a := WorkbenchAsset{ID: uuid.NewString(), Filename: name, MimeType: mime, Size: int64(len(data)), Width: w, Height: h, Draft: true}
	err = s.repo.AddAsset(ctx, uid, a, func() error { return s.writeAsset(uid, a, data) })
	if err != nil {
		if cleanup := s.removeAsset(uid, a); cleanup != nil {
			return a, fmt.Errorf("登记图片失败: %w；%v", err, cleanup)
		}
	}
	return a, err
}

func (s *ImageWorkbenchService) Open(ctx context.Context, uid int64, id string) (WorkbenchAsset, *os.File, error) {
	a, err := s.repo.Asset(ctx, uid, id)
	if err != nil {
		return a, nil, err
	}
	f, err := os.Open(s.assetPath(uid, id))
	return a, f, err
}

func (s *ImageWorkbenchService) DeleteAsset(ctx context.Context, uid int64, id string) error {
	return s.repo.DeleteAsset(ctx, uid, id, func(a WorkbenchAsset) error { return s.removeAsset(uid, a) })
}
func (s *ImageWorkbenchService) Delete(ctx context.Context, uid int64, id string) error {
	return s.repo.Delete(ctx, uid, id, func(a WorkbenchAsset) error { return s.removeAsset(uid, a) })
}

func validateWorkbenchInput(in *WorkbenchInput) error {
	id, err := uuid.Parse(in.RequestID)
	if err != nil {
		return infraerrors.BadRequest("IMAGE_WORKBENCH_INPUT", "请求标识无效")
	}
	in.RequestID = id.String()
	in.Model = strings.TrimSpace(in.Model)
	in.Prompt = strings.TrimSpace(in.Prompt)
	if in.APIKeyID <= 0 || in.Model == "" || len(in.Model) > 200 || !chatTextValid(in.Model) || !utf8.ValidString(in.Prompt) || strings.ContainsRune(in.Prompt, 0) || len(in.Prompt) == 0 || utf8.RuneCountInString(in.Prompt) > 8000 || in.Count < 1 || in.Count > 4 || len(in.ReferenceIDs) > 5 {
		return infraerrors.BadRequest("IMAGE_WORKBENCH_INPUT", "密钥、模型、提示词或数量无效；提示词最多 8000 字，每次生成 1 至 4 张，参考图最多 5 张")
	}
	switch in.Protocol {
	case "images":
		switch in.Size {
		case "auto", "1024x1024", "1536x1024", "1024x1536":
		default:
			return infraerrors.BadRequest("IMAGE_WORKBENCH_INPUT", "图片尺寸无效")
		}
		switch in.Quality {
		case "auto", "low", "medium", "high":
		default:
			return infraerrors.BadRequest("IMAGE_WORKBENCH_INPUT", "图片质量无效")
		}
		if in.AspectRatio != "" || in.ImageSize != "" {
			return infraerrors.BadRequest("IMAGE_WORKBENCH_INPUT", "Images 请求不能携带 Gemini 参数")
		}
	case "gemini":
		if !IsSafeGeminiModelPathSegment(in.Model) {
			return infraerrors.BadRequest("IMAGE_WORKBENCH_INPUT", "Gemini 模型名称无效")
		}
		switch in.AspectRatio {
		case "auto", "1:1", "2:3", "3:2", "3:4", "4:3", "4:5", "5:4", "9:16", "16:9", "21:9":
		default:
			return infraerrors.BadRequest("IMAGE_WORKBENCH_INPUT", "图片比例无效")
		}
		switch in.ImageSize {
		case "auto", "1K", "2K", "4K":
		default:
			return infraerrors.BadRequest("IMAGE_WORKBENCH_INPUT", "图片分辨率无效")
		}
		if in.Size != "" || in.Quality != "" {
			return infraerrors.BadRequest("IMAGE_WORKBENCH_INPUT", "Gemini 请求不能携带 Images 参数")
		}
	default:
		return infraerrors.BadRequest("IMAGE_WORKBENCH_INPUT", "生图协议无效")
	}
	seen := make(map[string]bool)
	for i, id := range in.ReferenceIDs {
		parsed, err := uuid.Parse(id)
		if err != nil || seen[parsed.String()] {
			return infraerrors.BadRequest("IMAGE_WORKBENCH_INPUT", "参考图标识无效或重复")
		}
		in.ReferenceIDs[i] = parsed.String()
		seen[parsed.String()] = true
	}
	if in.ReferenceIDs == nil {
		in.ReferenceIDs = []string{}
	}
	return nil
}

func (s *ImageWorkbenchService) ownedKey(ctx context.Context, uid, id int64) (*APIKey, error) {
	key, err := s.keys.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if key.UserID != uid {
		return nil, ErrWorkbenchNotFound
	}
	return key, nil
}

func (s *ImageWorkbenchService) Submit(ctx context.Context, uid int64, in WorkbenchInput, original *http.Request) (WorkbenchJob, bool, error) {
	if err := validateWorkbenchInput(&in); err != nil {
		return WorkbenchJob{}, false, err
	}
	key, err := s.ownedKey(ctx, uid, in.APIKeyID)
	if err != nil {
		return WorkbenchJob{}, false, err
	}
	request, err := s.generationRequest(ctx, uid, in, key, original)
	if err != nil {
		return WorkbenchJob{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopping || s.gateway == nil {
		return WorkbenchJob{}, false, infraerrors.ServiceUnavailable("IMAGE_WORKBENCH_UNAVAILABLE", "生图服务正在停止，请稍后重试")
	}
	job, existing, err := s.repo.Create(ctx, uid, in)
	if err != nil || existing {
		return job, existing, err
	}
	taskCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), workbenchExecutionTimeout)
	s.cancels[job.ID] = cancel
	s.wg.Add(1)
	go s.run(taskCtx, cancel, uid, job, request)
	return job, false, nil
}

func (s *ImageWorkbenchService) Cancel(ctx context.Context, uid int64, id string) error {
	job, err := s.repo.Get(ctx, uid, id)
	if err != nil {
		return err
	}
	if job.Status != "running" {
		return nil
	}
	s.mu.Lock()
	cancel := s.cancels[id]
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	// 只请求取消，执行协程退出后才释放预留，避免旧任务仍在返回结果时新任务抢占。
	return nil
}

func (s *ImageWorkbenchService) Stop(ctx context.Context) error {
	s.mu.Lock()
	s.stopping = true
	for _, cancel := range s.cancels {
		cancel()
	}
	s.mu.Unlock()
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *ImageWorkbenchService) run(ctx context.Context, cancel context.CancelFunc, uid int64, job WorkbenchJob, template *http.Request) {
	defer s.wg.Done()
	defer cancel()
	defer func() { s.mu.Lock(); delete(s.cancels, job.ID); s.mu.Unlock() }()
	status := "failed"
	defer func() {
		if recovered := recover(); recovered != nil {
			logger.L().Error("生图任务执行异常", zap.String("task_id", job.ID), zap.Stack("stack"))
			status = "interrupted"
		}
		if ctx.Err() != nil && status != "complete" {
			s.mu.Lock()
			interrupted := s.stopping || errors.Is(ctx.Err(), context.DeadlineExceeded)
			s.mu.Unlock()
			if interrupted {
				status = "interrupted"
			} else {
				status = "cancelled"
			}
		}
		finishCtx, finishCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer finishCancel()
		if err := s.repo.Finish(finishCtx, uid, job.ID, status); err != nil {
			logger.L().Error("保存生图任务结束状态失败", zap.String("task_id", job.ID), zap.Error(err))
		}
	}()
	successes := 0
	for i := 0; i < job.Input.Count; i++ {
		if ctx.Err() != nil {
			return
		}
		start := time.Now()
		requestCtx := context.WithValue(ctx, ctxkey.ClientRequestID, uuid.NewString())
		request := template.Clone(requestCtx)
		body, err := template.GetBody()
		if err != nil {
			logger.L().Error("重新读取生图请求失败", zap.Error(err))
			return
		}
		request.Body = body
		request.Header.Set("X-Request-Id", uuid.NewString())
		result := newWorkbenchResponse(ctx)
		s.gateway.ServeHTTP(result, request)
		_ = body.Close()
		data, resultErr := s.readGenerationResult(ctx, job.Input.Protocol, result)
		item := WorkbenchItem{Position: i, Status: "failed", DurationMS: time.Since(start).Milliseconds()}
		var asset *WorkbenchAsset
		if resultErr == nil {
			mime, w, h, infoErr := workbenchImageInfo(data)
			if infoErr != nil {
				resultErr = infoErr
			} else {
				a := WorkbenchAsset{ID: uuid.NewString(), Filename: "generated" + extensionForContentType(mime), MimeType: mime, Size: int64(len(data)), Width: w, Height: h, Draft: false}
				asset = &a
				item.Status = "complete"
				item.AssetID = &a.ID
			}
		}
		if resultErr != nil && ctx.Err() != nil {
			return
		}
		if resultErr != nil {
			item.Error = workbenchErrorMessage(resultErr, template.Header.Get("Authorization"))
		}
		saveCtx, saveCancel := context.WithTimeout(context.Background(), 10*time.Second)
		err = s.repo.SaveItem(saveCtx, uid, job.ID, item, asset, func() error {
			if asset == nil {
				return nil
			}
			return s.writeAsset(uid, *asset, data)
		})
		saveCancel()
		if err != nil {
			if asset != nil {
				if cleanup := s.removeAsset(uid, *asset); cleanup != nil {
					logger.L().Error("清理未登记生图结果失败", zap.Error(cleanup))
				}
			}
			logger.L().Error("保存生图结果失败", zap.String("task_id", job.ID), zap.Error(err))
			return
		}
		if resultErr == nil {
			successes++
			status = "partial"
		}
	}
	switch {
	case successes == job.Input.Count:
		status = "complete"
	case successes > 0:
		status = "partial"
	}
}

func workbenchErrorMessage(err error, authorization string) string {
	message := err.Error()
	if key := strings.TrimPrefix(authorization, "Bearer "); key != "" {
		message = strings.ReplaceAll(message, key, "[REDACTED]")
	}
	if len(message) > 1000 {
		message = string([]rune(message)[:min(250, utf8.RuneCountInString(message))])
	}
	return message
}
