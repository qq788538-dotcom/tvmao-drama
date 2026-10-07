import { App as AntApp } from 'antd';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { invalidateAfterSettingsChange } from './invalidate';
import { errorText, getConfig, putConfig, restoreConfig, type ConfigKey, type ConfigView } from './api';

/** 画布配置页共用：读取、整份保存（草稿含全部原字段）、恢复默认。 */
export function useConfigEditor<T>(key: ConfigKey) {
  const qc = useQueryClient();
  const { message } = AntApp.useApp();
  const queryKey = ['settings-config', key];
  const query = useQuery({ queryKey, queryFn: () => getConfig<T>(key) });
  const onError = (e: unknown) => message.error(errorText(e));
  const save = useMutation<ConfigView<T>, Error, T>({
    mutationFn: (value) => putConfig<T>(key, value),
    onSuccess: (v) => {
      qc.setQueryData(queryKey, v);
      invalidateAfterSettingsChange(qc); // 画布侧配置缓存 + 模型页的“被引用”标记
      message.success('已保存');
    },
    onError,
  });
  const restore = useMutation<ConfigView<T>, Error, void>({
    mutationFn: () => restoreConfig<T>(key),
    onSuccess: (v) => {
      qc.setQueryData(queryKey, v);
      invalidateAfterSettingsChange(qc);
      message.success('已恢复默认');
    },
    onError,
  });
  return { data: query.data, isLoading: query.isLoading, error: query.error, save, restore };
}
