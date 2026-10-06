import type { RejectedDrop } from './fileDrop';

/** 被拒文件列表。全列出来对「误拖整个下载文件夹」的场景没意义,
 *  列几个够用户认出自己拖错了什么。 */
const MAX_LISTED = 5;

/** 画布拒绝非图片/音频/视频文件时的弹框正文。拖入和右键「上传」共用一套措辞。 */
export function renderRejected(rejected: RejectedDrop[]) {
  const shown = rejected.slice(0, MAX_LISTED);
  return (
    <div>
      <div style={{ marginBottom: 8 }}>画布只接受图片、音频、视频文件。</div>
      <ul style={{ paddingLeft: 18, margin: 0 }}>
        {shown.map((r) => (
          <li key={r.name}>
            {r.name}
            <span style={{ opacity: 0.6 }}>（{r.reason}）</span>
          </li>
        ))}
      </ul>
      {rejected.length > shown.length && (
        <div style={{ marginTop: 6, opacity: 0.6 }}>等 {rejected.length} 个文件</div>
      )}
    </div>
  );
}
