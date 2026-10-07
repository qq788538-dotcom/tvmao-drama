import { Alert, App as AntApp, Button, Card, Descriptions, Modal, Space, Typography } from 'antd';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { open } from '@tauri-apps/plugin-dialog';
import {
  appVersion,
  applyDataDirChange,
  checkDataDir,
  dataDirStatus,
  dismissDataDirError,
  errorText,
  switchToExistingDataDir,
  getGeneral,
  type DataDirCheck,
} from '../api';

type OpenDialog = (o: { directory: true; multiple: false; defaultPath?: string; title?: string }) => Promise<string | string[] | null>;

export async function pickDataDir(
  current: string,
  openDialog: OpenDialog = open as unknown as OpenDialog,
  check: (p: string) => Promise<DataDirCheck> = checkDataDir,
): Promise<DataDirCheck | null> {
  const picked = await openDialog({ directory: true, multiple: false, defaultPath: current, title: '选择新的数据目录' });
  if (!picked || Array.isArray(picked)) return null;
  return check(picked);
}

/** 按所选目录的状态决定确认框（I1）：空目录 → 迁移；已有喵剧数据 → 直接使用（不迁移）。 */
export function dataDirChangeDialog(t: DataDirCheck): {
  mode: 'migrate' | 'use-existing';
  title: string;
  description: string;
  okText: string;
} {
  if (t.state === 'existing') {
    return {
      mode: 'use-existing',
      title: '使用已有的数据目录',
      description:
        '这个文件夹里已经有喵剧AI 的数据（例如之前用过的数据目录）。将直接切换到它，不会复制或删除任何数据；当前目录里的数据保持原样，以后也可以再切回来。本地服务会重启，期间请稍候。',
      okText: '直接使用该目录（不迁移）',
    };
  }
  return {
    mode: 'migrate',
    title: '更改数据目录',
    description:
      '将把全部数据（项目、素材、生成结果、设置）复制到这里。应用会立即重启并开始复制，数据多时需要几分钟，期间请不要关闭应用。复制并校验成功后，原目录中的数据会被删除。',
    okText: '立即重启并迁移',
  };
}

export function GeneralPage() {
  const { message } = AntApp.useApp();
  const qc = useQueryClient();
  const general = useQuery({ queryKey: ['settings-general'], queryFn: getGeneral });
  const dirs = useQuery({ queryKey: ['settings-datadir'], queryFn: dataDirStatus });
  const version = useQuery({ queryKey: ['settings-app-version'], queryFn: appVersion });

  const dismiss = useMutation({
    mutationFn: dismissDataDirError,
    onSuccess: () => qc.invalidateQueries({ queryKey: ['settings-datadir'] }),
  });

  const changeDir = async () => {
    if (!dirs.data) return;
    let target: DataDirCheck | null;
    try {
      target = await pickDataDir(dirs.data.current);
    } catch (e) {
      message.error(errorText(e));
      return;
    }
    if (!target) return;
    const { path } = target;
    const d = dataDirChangeDialog(target);
    Modal.confirm({
      title: d.title,
      content: (
        <div>
          <Typography.Text code>{path}</Typography.Text>
          <p style={{ marginTop: 12 }}>{d.description}</p>
        </div>
      ),
      okText: d.okText,
      cancelText: '取消',
      onOk: async () => {
        try {
          if (d.mode === 'use-existing') await switchToExistingDataDir(path);
          else await applyDataDirChange(path);
        } catch (e) {
          message.error(errorText(e));
        }
      },
    });
  };

  const g = general.data;
  return (
    <Space direction="vertical" size={16} style={{ width: '100%', maxWidth: 760 }}>
      <Card title="数据目录" extra={<Button onClick={changeDir}>更改…</Button>}>
        {dirs.data?.lastError && (
          <Alert type="error" showIcon message={dirs.data.lastError} closable onClose={() => dismiss.mutate()} style={{ marginBottom: 12 }} />
        )}
        <Descriptions column={1} size="small">
          <Descriptions.Item label="当前位置">
            <Typography.Text copyable>{dirs.data?.current ?? g?.dataDir ?? '—'}</Typography.Text>
          </Descriptions.Item>
          <Descriptions.Item label="默认位置">{dirs.data?.defaultDir ?? '—'}</Descriptions.Item>
        </Descriptions>
        <Typography.Paragraph type="secondary" style={{ marginTop: 8, marginBottom: 0 }}>
          备份数据：退出应用后复制整个数据目录即可。
        </Typography.Paragraph>
      </Card>

      <Card title="关于">
        <Descriptions column={1} size="small">
          <Descriptions.Item label="应用版本">{version.data ?? '—'}</Descriptions.Item>
          <Descriptions.Item label="本地服务版本">{g?.version ?? '—'}</Descriptions.Item>
        </Descriptions>
      </Card>
    </Space>
  );
}
