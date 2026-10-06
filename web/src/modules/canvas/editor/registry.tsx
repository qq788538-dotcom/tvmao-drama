import type { ComponentType } from 'react';
import {
  AudioLines,
  Camera,
  Clapperboard,
  Eye,
  HardDrive,
  Sparkles,
  // Split 只被下面隐藏的「图片对比」菜单项引用，恢复时一起放开。
  // Split,
  Type,
  Upload,
  Video,
} from 'lucide-react';
import { InputImageNode } from './nodes/InputImageNode';
import { AudioInputNode } from './nodes/AudioInputNode';
import { TextNode } from './nodes/TextNode';
import { GenImageNode } from './nodes/GenImageNode';
import { GenVideoNode } from './nodes/GenVideoNode';
import { GenAudioNode } from './nodes/GenAudioNode';
import { VideoInputNode } from './nodes/VideoInputNode';
import { ImageCompareNode } from './nodes/ImageCompareNode';
import { LocalSaveNode } from './nodes/LocalSaveNode';
import { PreviewNode } from './nodes/PreviewNode';
import { SubtitleEraseNode } from './nodes/SubtitleEraseNode';
import { VideoUpscaleNode, VideoWhiteModelNode } from './nodes/TieredVideoNode';
import { ScriptNode } from './nodes/script/ScriptNode';
import { DirectorDeskNode } from './nodes/director/DirectorDeskNode';
import type { NodeType } from './types';

export const nodeComponents: Record<NodeType, ComponentType<{ id: string }>> = {
  'input-image': InputImageNode,
  'input-audio': AudioInputNode,
  'text-node': TextNode,
  'gen-image': GenImageNode,
  'gen-video': GenVideoNode,
  'gen-audio': GenAudioNode,
  'video-input': VideoInputNode,
  'image-compare': ImageCompareNode,
  'local-save': LocalSaveNode,
  'preview': PreviewNode,
  'subtitle-erase': SubtitleEraseNode,
  'video-upscale': VideoUpscaleNode,
  'video-whitemodel': VideoWhiteModelNode,
  script: ScriptNode,
  'director-desk': DirectorDeskNode,
};

/** 统一上传入口的特殊菜单类型。点击时根据所选文件的 MIME 决定实际的 node
 *  类型（input-image / input-audio / video-input），然后自动上传。
 *  它不是一个真正的 NodeType。 */
export const UPLOAD_MENU_TYPE = '__upload__' as const;

export interface NodeMenuItem {
  type: NodeType | typeof UPLOAD_MENU_TYPE;
  label: string;
  icon: typeof Eye;
  group: 'input' | 'generate' | 'output' | 'workflow';
  hint?: string;
}

export const nodeMenuItems: NodeMenuItem[] = [
  { type: UPLOAD_MENU_TYPE, label: '上传', icon: Upload, group: 'input', hint: '图片 / 音频 / 视频' },
  { type: 'text-node', label: '文字节点', icon: Type, group: 'input', hint: '组织提示词与文本' },
  // input-image / input-audio / video-input 被隐藏——上面新的「上传」
  // 入口会根据文件 MIME 按需创建它们。它们仍然注册在 nodeComponents 里，
  // 这样老的已保存项目还能正常渲染。
  { type: 'gen-image', label: 'AI 绘图', icon: Sparkles, group: 'generate', hint: '调用图片模型' },
  { type: 'gen-video', label: 'AI 视频', icon: Video, group: 'generate', hint: '调用视频模型' },
  { type: 'gen-audio', label: 'AI 音频', icon: AudioLines, group: 'generate', hint: '调用音频模型' },
  { type: 'script', label: '脚本', icon: Clapperboard, group: 'generate', hint: '分镜脚本 → 批量生视频' },
  { type: 'director-desk', label: '3D导演台', icon: Camera, group: 'generate', hint: '3D 摆位预演 → 机位截图' },
  // 「输出」组：「保存到本地」按 spec F2 放回菜单；「图片对比」「预览」按用户要求继续
  // 隐藏——只是不出现在菜单里，不是移除：仍注册在 nodeComponents，存量项目里的这些
  // 节点照常渲染。要恢复显示，取消对应行的注释即可。
  // { type: 'image-compare', label: '图片对比', icon: Split, group: 'output', hint: '滑动对比两张图片' },
  { type: 'local-save', label: '保存到本地', icon: HardDrive, group: 'output', hint: '下载图片或视频' },
  // { type: 'preview', label: '预览', icon: Eye, group: 'output', hint: '查看图片或视频' },
];
