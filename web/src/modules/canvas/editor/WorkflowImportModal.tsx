// apps/web/src/canvas/WorkflowImportModal.tsx
//
// 仅限超级管理员。粘贴一段 Skill JSON（和 WorkflowExportModal 产出的 payload
// 相同）→ 解码成 canvas nodes+connections → 通过 store 的 hydrateSnapshot
// 灌进当前 canvas。

import { useState } from 'react';
import { Alert, Button, Input, Modal, message } from 'antd';
import { useCanvasStore } from './store';
import { decodeFromSkill, SkillExport } from './workflowSerializer';

interface Props {
  open: boolean;
  onClose: () => void;
}

export function WorkflowImportModal({ open, onClose }: Props) {
  const [raw, setRaw] = useState('');
  const [warnings, setWarnings] = useState<string[]>([]);
  const view = useCanvasStore((s) => s.view);
  const load = useCanvasStore((s) => s.load);

  const apply = () => {
    let parsed: SkillExport;
    try {
      parsed = JSON.parse(raw);
    } catch (err) {
      message.error(`JSON 解析失败: ${(err as Error).message}`);
      return;
    }
    if (!Array.isArray(parsed.workflow_steps)) {
      message.error('缺少 workflow_steps 数组，请检查 JSON 结构');
      return;
    }
    const { nodes, connections, warnings: ws } = decodeFromSkill(parsed);
    if (nodes.length === 0) {
      message.error('解码后没有任何可识别的节点');
      setWarnings(ws);
      return;
    }
    // 替换整个 canvas——预期管理员是导入到一个空白/草稿画布，
    // 而不是合并进已有的工作。
    load({ nodes, connections, view });
    setWarnings(ws);
    message.success(`已应用：${nodes.length} 节点 / ${connections.length} 连线`);
    if (ws.length === 0) onClose();
  };

  return (
    <Modal
      open={open}
      onCancel={onClose}
      title="从 Skill JSON 导入到画布"
      width={760}
      footer={[
        <Button key="close" onClick={onClose}>关闭</Button>,
        <Button key="apply" type="primary" onClick={apply}>
          应用到画布
        </Button>,
      ]}
    >
      <Alert
        type="warning"
        style={{ marginBottom: 12 }}
        message="会覆盖当前画布"
        description="导入会替换当前画布的所有节点和连线，请先确保已保存或在空白画布上操作。"
      />
      <Input.TextArea
        value={raw}
        onChange={(e) => setRaw(e.target.value)}
        rows={16}
        placeholder='粘贴完整 Skill JSON，例如：{"name":"...","skill_type":"workflow","workflow_steps":[...]}'
        style={{ fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace', fontSize: 11 }}
      />
      {warnings.length > 0 && (
        <Alert
          type="warning"
          style={{ marginTop: 12 }}
          message="解码警告"
          description={
            <ul style={{ margin: 0, paddingLeft: 20 }}>
              {warnings.map((w, i) => (
                <li key={i}>{w}</li>
              ))}
            </ul>
          }
        />
      )}
    </Modal>
  );
}
