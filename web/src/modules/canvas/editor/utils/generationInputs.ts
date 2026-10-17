import type { ModelUISchema, UISchemaField } from '@/shared/api';
import { calculateResolution } from './resolution';

type SettingsLike = Record<string, unknown> | undefined;

/**
 * 切换模型时用：返回新模型 uiSchema 每个字段的默认值 patch，把节点参数(比例/分辨率/
 * 张数/时长等)重置为新模型的默认——否则旧模型的 16:9 之类会带到新模型，可能对它非法。
 * default 缺省时退回第一个选项，保证值对新模型合法。prompt 等非 schema 字段不在内、不动。
 */
export function modelSettingDefaults(uiSchema: ModelUISchema | null | undefined): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const [key, field] of Object.entries(uiSchema ?? {})) {
    if (!field) continue;
    const def = field.default ?? field.options?.[0]?.value;
    if (def !== undefined) out[key] = def;
  }
  return out;
}

export function buildDynamicInputs(
  schemaEntries: Array<[string, UISchemaField]>,
  settings: SettingsLike,
): Record<string, unknown> {
  const inputs: Record<string, unknown> = {};
  for (const [key, field] of schemaEntries) {
    const value = settings?.[key];
    inputs[key] = value !== undefined && value !== null ? value : field.default ?? '';
  }
  return inputs;
}

/**
 * 合成最终提示词：上游文本节点的词在前、节点输入框的词在后，换行分隔。
 *
 * 两者是**互补**关系而不是二选一——上游文本通常是从剧本/前一节点流下来的基底描述，
 * 输入框是用户对这个节点当场的追加或修正，丢掉任何一边都不是用户的意思。后置输入框
 * 是因为多数模型对靠后的指令更敏感，「修正」压得住「基底」。分隔符用 `\n`，和多个
 * 上游文本节点之间已有的 `linked.texts.join('\n')` 保持同一种。
 *
 * 都空时给占位符：上游对空 prompt 的行为不一致，占位符比空串可预期。正常路径走不到
 * 这里（两边都空时提交按钮已禁用），它只兜底非 UI 的调用方。
 */
export function composePrompt(prompt: string, fallbackPrompt: string): string {
  const parts = [fallbackPrompt.trim(), prompt.trim()].filter(Boolean);
  return parts.join('\n') || '(no prompt)';
}

export function buildImageGenerationInputs({
  prompt,
  fallbackPrompt,
  dynamicInputs,
  connectedImages,
}: {
  prompt: string;
  fallbackPrompt: string;
  dynamicInputs: Record<string, unknown>;
  connectedImages: string[];
}): Record<string, unknown> {
  const ratio = (dynamicInputs.ratio as string | undefined) ?? '1:1';
  const resolution = (dynamicInputs.resolution as string | undefined) ?? '1K';
  const { str: size, w: width, h: height } = calculateResolution(ratio, resolution);

  return {
    prompt: composePrompt(prompt, fallbackPrompt),
    ...dynamicInputs,
    size,
    width,
    height,
    images: connectedImages,
    image: connectedImages[0],
  };
}

/**
 * 音频生成入参。音频没有画面，不算 size/width/height；上游连的音频（音色参考）
 * 走 audios，同时给 audio(首个)——对齐 image/images 的先例，让上游 requestTemplate
 * 用 {{audio}} 或 {{audios}} 都能接。
 *
 * 注意：CLI 暂不支持提交 gen-audio（cliapi wantKind 不含），所以后端 canvasinputs
 * 没有对应的 BuildAudioInputs；CLI 要支持时必须补同构 + golden（见 inputs.go 包注释）。
 */
export function buildAudioGenerationInputs({
  prompt,
  fallbackPrompt,
  dynamicInputs,
  connectedAudios,
}: {
  prompt: string;
  fallbackPrompt: string;
  dynamicInputs: Record<string, unknown>;
  connectedAudios: string[];
}): Record<string, unknown> {
  return {
    prompt: composePrompt(prompt, fallbackPrompt),
    ...dynamicInputs,
    audios: connectedAudios,
    audio: connectedAudios[0],
  };
}

export function buildVideoGenerationInputs({
  prompt,
  fallbackPrompt,
  dynamicInputs,
  connectedImages,
  connectedVideos,
  connectedAudios,
}: {
  prompt: string;
  fallbackPrompt: string;
  dynamicInputs: Record<string, unknown>;
  connectedImages: string[];
  connectedVideos: string[];
  connectedAudios: string[];
}): Record<string, unknown> {
  const ratio = (dynamicInputs.ratio as string | undefined) ?? '16:9';
  const resolution = (dynamicInputs.resolution as string | undefined) ?? '720P';
  const { str: size, w: width, h: height } = calculateResolution(ratio, resolution);

  return {
    prompt: composePrompt(prompt, fallbackPrompt),
    ...dynamicInputs,
    size,
    width,
    height,
    images: connectedImages,
    videos: connectedVideos,
    audios: connectedAudios,
  };
}
