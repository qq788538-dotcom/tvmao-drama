// 画布节点能力清单（浏览器侧）。
//
// canvasManifest.json 是从 apps/api/internal/agent/canvasmanifest/manifest.json
// **原样复制**过来的副本，Go 侧的 sync_test.go 钉死两份字节级一致。改清单必须
// 两处一起改：
//
//   cp apps/api/internal/agent/canvasmanifest/manifest.json \
//      apps/web/src/modules/canvas/editor/agent/canvasManifest.json
//
// 用同一份 JSON 两处拷贝，而不是两侧各写一份结构体：结构体本身就是漂移的入口
//（一侧加了字段、另一侧的解析忽略它，测试照样绿），字节比对糊弄不过去。
//
// 浏览器侧只用它做两件事：按 access 剥字段（纵深防御，后端已校验过一遍），
// 以及查「改了这个字段要不要重置动态字段」。完整的值域校验在后端——它那里
// 能直接读 model_configs 的 uiSchema，前端再抄一份就成了第二个真源。
import manifestJson from './canvasManifest.json';

export type FieldAccess = 'open' | 'readonly' | 'machine';

export interface FieldSpec {
  access: FieldAccess;
  type: string;
  desc: string;
  wholeReplace?: boolean;
  idManaged?: boolean;
  fullValue?: boolean;
  resetsDynamicFields?: boolean;
}

interface NodeSpec {
  dynamicFieldsFrom?: string;
  fields: Record<string, FieldSpec>;
}

interface Manifest {
  version: number;
  commonFields: Record<string, FieldSpec>;
  nodeTypes: Record<string, NodeSpec>;
}

const manifest = manifestJson as Manifest;

/** 查一个节点类型的某个字段的契约。类型专属字段优先于通用字段。 */
export function fieldSpec(nodeType: string, field: string): FieldSpec | undefined {
  return manifest.nodeTypes[nodeType]?.fields[field] ?? manifest.commonFields[field];
}

/** 该类型是否有随所选模型变化的动态字段（比例/分辨率/时长）。 */
export function hasDynamicFields(nodeType: string): boolean {
  return Boolean(manifest.nodeTypes[nodeType]?.dynamicFieldsFrom);
}

/** 改这个字段要不要把动态字段重置为新模型的默认值。目前只有 modelId 是 true。 */
export function resetsDynamicFields(nodeType: string, field: string): boolean {
  return Boolean(fieldSpec(nodeType, field)?.resetsDynamicFields);
}

/**
 * 剥掉 agent 不该写的字段，返回被剥掉的名字。
 *
 * 后端已经按同一份清单校验过一遍（非 open 字段会被拒、附带可写清单），这里
 * 是纵深防御：两侧都不给这个口子，谁先漂移都兜得住。与后端的差别在于这里
 * **静默剥离**而不是报错——帧已经到了浏览器，没有回执通道能把错误递回 agent。
 *
 * 未知节点类型不剥：节点类型清单还在增长，当封闭枚举会让每加一种类型就要
 * 重发后端。此时退回只剥三个通用机器字段。
 */
export function stripNonWritable(
  nodeType: string,
  settings: Record<string, unknown>,
): { kept: Record<string, unknown>; stripped: string[] } {
  const kept: Record<string, unknown> = {};
  const stripped: string[] = [];
  const known = nodeType in manifest.nodeTypes;
  const dynamic = hasDynamicFields(nodeType);

  for (const [key, value] of Object.entries(settings)) {
    const spec = fieldSpec(nodeType, key);
    if (spec) {
      if (spec.access === 'open') kept[key] = value;
      else stripped.push(key);
      continue;
    }
    // 清单里没有：动态字段（ratio/resolution/duration）走这里，它们的合法性
    // 后端按所选模型的 uiSchema 判过了。未知类型同理放行。
    if (!known || dynamic) kept[key] = value;
    else stripped.push(key);
  }
  return { kept, stripped };
}
