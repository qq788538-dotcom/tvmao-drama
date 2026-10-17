// 画布脚本生成器的风格选择——彩色 chip + name/version 两级 Cascader。
// 复制适配自 project-management/WorkStyleSelect.tsx（UI 与项目
// 管理步骤①的作品风格一致），但按「平级独立原则」不 import 那边的代码：数据源换成
// 画布自己的 /canvas-script/styles（由 ScriptGenerator 传入，避免二次请求）。
// AntD Cascader 弹层 portal 到 body——顺带绕开画布 transform 容器里 fixed 定位
// containing block 错乱的问题（自绘 PopoverShell 在缩放画布内会飘）。
import { useEffect } from 'react';
import { Cascader } from 'antd';
import { Box, Film, Image as ImageIcon, PaintBucket, Video } from 'lucide-react';

// 风格名是后台配置的，配色+图标按目录顺序(序号)分配，循环使用调色板，不写死具体名称。
const STYLE_PALETTE: { from: string; to: string; Icon: typeof Video }[] = [
  { from: '#F97316', to: '#FB923C', Icon: Video },
  { from: '#3b82f6', to: '#6366f1', Icon: Film },
  { from: '#3b82f6', to: '#22d3ee', Icon: PaintBucket },
  { from: '#ec4899', to: '#f43f5e', Icon: ImageIcon },
  { from: '#10b981', to: '#14b8a6', Icon: Box },
];

function StyleChip({ name, index }: { name: string; index: number }) {
  const t = STYLE_PALETTE[((index % STYLE_PALETTE.length) + STYLE_PALETTE.length) % STYLE_PALETTE.length];
  const Icon = t.Icon;
  return (
    <span
      className="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-md text-white text-xs font-bold shadow-sm whitespace-nowrap"
      style={{ background: `linear-gradient(to right, ${t.from}, ${t.to})` }}
    >
      <Icon size={14} />
      {name}
    </span>
  );
}

export interface ScriptStyleOption {
  name: string;
  version?: string;
}

interface Props {
  /** "name" 或 "name version"（与后端 StyleEntry.Key 同构） */
  value?: string;
  onChange?: (style: string) => void;
  disabled?: boolean;
  styles: ScriptStyleOption[];
  loading?: boolean;
}

export function ScriptStyleSelect({ value = '', onChange = () => {}, disabled, styles, loading }: Props) {
  // 自动默认选第一个（仅 value 空时）——格式对齐：name 或 `name version`。
  useEffect(() => {
    if (!value && styles.length > 0) {
      const s = styles[0];
      onChange(s.version ? `${s.name} ${s.version}` : s.name);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [styles, value]);

  // 按 name 分组：左级=风格名(彩色 chip)，右级=版本。
  const groups = new Map<string, string[]>();
  styles.forEach((s) => {
    if (!groups.has(s.name)) groups.set(s.name, []);
    if (s.version && !groups.get(s.name)!.includes(s.version)) groups.get(s.name)!.push(s.version);
  });
  const names = [...groups.keys()];
  const options = [...groups.entries()].map(([name, versions], i) => ({
    value: name,
    label: <StyleChip name={name} index={i} />,
    children: (versions.length ? versions : ['']).map((v) => ({ value: v, label: v || '默认' })),
  }));
  // 把已存的风格串("name version" 或 "name") 反解成 cascader 的 [name, version]
  const cascaderValue: string[] | undefined = (() => {
    if (!value) return undefined;
    for (const name of groups.keys()) {
      if (value === name) return [name, ''];
      if (value.startsWith(`${name} `)) return [name, value.slice(name.length + 1)];
    }
    return undefined;
  })();

  return (
    <Cascader
      value={cascaderValue}
      options={options}
      disabled={disabled}
      loading={loading}
      onChange={(val) => {
        const [name, version] = (val ?? []) as string[];
        onChange(version ? `${name} ${version}` : name || '');
      }}
      placeholder="选择风格"
      allowClear={false}
      placement="topLeft"
      displayRender={(_labels, selected) => {
        const name = selected?.[0]?.value as string;
        const version = selected?.[1]?.value as string;
        if (!name) return '选择风格';
        return (
          <span className="flex items-center gap-2">
            <StyleChip name={name} index={names.indexOf(name)} />
            {version && <span className="text-[#a3a3a3]">/ {version}</span>}
          </span>
        );
      }}
    />
  );
}
