import { describe, expect, it } from 'vitest';
import {
  buildImageGenerationInputs,
  buildVideoGenerationInputs,
  buildDynamicInputs,
  composePrompt,
  modelSettingDefaults,
} from './generationInputs';
import type { ModelUISchema, UISchemaField } from '@/shared/api';

const schemaEntries: Array<[string, UISchemaField]> = [
  ['ratio', { label: '比例', default: '16:9', options: [] }],
  ['resolution', { label: '分辨率', default: '2K', options: [] }],
  ['n', { label: '数量', default: 2, options: [] }],
  ['duration', { label: '时长', default: 5, options: [] }],
];

describe('buildDynamicInputs', () => {
  it('uses node settings first and falls back to schema defaults', () => {
    expect(
      buildDynamicInputs(schemaEntries, {
        ratio: '1:1',
        n: 4,
      }),
    ).toEqual({
      ratio: '1:1',
      resolution: '2K',
      n: 4,
      duration: 5,
    });
  });
});

// 这张表是「上游文本节点 + 节点输入框」两个提示词来源的合成契约，
// apps/api/internal/canvasinputs 有一份逐字同构的 Go 实现（CLI 走它），
// 改这里必须同步改那边的 TestComposePrompt。
describe('composePrompt', () => {
  it.each([
    ['两边都有 → 上游在前、输入框在后，\\n 分隔', '本地词', '上游词', '上游词\n本地词'],
    ['只有输入框 → 原样', '本地词', '', '本地词'],
    ['只有上游文本 → 原样', '   ', '上游词', '上游词'],
    ['两边都空 → 占位符', '  ', '   ', '(no prompt)'],
  ])('%s', (_name, prompt, fallback, want) => {
    expect(composePrompt(prompt, fallback)).toBe(want);
  });
});

describe('buildImageGenerationInputs', () => {
  it('matches the image submit inputs including computed size and first image alias', () => {
    expect(
      buildImageGenerationInputs({
        prompt: '',
        fallbackPrompt: '上游文本',
        dynamicInputs: {
          ratio: '1:1',
          resolution: '1K',
          n: 2,
        },
        connectedImages: ['https://example.com/a.png', 'https://example.com/b.png'],
      }),
    ).toEqual({
      prompt: '上游文本',
      ratio: '1:1',
      resolution: '1K',
      n: 2,
      size: '1024x1024',
      width: 1024,
      height: 1024,
      images: ['https://example.com/a.png', 'https://example.com/b.png'],
      image: 'https://example.com/a.png',
    });
  });

  it('uses the no-prompt placeholder when both local and upstream prompt are empty', () => {
    expect(
      buildImageGenerationInputs({
        prompt: '   ',
        fallbackPrompt: '',
        dynamicInputs: {},
        connectedImages: [],
      }).prompt,
    ).toBe('(no prompt)');
  });
});

describe('buildVideoGenerationInputs', () => {
  it('matches the video submit inputs including connected media arrays', () => {
    expect(
      buildVideoGenerationInputs({
        prompt: '镜头推进',
        fallbackPrompt: '备用文本',
        dynamicInputs: {
          ratio: '16:9',
          resolution: '720P',
          duration: 8,
        },
        connectedImages: ['https://example.com/a.png'],
        connectedVideos: ['https://example.com/a.mp4'],
        connectedAudios: ['https://example.com/a.mp3'],
      }),
    ).toEqual({
      // 上游文本节点的词在前、节点输入框的词在后，两句都提交。
      prompt: '备用文本\n镜头推进',
      ratio: '16:9',
      resolution: '720P',
      duration: 8,
      size: '1280x720',
      width: 1280,
      height: 720,
      images: ['https://example.com/a.png'],
      videos: ['https://example.com/a.mp4'],
      audios: ['https://example.com/a.mp3'],
    });
  });
});

describe('modelSettingDefaults', () => {
  it('返回每个字段的 default，供切模型时重置参数', () => {
    const uiSchema: ModelUISchema = {
      ratio: { default: '1:1', options: [{ value: '1:1', label: '1:1' }] },
      resolution: { default: '1K', options: [{ value: '1K', label: '1K' }] },
      n: { default: 1, options: [{ value: 1, label: '1' }] },
    };
    expect(modelSettingDefaults(uiSchema)).toEqual({ ratio: '1:1', resolution: '1K', n: 1 });
  });

  it('字段无 default 时退回第一个选项，保证对新模型合法', () => {
    const uiSchema: ModelUISchema = {
      ratio: { options: [{ value: '9:16', label: '竖屏' }, { value: '1:1', label: '方' }] },
    };
    expect(modelSettingDefaults(uiSchema)).toEqual({ ratio: '9:16' });
  });

  it('空/undefined schema 返回空对象；prompt 等非 schema 字段本就不在内', () => {
    expect(modelSettingDefaults(undefined)).toEqual({});
    expect(modelSettingDefaults({})).toEqual({});
  });
});
