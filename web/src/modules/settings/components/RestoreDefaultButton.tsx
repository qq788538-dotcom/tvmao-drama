import { Button, Popconfirm } from 'antd';

export function RestoreDefaultButton({
  onConfirm,
  loading,
  disabled,
  label = '恢复默认',
}: {
  onConfirm: () => void;
  loading?: boolean;
  disabled?: boolean;
  label?: string;
}) {
  return (
    <Popconfirm title="恢复为内置默认值？" description="你对这一项的修改会丢失。" okText="恢复" cancelText="取消" onConfirm={onConfirm} disabled={disabled}>
      <Button size="small" loading={loading} disabled={disabled}>
        {label}
      </Button>
    </Popconfirm>
  );
}
