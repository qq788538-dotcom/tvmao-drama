import { useMemo, type CSSProperties } from 'react';
import { Select } from 'antd';
import { useQuery } from '@tanstack/react-query';
import { listModels, type ModelView } from '../api';

export interface ModelOption {
  value: string;
  label: string;
  disabled?: boolean;
}

/** 可选 = 已启用且类型匹配（不看 hidden：后处理、脚本的候选模型在模型中心通常是隐藏的）。 */
export function modelOptions(models: ModelView[], type: string, value: string | string[] | undefined): ModelOption[] {
  const opts: ModelOption[] = models
    .filter((m) => m.enabled && m.type === type)
    .map((m) => ({ value: m.modelId, label: `${m.name}（${m.modelId}）` }));
  const current = value === undefined ? [] : Array.isArray(value) ? value : [value];
  for (const v of current) {
    if (v && !opts.some((o) => o.value === v)) opts.push({ value: v, label: `${v}（不可用：不存在、已停用或类型不符）`, disabled: true });
  }
  return opts;
}

export function ModelSelect({
  type,
  multiple,
  value,
  onChange,
  disabled,
  style,
}: {
  type: 'image' | 'video' | 'text' | 'audio';
  multiple?: boolean;
  value?: string | string[];
  onChange?: (v: any) => void;
  disabled?: boolean;
  style?: CSSProperties;
}) {
  const { data } = useQuery({ queryKey: ['settings-models'], queryFn: listModels });
  const options = useMemo(() => modelOptions(data ?? [], type, value), [data, type, value]);
  return (
    <Select
      mode={multiple ? 'multiple' : undefined}
      showSearch
      optionFilterProp="label"
      placeholder="选择模型"
      value={value === '' ? undefined : value}
      onChange={(v) => onChange?.(v ?? (multiple ? [] : ''))}
      options={options}
      disabled={disabled}
      style={{ minWidth: 280, ...style }}
    />
  );
}
