// apps/web/src/canvas/WorkflowExportModal.tsx
//
// 仅限超级管理员。把当前 canvas 编译成 Skill JSON payload，并显示在一个可复制
// 的 textarea 里——管理员随后手动把这段 JSON 粘进 agent_skills 表（或后台 UI
// 的 skill 编辑器）。它故意不调用任何后端 API：这样这个功能对非管理员不可见。

import { useMemo } from 'react';
import { Alert, Button, Modal, Tag, Input, message } from 'antd';
import { useCanvasStore } from './store';
import { compileToSkill } from './workflowSerializer';

interface Props {
  open: boolean;
  onClose: () => void;
}

export function WorkflowExportModal({ open, onClose }: Props) {
  const nodes = useCanvasStore((s) => s.nodes);
  const connections = useCanvasStore((s) => s.connections);
  const view = useCanvasStore((s) => s.view);

  // Skill 元信息（name / description / category）留空，由管理员复制后到
  // 后台手填——画布编辑器只负责把节点拓扑编译成 workflow_steps 这部分。
  const result = useMemo(() => {
    return compileToSkill({ nodes, connections, view }, { name: '', description: '' });
  }, [nodes, connections, view]);

  const json = useMemo(() => JSON.stringify(result.skill, null, 2), [result.skill]);

  const copyToClipboard = async () => {
    try {
      if (navigator.clipboard?.writeText) {
        await navigator.clipboard.writeText(json);
      } else {
        const ta = document.createElement('textarea');
        ta.value = json;
        ta.style.position = 'fixed';
        ta.style.left = '-9999px';
        document.body.appendChild(ta);
        ta.select();
        document.execCommand('copy');
        document.body.removeChild(ta);
      }
      message.success('已复制 JSON');
    } catch {
      message.error('复制失败，请手动选取');
    }
  };

  return (
    <Modal
      open={open}
      onCancel={onClose}
      title="导出画布为 Skill JSON"
      width={760}
      footer={[
        <Button key="close" onClick={onClose}>关闭</Button>,
        <Button key="copy" type="primary" onClick={copyToClipboard}>
          复制 JSON
        </Button>,
      ]}
    >
      {result.warnings.length > 0 && (
        <Alert
          type="warning"
          style={{ marginBottom: 12 }}
          message="编译警告"
          description={
            <ul style={{ margin: 0, paddingLeft: 20 }}>
              {result.warnings.map((w, i) => (
                <li key={i}>{w}</li>
              ))}
            </ul>
          }
        />
      )}

      <div style={{ marginBottom: 8, fontSize: 12 }}>
        <Tag>{result.skill.workflow_steps.length} 个 step</Tag>
        <Tag>{Object.keys(result.skill.param_defaults ?? {}).length} 个 input 变量</Tag>
      </div>

      <Input.TextArea
        value={json}
        rows={16}
        readOnly
        style={{ fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace', fontSize: 11 }}
        onFocus={(e) => e.currentTarget.select()}
      />
    </Modal>
  );
}
