import { useCallback } from 'react';
import { useMatch, useNavigate } from 'react-router-dom';
import { App as AntApp, Modal } from 'antd';
import { CanvasProjectsPage } from '@/modules/canvas/CanvasProjectsPage';
import { flushCanvasMutations } from '@/modules/canvas/editor/persistence/canvasMutations';
import { resolveStartupProject } from '@/desktop/resolveStartupProject';
import { clearLastProjectId } from '@/desktop/lastProject';
import { useProjectsModal } from './projectsModalStore';

/**
 * 「所有项目」弹框：复用项目列表页（新建、重命名、封面、副本、删除都是它原有的逻辑），
 * 只接管「进入某个项目」和「删除了项目」两件事。
 * - 点当前项目：只关弹框。
 * - 点别的项目：先 flush 当前画布还在 debounce 的修改，再跳转并关弹框。
 * - 删的是当前项目：沿用启动规则回落（最近修改 → 没有就新建「未命名项目」）。
 */
export function ProjectsModal() {
  const open = useProjectsModal((s) => s.open);
  const close = useProjectsModal((s) => s.closeProjects);
  const navigate = useNavigate();
  const match = useMatch('/canvas/:projectId');
  const currentId = match ? Number(match.params.projectId) : null;
  const { message } = AntApp.useApp();

  const onOpenProject = useCallback(
    (id: number) => {
      if (id === currentId) {
        close();
        return;
      }
      void flushCanvasMutations()
        .catch(() => undefined)
        .finally(() => {
          navigate(`/canvas/${id}`);
          close();
        });
    },
    [currentId, navigate, close],
  );

  const onProjectDeleted = useCallback(
    (id: number) => {
      if (id !== currentId) return;
      // 当前项目已经没了：忘掉「上一次的项目」，再按启动规则选下一个。
      clearLastProjectId();
      resolveStartupProject().then(
        (next) => {
          navigate(`/canvas/${next}`, { replace: true });
          close();
        },
        (err: unknown) => message.error(err instanceof Error ? err.message : String(err)),
      );
    },
    [currentId, navigate, close, message],
  );

  return (
    <Modal
      open={open}
      onCancel={close}
      footer={null}
      title="所有项目"
      width="min(1100px, 92vw)"
      centered
      maskClosable={false}
      keyboard={false}
      destroyOnClose
      styles={{ body: { padding: 0, height: '80vh', overflow: 'auto' } }}
    >
      <CanvasProjectsPage embedded onOpenProject={onOpenProject} onProjectDeleted={onProjectDeleted} />
    </Modal>
  );
}
