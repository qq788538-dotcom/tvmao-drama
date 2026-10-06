import { Modal, App as AntApp } from 'antd';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { deleteLibraryFolder } from '@/shared/api';

interface Props {
  open: boolean;
  folderId: number | null;
  itemCount: number;
  childCount: number;
  onCancel: () => void;
  onDeleted: () => void;
}

export function DeleteFolderConfirm({
  open,
  folderId,
  itemCount,
  childCount,
  onCancel,
  onDeleted,
}: Props) {
  const { message } = AntApp.useApp();
  const queryClient = useQueryClient();
  const mut = useMutation({
    mutationFn: () => deleteLibraryFolder(folderId!, true),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['library-folders'] });
      queryClient.invalidateQueries({ queryKey: ['library-items'] });
      message.success('已删除');
      onDeleted();
    },
    onError: (e: any) => {
      message.error(e?.response?.data?.error || e.message || '删除失败');
    },
  });

  return (
    <Modal
      title="确认删除目录"
      open={open}
      onCancel={onCancel}
      onOk={() => mut.mutate()}
      okText="确认删除"
      okButtonProps={{ danger: true, loading: mut.isPending }}
      cancelText="取消"
    >
      <p>
        此目录下还有 <b>{itemCount}</b> 个资源、<b>{childCount}</b> 个子目录。
      </p>
      <p>删除后这些资源将一并删除（如未被任何项目引用，TOS 对象将在 7 天后回收）。</p>
      <p>确认继续吗？</p>
    </Modal>
  );
}
