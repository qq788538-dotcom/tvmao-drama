package workflows

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/qq788538-dotcom/tvmao-drama/server/internal/assets"
)

// copyConcurrency 是同时在飞的 CopyObject 数。8 是权衡：对象存储的服务端复制不占我们
// 的带宽，瓶颈在每次调用的往返延迟，并发度线性缩短总耗时；但每个 worker 还会做两次 DB
// 查询（ListByObjectKey / FindByUserProjectHash）加一次 Insert，开太大会在项目数多时
// 抢连接池。8 路能把 100 个对象从 10~30 秒压到 2~4 秒，足够把「卡死感」消掉。
const copyConcurrency = 8

// userStorageAdjuster 对应 users repo 中上传/项目路径用到的现有方法。
// 我们把它保留为接口，这样 duplicator 不必硬依赖 users 包。
type userStorageAdjuster interface {
	AddStorage(id int64, delta int64) error
}

// cleanupQueue 接收孤儿 TOS key，在 duplication 调用中途失败时异步删除。
// 实现在 cleanup.go 里；这里声明为接口，方便测试打桩。
type cleanupQueue interface {
	EnqueueObjectKey(key string) error
}

// AssetDuplicator 负责发布/克隆时的逻辑：把一个项目引用的 TOS 对象镜像到
// 另一个用户名下（workflow-freeze 场景下则镜像到同一用户名下）。它用云原生的
// CopyObject API——无需下载再上传——并在失败时通过尽力而为的清理队列把存储退回。
type AssetDuplicator struct {
	storage assets.Storage
	assets  assets.Repository
	users   userStorageAdjuster
	gc      cleanupQueue
}

func NewAssetDuplicator(storage assets.Storage, assetsRepo assets.Repository, users userStorageAdjuster, gc cleanupQueue) *AssetDuplicator {
	return &AssetDuplicator{storage: storage, assets: assetsRepo, users: users, gc: gc}
}

// DuplicateResult 汇总调用方在成功执行后需要的东西：重写后的 snapshot
// （已做 URL 替换）、新的 asset ID（好让发布路径填充 shared_workflow_assets
// 关联表），以及拷贝的总 byte_size（计入目标用户的配额，并存到 workflow 行上
// 供 UI 展示）。
type DuplicateResult struct {
	Snapshot    json.RawMessage
	NewAssetIDs []int64
	BytesCopied int64 // 新写入的字节数（不含命中 hash 去重的部分）
}

// Duplicate 是对外的入口。
//
// `targetUserID` 成为每一条新 asset 记录的 owner。
// `targetProjectID` > 0 → asset 记录挂到该项目（克隆路径）；
// == 0 → asset 记录不挂任何项目（发布冻结路径）。
// `sourceKind` 给记录打上遥测标签（`workflow_freeze`、`workflow_clone`）。
//
// 算法遵循两阶段约定：
//
//	阶段 A：收集 snapshot 引用的每个 URL，把每个 URL 解析到一条源 asset 记录
//	（跳过不在我们 bucket 里的 URL），把字节拷贝到目标用户名下的新 key，
//	并暂存 (key, asset_id) 对。这里任何失败都会取消进行中的 CopyObject
//	goroutine，并把已经拷贝过的 key 喂进 GC 队列。
//
//	阶段 B：在所有 CopyObject 完成后，把 snapshot 里的 URL 重写为指向新 key。
//	这一步是纯 JSON 操作——没有 IO——所以不会有竞态或部分失败。
func (d *AssetDuplicator) Duplicate(
	ctx context.Context,
	targetUserID, targetProjectID int64,
	sourceKind string,
	snapshot json.RawMessage,
) (*DuplicateResult, error) {
	if d == nil {
		return nil, errors.New("duplicator is nil")
	}

	// 从 snapshot 里收集去重后的源 key。解析不出我们 bucket 内 key 的字符串
	// 会被跳过（外部 CDN 等）。
	keys := d.collectKeys(snapshot)
	if len(keys) == 0 {
		return &DuplicateResult{Snapshot: snapshot}, nil
	}

	// 阶段 A：把每个源 key 拷贝到新的目标 key。建立 源 key → 目标 URL 的映射
	// 供重写步骤使用。
	//
	// 并发跑（copyConcurrency 路）。字节不经过我们的服务器——各后端用云原生的
	// CopyObject——所以成本是**每个对象一次网络往返**，与对象大小基本无关。串行时
	// 100 个对象就是 100 次往返串起来（10~30 秒），发布/克隆/项目副本三条路都卡在
	// 这里，大项目还会被 nginx 超时切断：后端其实跑完了，前端却报错，用户重试就复制
	// 出两份。
	//
	// **零锁**：每个 worker 只写 results[i] 这一个自己的下标，谁都不碰共享容器。
	// g.Wait() 返回后所有 worker 都已退出，此时读整个 results 是安全的——回滚要靠它
	// 找出「已经写出去的对象」，漏一个就是静默的存储泄漏，这是本函数最不能出错的地方。
	results := make([]*copyOutcome, len(keys))

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(copyConcurrency)
	for i, srcKey := range keys {
		i, srcKey := i, srcKey
		g.Go(func() error {
			out, err := d.copyOne(gctx, targetUserID, targetProjectID, sourceKind, srcKey)
			// **先记账再判错**：copyOne 在「对象已 CopyObject 出去、但 asset 行没插成」
			// 时会带着 dstKey 和 error 一起返回。先 return err 的话这个 dstKey 就丢了，
			// 回滚看不到它 —— 对象永远留在存储里，没有任何行引用它，静默泄漏。
			if out != nil {
				results[i] = out
			}
			return err
		})
	}
	runErr := g.Wait()

	// 回滚/汇总都基于同一份 results。真正 CopyObject 写出去的才有 dstKey；
	// hash 去重命中的那些复用的是**已存在**的对象，绝不能把它们喂进 GC 队列。
	if runErr != nil {
		for _, r := range results {
			if r != nil && r.dstKey != "" {
				_ = d.gc.EnqueueObjectKey(r.dstKey)
			}
		}
		return nil, runErr
	}

	keyToNewURL := make(map[string]string, len(keys))
	newAssetIDs := make([]int64, 0, len(keys))
	var bytesCopied int64
	for _, r := range results {
		if r == nil {
			continue
		}
		keyToNewURL[r.srcKey] = r.newURL
		newAssetIDs = append(newAssetIDs, r.assetID)
		bytesCopied += r.bytes
	}

	if bytesCopied > 0 {
		_ = d.users.AddStorage(targetUserID, bytesCopied)
	}

	// 阶段 B：重写 snapshot 的 URL。WalkStrings 是纯变换。
	rewritten := assets.TransformJSONRaw(snapshot, func(s string) string {
		key, ok := d.storage.ExtractKey(s)
		if !ok || key == "" {
			return s
		}
		if newURL, found := keyToNewURL[key]; found {
			return newURL
		}
		return s
	})

	return &DuplicateResult{
		Snapshot:    rewritten,
		NewAssetIDs: newAssetIDs,
		BytesCopied: bytesCopied,
	}, nil
}

// copyOutcome 是单个 key 的复制结果。dstKey 只有**真的 CopyObject 写出了新对象**时
// 才非空——回滚时据此判断哪些该进 GC 队列；hash 去重复用的既有对象绝不能被回收，
// 那是别人还在用的字节。
type copyOutcome struct {
	srcKey  string
	newURL  string
	assetID int64
	bytes   int64
	dstKey  string
}

// copyOne 处理单个源 key：解析源 asset → 尝试 hash 去重 → CopyObject → 插 asset 行。
// 返回 (nil, nil) 表示「静默跳过」（snapshot 引用了我们的 bucket，但没有对应的 asset
// 行，多半是上游生成、尚未镜像过来的内容）。
func (d *AssetDuplicator) copyOne(
	ctx context.Context,
	targetUserID, targetProjectID int64,
	sourceKind, srcKey string,
) (*copyOutcome, error) {
	// 解析源 asset 记录，拿到它的 content_hash 和元数据。
	// 多条共享同一 object_key 的记录没关系——我们取第一条。
	records, err := d.assets.ListByObjectKey(srcKey)
	if err != nil {
		return nil, fmt.Errorf("lookup asset %q: %w", srcKey, err)
	}
	if len(records) == 0 {
		return nil, nil
	}
	srcRec := records[0]

	// 按 (目标用户, 目标项目) 维度做 hash 去重：如果目标在同一项目里已经拥有相同的
	// 字节，就复用那条 asset 记录——不写 TOS，不计配额。
	//
	// 并发化削弱了这个去重：串行时后一个 key 能查到前一个刚插入的行，并发时两者可能
	// 同时查空、各复制一份。**这只多占一点存储，不影响正确性**，且触发条件苛刻——
	// 目标项目此刻是新建的空项目/新冻结的 workflow，能撞上的只有「本次复制的 keys 之间
	// 内容相同」这一种，而上传路径本身已按 hash 去重过，同一项目里两个不同 key 装着
	// 相同字节本就罕见。为它把 CopyObject 重新串行化，代价远大于收益。
	if srcRec.ContentHash != "" {
		if existing, err := d.assets.FindByUserProjectHash(targetUserID, targetProjectID, srcRec.ContentHash); err == nil && existing != nil {
			return &copyOutcome{srcKey: srcKey, newURL: existing.URL, assetID: existing.ID}, nil
		} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("hash lookup: %w", err)
		}
	}

	// 在目标用户名下构造一个新 key 并拷贝。
	dstKey := d.newKeyFor(targetUserID, srcKey)
	stored, err := d.storage.CopyObject(ctx, srcKey, dstKey, srcRec.MimeType)
	if err != nil {
		return nil, fmt.Errorf("copy %q: %w", srcKey, err)
	}

	// 当 CopyObject 没报告大小（HEAD 失败）时，用源记录已知的大小，
	// 让配额记账保持准确。
	size := stored.Size
	if size == 0 {
		size = srcRec.SizeBytes
	}

	newID, err := d.assets.Insert(assets.AssetRecord{
		UserID:      targetUserID,
		ProjectID:   targetProjectID,
		ObjectKey:   dstKey,
		SourceKind:  sourceKind,
		URL:         stored.URL,
		SizeBytes:   size,
		MimeType:    srcRec.MimeType,
		ContentHash: srcRec.ContentHash,
	})
	if err != nil {
		// 对象已经写出去了但行没插成——把 dstKey 带回去，让调用方的回滚把它回收。
		return &copyOutcome{srcKey: srcKey, dstKey: dstKey}, fmt.Errorf("insert asset row: %w", err)
	}
	return &copyOutcome{
		srcKey: srcKey, newURL: stored.URL, assetID: newID, bytes: size, dstKey: dstKey,
	}, nil
}

// EstimateBytes 预先遍历 snapshot，算出最坏情况下的存储开销（字节数）——
// 把每个指向我们 bucket 的 URL 对应的源 asset 大小累加起来。供克隆路径在
// 任何 CopyObject 运行之前就对超配额做 fail-fast。这里不针对目标已有的 asset
// 做 hash 去重（预览时我们还不知道 targetUserID）；Duplicate 实际跑完后的
// 真实计费可能更低。
func (d *AssetDuplicator) EstimateBytes(snapshot json.RawMessage) (int64, error) {
	keys := d.collectKeys(snapshot)
	if len(keys) == 0 {
		return 0, nil
	}
	var total int64
	for _, k := range keys {
		records, err := d.assets.ListByObjectKey(k)
		if err != nil {
			return 0, err
		}
		if len(records) == 0 {
			continue
		}
		total += records[0].SizeBytes
	}
	return total, nil
}

// collectKeys 遍历 snapshot 一次，返回它引用的每个去重后的 object key
// （在我们 bucket 内）。重复项会被合并，这样即便多个节点共享同一个 asset，
// 每个唯一 key 也只 CopyObject 一次。
func (d *AssetDuplicator) collectKeys(raw json.RawMessage) []string {
	seen := map[string]struct{}{}
	var ordered []string
	assets.TransformJSONRaw(raw, func(s string) string {
		key, ok := d.storage.ExtractKey(s)
		if !ok || key == "" {
			return s
		}
		if _, dup := seen[key]; !dup {
			seen[key] = struct{}{}
			ordered = append(ordered, key)
		}
		return s
	})
	return ordered
}

// newKeyFor 为一次 CopyObject 生成一个新的目标 key。我们沿用源 key 的扩展名
// （这样图片缩略图等由 MIME 驱动的东西仍能正常工作），其余部分则在目标用户的
// 命名空间下构造一条全新路径。不用 storage.NewKey，是因为那个是按文件名驱动的，
// 而我们想要一个基于 hash 的唯一名字。
func (d *AssetDuplicator) newKeyFor(targetUserID int64, srcKey string) string {
	rand6 := mustRandHex(6)
	ext := strings.ToLower(path.Ext(srcKey))
	if ext != "" && len(ext) > 6 {
		// 防御性处理：忽略超过 6 个字符的奇怪「扩展名」。
		ext = ""
	}
	now := time.Now().UTC()
	return fmt.Sprintf("u%d/%04d/%02d/wf-%s%s",
		targetUserID, now.Year(), int(now.Month()), rand6, ext)
}

func mustRandHex(n int) string {
	buf := make([]byte, n)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}
