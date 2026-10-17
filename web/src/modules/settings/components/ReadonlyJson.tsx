import { Tag, theme } from 'antd';

/** 技术字段只读展示：格式化 JSON，不提供任何编辑入口。 */
export function ReadonlyJson({ value, maxHeight = 240 }: { value: unknown; maxHeight?: number }) {
  const { token } = theme.useToken();
  const text = value === null || value === undefined ? '（未配置）' : JSON.stringify(value, null, 2);
  return (
    <div style={{ position: 'relative' }}>
      <Tag style={{ position: 'absolute', right: 8, top: 8 }}>只读</Tag>
      <pre
        style={{
          margin: 0,
          padding: 12,
          maxHeight,
          overflow: 'auto',
          background: token.colorBgElevated,
          border: `1px solid ${token.colorBorderSecondary}`,
          borderRadius: 8,
          fontFamily: '"JetBrains Mono", ui-monospace, monospace',
          fontSize: 12,
          whiteSpace: 'pre-wrap',
          wordBreak: 'break-all',
        }}
      >
        {text}
      </pre>
    </div>
  );
}
