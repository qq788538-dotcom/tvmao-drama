// apps/web/src/canvas/workflowSerializer.test.ts
import { describe, expect, it } from 'vitest';
import type { CanvasSnapshot } from './types';
import { compileToSkill, decodeFromSkill } from './workflowSerializer';

const emptyView = { x: 0, y: 0, zoom: 1 };

describe('compileToSkill', () => {
  it('compiles a single gen-image node into one step', () => {
    const snapshot: CanvasSnapshot = {
      nodes: [
        {
          id: 'a',
          type: 'gen-image',
          x: 0, y: 0, width: 280, height: 220,
          settings: { prompt: '一只柴犬', ratio: '1:1' },
        },
      ],
      connections: [],
      view: emptyView,
    };
    const { skill, warnings } = compileToSkill(snapshot, { name: '出图', description: '' });
    expect(warnings).toEqual([]);
    expect(skill.workflow_steps).toHaveLength(1);
    expect(skill.workflow_steps[0].tool_name).toBe('generate_image');
    expect(skill.workflow_steps[0].inputs).toMatchObject({ prompt: '一只柴犬', ratio: '1:1' });
  });

  it('translates a connection into a {{step.X.url}} image ref', () => {
    const snapshot: CanvasSnapshot = {
      nodes: [
        { id: 'a', type: 'gen-image', x: 0, y: 0, width: 280, height: 220, settings: { prompt: 'concept' } },
        { id: 'b', type: 'gen-video', x: 400, y: 0, width: 280, height: 220, settings: { prompt: 'animate' } },
      ],
      connections: [{ id: 'c1', from: 'a', to: 'b', inputType: 'image' }],
      view: emptyView,
    };
    const { skill } = compileToSkill(snapshot, { name: 'logo→video', description: '' });
    const videoStep = skill.workflow_steps.find((s) => s.tool_name === 'generate_video');
    expect(videoStep).toBeDefined();
    const images = videoStep!.inputs.images as string[];
    expect(images).toHaveLength(1);
    expect(images[0]).toMatch(/^\{\{step\.step_1_gen_image\.url\}\}$/);
  });

  it('promotes input nodes to {{input.X}} placeholders + param_defaults entries', () => {
    const snapshot: CanvasSnapshot = {
      nodes: [
        { id: 'novel', type: 'text-node', x: 0, y: 0, width: 240, height: 140, content: '从前有座山' },
        { id: 'img', type: 'gen-image', x: 320, y: 0, width: 280, height: 220, settings: { prompt: 'draw it' } },
      ],
      connections: [{ id: 'c1', from: 'novel', to: 'img', inputType: 'text' }],
      view: emptyView,
    };
    const { skill } = compileToSkill(snapshot, { name: 't', description: '' });
    const step = skill.workflow_steps[0];
    expect(step.inputs.prompt).toMatch(/^\{\{input\.input_text_node_/);
    const keys = Object.keys(skill.param_defaults ?? {});
    expect(keys).toHaveLength(1);
    expect(keys[0]).toMatch(/^input_text_node_/);
  });

  it('warns about unsupported node types but still compiles supported ones', () => {
    const snapshot: CanvasSnapshot = {
      nodes: [
        // 旧快照里可能残留已下线的节点类型——serializer 必须对未知类型给 warning 而不是崩。
        { id: 'x', type: 'legacy-node' as never, x: 0, y: 0, width: 280, height: 220 },
        { id: 'y', type: 'gen-image', x: 320, y: 0, width: 280, height: 220, settings: { prompt: 'p' } },
      ],
      connections: [],
      view: emptyView,
    };
    const { skill, warnings } = compileToSkill(snapshot, { name: 't', description: '' });
    expect(warnings.length).toBeGreaterThan(0);
    expect(warnings[0]).toContain('legacy-node');
    expect(skill.workflow_steps).toHaveLength(1);
  });
});

describe('decodeFromSkill', () => {
  it('round-trips a gen-image → gen-video pair through compile + decode', () => {
    const original: CanvasSnapshot = {
      nodes: [
        { id: 'a', type: 'gen-image', x: 0, y: 0, width: 280, height: 220, settings: { prompt: 'p1', ratio: '9:16' } },
        { id: 'b', type: 'gen-video', x: 400, y: 0, width: 280, height: 220, settings: { prompt: 'p2', duration: 4 } },
      ],
      connections: [{ id: 'c1', from: 'a', to: 'b', inputType: 'image' }],
      view: emptyView,
    };
    const { skill } = compileToSkill(original, { name: 't', description: '' });
    const { nodes, connections, warnings } = decodeFromSkill(skill);
    expect(warnings).toEqual([]);
    expect(nodes.filter((n) => n.type === 'gen-image')).toHaveLength(1);
    expect(nodes.filter((n) => n.type === 'gen-video')).toHaveLength(1);
    expect(connections).toHaveLength(1);
    expect(connections[0].inputType).toBe('image');
    const imageNode = nodes.find((n) => n.type === 'gen-image')!;
    expect((imageNode.settings as { ratio?: string })?.ratio).toBe('9:16');
    const videoNode = nodes.find((n) => n.type === 'gen-video')!;
    expect((videoNode.settings as { duration?: number })?.duration).toBe(4);
  });

  it('decodes {{input.X}} into a text-node + connection', () => {
    const skill = {
      name: 't',
      description: '',
      category: 'workflow',
      skill_type: 'workflow' as const,
      workflow_steps: [{
        name: 'step_1_gen_image',
        tool_name: 'generate_image',
        inputs: { prompt: '{{input.input_novel_input_abc123}}' },
      }],
      param_defaults: { input_novel_input_abc123: '' },
    };
    const { nodes, connections } = decodeFromSkill(skill);
    expect(nodes.filter((n) => n.type === 'text-node')).toHaveLength(1);
    expect(nodes.filter((n) => n.type === 'gen-image')).toHaveLength(1);
    expect(connections).toHaveLength(1);
    expect(connections[0].inputType).toBe('text');
  });

  it('warns on unknown tool_name', () => {
    const skill = {
      name: 't',
      description: '',
      category: 'workflow',
      skill_type: 'workflow' as const,
      workflow_steps: [{ name: 'x', tool_name: 'frobnicate', inputs: {} }],
    };
    const { nodes, warnings } = decodeFromSkill(skill);
    expect(nodes).toHaveLength(0);
    expect(warnings.some((w) => w.includes('frobnicate'))).toBe(true);
  });
});
