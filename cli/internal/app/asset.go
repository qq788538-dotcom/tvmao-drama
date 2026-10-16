package app

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/james/huabu_studio/apps/cli/internal/client"
)

func newAssetCommand(st *runtimeState) *cobra.Command {
	c := &cobra.Command{Use: "asset", Short: "上传素材与下载生成结果"}
	c.PersistentFlags().Int64Var(&st.flagProject, "project", 0, "项目 ID（省略则用当前目录绑定）")
	c.AddCommand(newAssetUploadCommand(st), newAssetGetCommand(st), newAssetDownloadCommand(st))
	return c
}

func newAssetUploadCommand(st *runtimeState) *cobra.Command {
	var createNode bool
	var nodeType string
	c := &cobra.Command{
		Use:   "upload <文件路径>",
		Short: "上传本地文件，可同时创建输入节点",
		Long: strings.TrimSpace(`
上传本地文件到当前项目。服务端按内容哈希去重：同一项目里重传同一个文件不会
重复占用存储配额（响应里 deduped=true）。

--create-node 是**客户端编排的两步**：先上传，再用返回的 URL 创建输入节点。
对象存储写入与画布快照写入不在同一事务，所以第二步失败时素材会保留，
重试只补建节点、不重复上传。
`),
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pid, cl, err := st.projectClient()
			if err != nil {
				return err
			}
			path := args[0]
			if _, statErr := os.Stat(path); statErr != nil {
				return usagef("读不到文件 %s：%v", path, statErr)
			}
			st.out.Progress("正在上传 %s …", filepath.Base(path))

			asset, err := cl.UploadAsset(cmd.Context(), pid, path)
			if err != nil {
				return err
			}
			if asset.Deduped {
				st.out.Progress("命中已有素材（未重复占用存储）")
			}
			if !createNode {
				return st.out.Emit(asset)
			}

			ty := nodeType
			if ty == "" {
				ty = guessInputNodeType(path, asset.MimeType)
			}
			if ty == "" {
				// 猜不出来时报错而不是默认成图片：把音频建成图片节点，
				// 下游会拿到一个永远不对的输入。
				return usagef("无法判断素材类型，请用 --node-type 指定 image-input / audio-input / video-input")
			}
			// 幂等键**由素材 ID 派生**，不是每次随机：同一个文件重传会命中服务端的
			// 内容去重、拿到同一个 assetId，于是这里也拿到同一个 key ——
			// 「节点建到一半失败后重跑」只会补建，不会多出第二个节点。
			nodeKey := fmt.Sprintf("cli-asset-node-%d-%s", asset.ID, ty)
			if st.flagIdemKey != "" {
				nodeKey = st.flagIdemKey
			}
			node, err := cl.CreateNode(cmd.Context(), pid, client.CreateNodeInput{
				Type: ty, Content: asset.URL,
			}, nodeKey, 0)
			if err != nil {
				// 部分成功：素材已经在了，明确告诉用户下一步怎么补。
				st.out.Warn("素材已上传（id=%d），但创建节点失败；可用 miaoju node create --type %s --content <url> 补建",
					asset.ID, ty)
				return err
			}
			return st.out.Emit(map[string]any{"asset": asset, "node": node})
		},
	}
	c.Flags().BoolVar(&createNode, "create-node", false, "上传后创建对应的输入节点")
	c.Flags().StringVar(&nodeType, "node-type", "", "显式指定输入节点类型：image-input / audio-input / video-input")
	return c
}

// guessInputNodeType 按 MIME 与扩展名判断输入节点类型；判断不了返回空串。
func guessInputNodeType(path, mimeType string) string {
	switch {
	case strings.HasPrefix(mimeType, "image/"):
		return "image-input"
	case strings.HasPrefix(mimeType, "audio/"):
		return "audio-input"
	case strings.HasPrefix(mimeType, "video/"):
		return "video-input"
	}
	// 扩展名清单与 canvasinputs 的 audioExtRe/videoExtRe 保持同集合。
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".jpg", ".jpeg", ".webp", ".gif", ".bmp":
		return "image-input"
	case ".mp3", ".wav", ".aac", ".ogg", ".flac", ".m4a":
		return "audio-input"
	case ".mp4", ".webm", ".mov":
		return "video-input"
	}
	return ""
}

func newAssetGetCommand(st *runtimeState) *cobra.Command {
	return &cobra.Command{
		Use: "get <素材ID>", Short: "查看素材元数据并取得新的签名 URL", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil || id <= 0 {
				return usagef("素材 ID 必须是正整数，收到 %q", args[0])
			}
			a, err := st.client().GetAsset(cmd.Context(), id)
			if err != nil {
				return err
			}
			return st.out.Emit(a)
		},
	}
}

func newAssetDownloadCommand(st *runtimeState) *cobra.Command {
	var assetID int64
	var nodeRef, out string
	c := &cobra.Command{
		Use:   "download",
		Short: "下载素材或某个节点的当前产物",
		Long: strings.TrimSpace(`
按素材 ID 或节点引用下载。节点方式取的是它当前显示的那一版产物
（node.content），与网页上看到的一致。
`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if (assetID == 0) == (nodeRef == "") {
				return usagef("必须且只能提供 --asset 或 --node 其中之一")
			}
			if out == "" {
				return usagef("必须提供 --out <输出路径>")
			}

			cl := st.client()
			var url, suggested string
			if assetID > 0 {
				a, err := cl.GetAsset(cmd.Context(), assetID)
				if err != nil {
					return err
				}
				url, suggested = a.URL, "asset-"+strconv.FormatInt(a.ID, 10)
			} else {
				pid, err := st.project()
				if err != nil {
					return err
				}
				id, err := cl.ResolveNode(cmd.Context(), pid, nodeRef)
				if err != nil {
					return err
				}
				n, err := cl.GetNode(cmd.Context(), pid, id)
				if err != nil {
					return err
				}
				if n.Content == "" {
					return usagef("节点 %s 还没有产物（status=%s）", id, n.Status)
				}
				url, suggested = n.Content, id
			}

			target := out
			// --out 指向目录时自动补文件名，避免用户被迫先算好名字。
			if fi, err := os.Stat(out); err == nil && fi.IsDir() {
				target = filepath.Join(out, suggested)
			}
			st.out.Progress("正在下载到 %s …", target)
			n, err := downloadTo(target, func(w io.Writer) (int64, error) {
				return cl.DownloadURL(cmd.Context(), url, w)
			})
			if err != nil {
				return err
			}
			return st.out.Emit(map[string]any{"path": target, "bytes": n})
		},
	}
	c.Flags().Int64Var(&assetID, "asset", 0, "素材 ID")
	c.Flags().StringVar(&nodeRef, "node", "", "节点引用（取它当前显示的产物）")
	c.Flags().StringVar(&out, "out", "", "输出文件或目录（必填）")
	return c
}

// downloadTo 先下载到 target 同目录的临时文件，成功且非空才改名覆盖 target。
// 直接 os.Create(target) 会先截断用户已有的同名文件，下载失败再删掉它就等于把原文件弄丢了；
// 而且 Windows 上文件还开着时 os.Remove 会失败，半截文件删不掉。
func downloadTo(target string, fetch func(io.Writer) (int64, error)) (int64, error) {
	if fi, err := os.Stat(target); err == nil && !fi.Mode().IsRegular() {
		// 设备 / 命名管道（如 /dev/null）没法改名替换，直接写，与原来一样。
		f, err := os.OpenFile(target, os.O_WRONLY, 0)
		if err != nil {
			return 0, err
		}
		n, err := fetch(f)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		return n, err
	}
	var (
		f   *os.File
		tmp string
		err error
	)
	for i := 0; i < 10; i++ {
		// 随机后缀借用幂等键的生成器（crypto/rand）；O_EXCL 保证不会碰到已有文件。
		tmp = filepath.Join(filepath.Dir(target), fmt.Sprintf(".%s.%s.part", filepath.Base(target), newIdempotencyKey()))
		// 0o666 交给 umask，与 os.Create 的权限一致。
		f, err = os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o666)
		if !errors.Is(err, fs.ErrExist) {
			break
		}
	}
	if err != nil {
		return 0, err
	}
	n, err := fetch(f)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && n == 0 {
		err = usagef("下载到 0 字节，未写入输出文件")
	}
	if err == nil {
		err = os.Rename(tmp, target)
	}
	if err != nil {
		// 失败时删掉临时文件：留着半个文件会让下次判断「已下载」时误判。
		_ = os.Remove(tmp)
		return 0, err
	}
	return n, nil
}
