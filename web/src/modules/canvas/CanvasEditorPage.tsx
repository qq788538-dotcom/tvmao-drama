import axios, { type AxiosError } from 'axios';
import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { App as AntApp, Button, Modal, Tooltip } from 'antd';
import { ArrowLeft, BotMessageSquare, LayoutPanelLeft, Save, Share2, Waypoints } from 'lucide-react';
import { BrandLogo } from '@/shared/BrandLogo';
import { CanvasEditor } from '@/modules/canvas/editor/CanvasEditor';
import { StoryboardView } from '@/modules/canvas/storyboard/StoryboardView';
import { Sidebar } from '@/modules/canvas/editor/Sidebar';
import { Lightbox } from '@/modules/canvas/editor/Lightbox';
import { Minimap } from '@/modules/canvas/editor/Minimap';
import { ShortcutsHint } from '@/modules/canvas/editor/ShortcutsHint';
import { useCanvasStore } from '@/modules/canvas/editor/store';
import { VersionUpdateGate } from '@/shared/useVersionCheck';
import {
  configureCanvasPersistence,
  flushCanvasMutations,
  getCanvasOwnerToken,
  onCanvasOwnerLost,
  persistNodeById,
  resetCanvasPersistence,
  seedCanvasBaselines,
  setCanvasOwnerToken,
} from '@/modules/canvas/editor/persistence/canvasMutations';
import { loadLocalView } from '@/modules/canvas/editor/persistence/localView';
import {
  type CanvasViewMode,
  loadCanvasViewMode,
  saveCanvasViewMode,
} from '@/modules/canvas/editor/persistence/localCanvasView';
import {
  type Snapshot,
  claimCanvasOwner,
  getAuthContexts,
  getProject,
  getSnapshot,
  getToken,
  heartbeatCanvasOwner,
  isCanvasOwnerLostError,
  me as fetchMe,
  probeCanvasOwner,
  listRuntimeModels,
  updateProject,
} from '@/shared/api';
import { ownerTokenMark, shouldObserve } from '@/modules/canvas/editor/observeMode';
import { parseRemoteCanvasOp } from '@/modules/canvas/editor/remoteOpFrame';
import { newTeamOwnerToken } from '@/modules/canvas/editor/teamOwnerToken';
import { parseTransientDragPositions, setTransientChannel } from '@/modules/canvas/editor/transientChannel';
import {
  parsePresenceMembers,
  resetCanvasPresence,
  useCanvasPresenceStore,
} from '@/modules/canvas/editor/presenceStore';
import { PresenceBar } from '@/modules/canvas/editor/PresenceBar';
import { AgentClient } from '@/modules/canvas/editor/agent/agentClient';
import type { Frame } from '@/modules/canvas/editor/agent/types';
import type { CanvasSnapshot } from '@/modules/canvas/editor/types';
import { HistoryPanel } from '@/modules/canvas/panels/HistoryPanel';
import { AgentPanel, loadAgentPanelWidth, saveAgentPanelWidth } from '@/modules/canvas/editor/agent/AgentPanel';
import { useAgentHandoff } from '@/modules/canvas/editor/agent/agentHandoff';
import { agentDropPointFor, claimAgentSlot } from '@/modules/canvas/editor/agent/dropPoint';
import { applyCanvasOpArtifact, isCanvasOpArtifact } from '@/modules/canvas/editor/agent/canvasOps';
import { LibraryPanel } from '@/modules/canvas/panels/LibraryPanel';
import { PublishWorkflowModal } from '@/modules/canvas/panels/PublishWorkflowModal';
import { PublishSharePopover } from '@/modules/canvas/panels/PublishSharePopover';
import { TvShowSubmitModal } from '@/modules/canvas/panels/TvShowSubmitModal';
import '../../styles/canvas.css';

// 收到 canvas_changed 后**整拉兜底**的防抖窗口（观看模式的每一帧、以及团队
// 并发编辑里 version 缺口/没带 op 的那些帧）。一次拖拽/一轮 agent 生成会连着
// 推好几帧，逐帧拉快照既浪费也会让画面抖；500ms 把一串合成一次，实时感又不至
// 于被拖垮。带 op 且版本连续的帧走本地合并，根本不进这条路。
const OBSERVE_REFRESH_DEBOUNCE_MS = 500;

// 整拉兜底的最大等待：从第一帧「需要整拉」算起，最多拖这么久就必须拉一次，
// 不管期间还有多少帧把 500ms 短窗重置。2s = 用户能忍的「怎么还没同步」上限，
// 又足够长到不会把一串连发的帧拆成好几次整拉。
const REFRESH_MAX_WAIT_MS = 2000;

export function CanvasEditorPage() {
  const navigate = useNavigate();
  const { projectId } = useParams<{ projectId: string }>();
  const pid = Number(projectId);
  const load = useCanvasStore((s) => s.load);
  const setReadOnly = useCanvasStore((s) => s.setReadOnly);

  // 故事板是盖在画布上的覆盖层，不是路由：切过去时 CanvasEditor 不卸载，
  // 视口、选中态、正在跑的生成任务全部原样活着，切回来还在原地。
  //
  // 初值从 localStorage 惰性读取（全局一个值，所有画布共用）。这里必须是
  // useState 的初始化函数而不是 useEffect——事后 setView 会先渲染一帧画布
  // 再跳到故事板，用户看到闪一下，那一帧的画布还真挂载了一次。
  const [view, setViewState] = useState<CanvasViewMode>(loadCanvasViewMode);
  const setView = useCallback((next: CanvasViewMode) => {
    setViewState(next);
    saveCanvasViewMode(next);
  }, []);
  const [ready, setReady] = useState(false);
  const [lastSyncedAt, setLastSyncedAt] = useState<string | null>(null);
  // 编辑权：本页丢失编辑权（有更新的页面接管了同一画布）后进入只读，
  // 弹框要求刷新。owner-token 本身存在 canvasMutations 模块里，页面只关心
  // 「是否已失权」这个布尔。
  const [ownerLost, setOwnerLost] = useState(false);
  const [claimingOwner, setClaimingOwner] = useState(false);
  // 观看模式：**身份未知**（/auth/contexts 拉失败）时打开一个别人正在编辑的
  // 项目，本页不抢锁，只读旁观并靠 WS 帧刷新。null = 正常编辑态。
  // 团队身份不再进这个态（团队直写，见下面的 claim effect），个人身份从来也
  // 不进（一期就是直接 Claim）。
  // 只是一个布尔：一期这里存着 editorActorId（给横幅换邮箱用），但邮箱解析随
  // 团队观看下线一起删了，它从此只写不读。
  const [isObserving, setIsObserving] = useState(false);
  const [takingOver, setTakingOver] = useState(false);
  const [activeTool, setActiveTool] = useState<'select' | 'history'>('select');
  const [historyOpen, setHistoryOpen] = useState(false);
  // 首页「故事灵感」入口带着交接单进来时，面板必须随页面一起挂载并滑入——
  // 交接单的消费方是 AgentPanel（见 agentHandoff.ts），面板不挂载它就永远发不
  // 出去。用惰性初始值而不是 effect：effect 会先渲染一帧关闭态再打开，面板
  // 闪一下才滑入。这里只 peek 不 take，取走的动作留给 AgentPanel。
  const [chatOpen, setChatOpen] = useState(() => useAgentHandoff.getState().peek(pid) !== null);
  // AgentPanel 首次打开前不挂载：挂载即建 WS、拉 threads 并渲染首个会话的
  // 全部历史消息（消息里的图片会整批下载）。用户没打开过对话就不该发生这些。
  // 打开过一次后保持挂载——保留 WS 连接和进行中的 agent 运行状态，关闭面板
  // 只是滑出屏幕。
  const [agentEverOpened, setAgentEverOpened] = useState(false);
  useEffect(() => {
    if (chatOpen) setAgentEverOpened(true);
  }, [chatOpen]);
  // Agent 面板宽度的真源在这里而不是 AgentPanel 内部：故事板覆盖层要按同一个
  // 宽度让位（挤压布局），拖拽调宽时两者必须同帧更新，不能各存一份。
  const [agentPanelWidth, setAgentPanelWidth] = useState<number>(loadAgentPanelWidth);
  useEffect(() => {
    saveAgentPanelWidth(agentPanelWidth);
  }, [agentPanelWidth]);
  // 进入故事板模式自动打开 Agent 面板（产品语义：故事板 = 对话驱动的视图）。
  // 只在切入瞬间触发——用户在故事板里手动关掉后不会被强制重开，下次切入再开。
  // useLayoutEffect 而非 useEffect：覆盖层的 paddingRight 由 chatOpen 驱动，
  // paint 后才 set 的话会先闪一帧全宽故事板再收窄。
  useLayoutEffect(() => {
    if (view === 'storyboard') setChatOpen(true);
  }, [view]);
  const [libraryOpen, setLibraryOpen] = useState(false);
  const accessDeniedHandledRef = useRef(false);
  const loadedProjectRef = useRef<number | null>(null);
  // 本地已载到的快照版本 + 观看端重拉快照的防抖句柄（详见下面的实时刷新通道）。
  const loadedVersionRef = useRef(0);
  const refreshTimerRef = useRef<number | undefined>(undefined);
  // 整拉请求的代次（防重叠请求先发后到）与「本轮防抖里第一帧的时刻」（防密集
  // 缺口帧把窗口无限重置）。两者的道理写在 refreshSnapshot / scheduleFullRefresh 上。
  const refreshGenerationRef = useRef(0);
  const refreshRequestedAtRef = useRef<number | undefined>(undefined);
  // 团队项目的来源指纹（每标签页一个，详见 teamOwnerToken.ts）。惰性初始化
  // 且此后不再变：换一次值就等于换一个「自己」，正在飞的写回来会被当成队友的
  // 改动重放一遍。
  const teamOwnerTokenRef = useRef<string | null>(null);
  if (teamOwnerTokenRef.current === null) teamOwnerTokenRef.current = newTeamOwnerToken();
  // 按 action 维度的落点账本，记录同一 action 已占用的网格格子号；让一次
  // action 产生的多个节点按序号排进 5 列网格，对齐且互不重叠。
  // 模型目录：agent 切模型时要按新模型的 uiSchema 重置比例/分辨率/时长，与
  // 用户在节点工具条上手动切模型走同一条路（canvasOps 的 withDynamicFieldReset
  // 复用 UI 那个 modelSettingDefaults）。queryKey 与 GenImage/GenVideo 节点相同，
  // react-query 缓存共享——这里不会多发一次请求。
  const runtimeModelsQuery = useQuery({
    queryKey: ['runtime-models'],
    queryFn: listRuntimeModels,
    staleTime: 60_000,
  });
  const resolveUiSchema = useCallback(
    (modelId: string) => runtimeModelsQuery.data?.find((m) => m.modelId === modelId)?.uiSchema ?? undefined,
    [runtimeModelsQuery.data],
  );

  const agentDropSlotsRef = useRef<Record<string, Set<number>>>({});
  const slotsForAction = useCallback((actionId: string) => {
    const existing = agentDropSlotsRef.current[actionId];
    if (existing) return existing;
    const fresh = new Set<number>();
    agentDropSlotsRef.current[actionId] = fresh;
    return fresh;
  }, []);

  // 把 AgentPanel 的产出物作为预览节点拉到画布上，锚定在视口中心
  // 附近（往左偏一点，避开 420px 宽的抽屉）。
  const onAgentArtifact = useCallback(
    ({
      thread_id,
      action_id,
      artifact,
    }: {
      thread_id: string;
      action_id: string;
      artifact: { artifact_type?: string; artifact_id: string; url?: string; name?: string; width?: number; height?: number; prompt?: string; text?: string; payload?: unknown };
    }) => {
      // 画布操作五件套（增/改/删节点、连/断连线，后端 tools_canvas_ops.go）
      // 不走下面「落新产物节点 + 排布」的逻辑——它们操作的是画布现有结构，
      // 由 canvasOps 执行器调 store 现有动作完成（undo/持久化/连线清理全复用），
      // 不改 status、不触发任何生成。
      if (isCanvasOpArtifact(artifact.artifact_type)) {
        const state = useCanvasStore.getState();
        applyCanvasOpArtifact({
          store: state,
          artifact,
          agentMeta: {
            threadId: thread_id,
            actionId: action_id,
            artifactId: artifact.artifact_id,
            agentName: 'planner',
          },
          // 未带坐标的 create 与其他 agent 产物共享同一个 action 内的网格账本，
          // 按名字里的序号排位；惰性调用——带显式坐标的操作不消耗格子。
          nextDropPoint: (name) => {
            const slot = claimAgentSlot(slotsForAction(action_id), name);
            const anchorX = (window.innerWidth / 2 - state.view.x - 210) / state.view.zoom;
            const anchorY = (window.innerHeight / 2 - state.view.y) / state.view.zoom;
            return agentDropPointFor({ anchorX, anchorY }, slot);
          },
          persistNode: persistNodeById,
          resolveUiSchema,
        });
        return;
      }

      // text_info（create_text_node 工具产出的剧本/文案）没有对象存储 URL，
      // 正文随帧携带——必须在 url 守卫之前分流，否则整类产物被静默丢弃。
      const isTextInfo = artifact.artifact_type === 'text_info';
      // gen_*_node_info（create_gen_node 工具）同理无 URL：铺生成节点预填提示词，
      // 不执行生成、不扣积分（铁律：生成必须用户逐节点手动点）。
      const genNodeType =
        artifact.artifact_type === 'gen_image_node_info'
          ? 'gen-image'
          : artifact.artifact_type === 'gen_video_node_info'
            ? 'gen-video'
            : null;
      if (genNodeType ? !artifact.prompt : isTextInfo ? !artifact.text : !artifact.url) return;
      const state = useCanvasStore.getState();
      // 落点按 artifact.name 里的序号排进网格——agent 的产物是并发生成、乱序
      // 到达的（第13段可能先于第1段回来），只靠到达序排会得到一片乱码顺序。
      const slot = claimAgentSlot(slotsForAction(action_id), artifact.name);

      const cw = window.innerWidth;
      const ch = window.innerHeight;
      const anchorX = (cw / 2 - state.view.x - 210) / state.view.zoom;
      const anchorY = (ch / 2 - state.view.y) / state.view.zoom;
      const { x, y } = agentDropPointFor({ anchorX, anchorY }, slot);

      const agentMeta = {
        threadId: thread_id,
        actionId: action_id,
        artifactId: artifact.artifact_id,
        agentName: 'planner',
      };

      if (genNodeType) {
        // 只建节点 + 预填提示词，状态留初始值（不置 succeeded——节点还没产出）。
        // 字段名以浏览器为准：gen-image 存 settings.prompt，gen-video 存
        // settings.videoPrompt（GenVideoNode 读写的就是它，CLI 曾在这栽过跟头）。
        const id = state.addNode(genNodeType, { x, y });
        state.updateNodeSettings(id, {
          ...(genNodeType === 'gen-video'
            ? { videoPrompt: artifact.prompt }
            : { prompt: artifact.prompt }),
          ...(artifact.name ? { name: artifact.name } : {}),
          agentMeta,
        });
        persistNodeById(id);
        return;
      }

      if (isTextInfo) {
        // 落成现有文字节点（TextNode 读写 settings.text，组件零改动）。
        // 尺寸给比默认（280×200）宽高一档——剧本/文案通常是长文，默认框
        // 只能看到开头几行。
        const id = state.addNode('text-node', { x, y });
        state.updateNode(id, { status: 'succeeded', width: 320, height: 360 });
        state.updateNodeSettings(id, {
          text: artifact.text,
          name: artifact.name,
          agentMeta,
        });
        // 与下面 preview 路径同一条持久化路：addNode 只 upsert 了空壳，补一次完整节点。
        persistNodeById(id);
        return;
      }

      const w = 320;
      const h = artifact.width && artifact.height
        ? Math.round((artifact.height / artifact.width) * w)
        : 320;

      const id = state.addNode('preview', { x, y });
      state.updateNode(id, { content: artifact.url, status: 'succeeded', width: w, height: h });
      state.updateNodeSettings(id, {
        // 【M4】必须按 artifact_type 写 settings.kind：签名 URL 通常没有扩展名，
        // PreviewNode（渲染/灯箱）和 canvasSummary（@ 候选归类）判媒体类型只能
        // 靠它，不写的话视频/音频产物整条链都被当图片处理（<img> 渲染直接破图）。
        kind:
          artifact.artifact_type === 'video_info'
            ? 'video'
            : artifact.artifact_type === 'audio_info'
              ? 'audio'
              : 'image',
        agentMeta,
      });
      // agent 产出物落到画布是前端建节点，addNode 只 upsert 了空壳，补一次完整节点。
      persistNodeById(id);
    },
    [],
  );

  const userQuery = useQuery({ queryKey: ['me'], queryFn: fetchMe });

  // 身份（个人 / 团队）。跟 TopNav 用同一个 queryKey，从首页点进来时通常已有
  // 缓存，不会给打开画布多加一次往返。currentTeamId=0 = 个人身份。
  const authToken = getToken();
  const contextsQuery = useQuery({
    queryKey: ['auth-contexts'],
    queryFn: getAuthContexts,
    enabled: !!authToken,
  });
  const currentTeamId = contextsQuery.data?.currentTeamId ?? 0;
  const isTeamIdentity = currentTeamId > 0;
  // 身份没问出来之前一步都不能走：probe/claim 的分流完全由它决定，抢先 Claim
  // 就是把正在编辑的队友踢下线。所以下面的 loading 闸也要含它。
  const identityResolved = !authToken || !contextsQuery.isPending;
  // contexts 拉失败 = 身份未知。**未知也要 probe**，不能当个人身份直接 Claim：
  // 拿「误踢正在编辑的同事」换「省一个 GET」方向是反的。probe 是只读的，而且
  // isSelf 由服务端按 JWT 里的 actor 算——个人用户在另一个 tab 持锁时仍然
  // isSelf=true → 照旧走 steal-Claim，零回归；代价只是错误路径上多一个 GET。
  const identityUnknown = !!authToken && contextsQuery.isError;

  const projectQuery = useQuery({
    queryKey: ['project', pid],
    queryFn: () => getProject(pid),
    enabled: Number.isFinite(pid) && pid > 0,
  });

  const snapshotQuery = useQuery<Snapshot, AxiosError>({
    queryKey: ['snapshot', pid],
    queryFn: () => getSnapshot(pid),
    enabled: Number.isFinite(pid) && pid > 0,
    refetchOnWindowFocus: false,
    staleTime: Infinity,
    retry: (failureCount, error) => {
      const status = error.response?.status;
      if (status === 403 || status === 404) return false;
      return failureCount < 1;
    },
  });

  const { message } = AntApp.useApp();
  const queryClient = useQueryClient();

  // 顶栏项目名就地改名：null = 展示态，字符串 = 编辑中的草稿。
  // Esc 走「先标记再 blur」而不是直接卸载 input——统一从 onBlur 单出口收尾，
  // 避免「卸载是否补发 focusout」这种浏览器差异造成放弃/提交二义。
  const [nameDraft, setNameDraft] = useState<string | null>(null);
  const renameEscapedRef = useRef(false);
  const nameInputRef = useRef<HTMLInputElement | null>(null);
  // 一次编辑只允许收口一次：点画布提交走 document pointerdown，点普通控件
  // 提交走 onBlur，两条路可能对同一次编辑先后各触发一次（pointerdown 之后
  // 浏览器紧接着把焦点挪走），不加闩会重复发一次改名请求。
  const renameClosedRef = useRef(false);

  // 提交改名。空名（含纯空格）或没改动 → 静默还原旧名，不发请求。
  // PUT /projects/{id} 是整行覆盖（name+description 一起写），必须回传原
  // description，否则就地改名会顺手把列表页里填的项目描述清空。
  const commitRename = useCallback(
    (raw: string) => {
      if (renameClosedRef.current) return;
      renameClosedRef.current = true;
      setNameDraft(null);
      const project = projectQuery.data;
      const next = raw.trim();
      if (!project || !next || next === project.name) return;
      updateProject(project.id, { name: next, description: project.description ?? '' })
        .then(() => queryClient.invalidateQueries({ queryKey: ['project', pid] }))
        .catch(() => message.error('重命名失败'));
    },
    [projectQuery.data, queryClient, pid, message],
  );

  // 点到输入框外面 = 提交。不能只靠 onBlur：画布为了平移/框选在 mousedown 里
  // preventDefault（CanvasEditor.tsx），把浏览器「点别处 → 原输入框失焦」这个
  // 默认行为一并掐掉了，点画布时 blur 根本不会发生。preventDefault 吞不掉事件
  // 本身，capture 阶段的 pointerdown 一定先于画布拿到，在这里主动收口。
  const renameEditing = nameDraft !== null;
  useEffect(() => {
    if (!renameEditing) return;
    const onPointerDownOutside = (e: PointerEvent) => {
      const input = nameInputRef.current;
      if (!input || input.contains(e.target as Node)) return;
      commitRename(input.value);
    };
    document.addEventListener('pointerdown', onPointerDownOutside, true);
    return () => document.removeEventListener('pointerdown', onPointerDownOutside, true);
  }, [renameEditing, commitRename]);
  const snapshotStatus = axios.isAxiosError(snapshotQuery.error)
    ? snapshotQuery.error.response?.status
    : undefined;

  useEffect(() => {
    if (snapshotStatus !== 403 && snapshotStatus !== 404) {
      accessDeniedHandledRef.current = false;
      return;
    }
    if (accessDeniedHandledRef.current) return;
    accessDeniedHandledRef.current = true;
    message.error('该画布不存在或你无权访问');
    navigate('/canvas', { replace: true });
  }, [message, navigate, snapshotStatus]);

  useEffect(() => {
    if (!snapshotQuery.data) return;
    setLastSyncedAt(snapshotQuery.data.syncedAt ?? null);
    // 记住「本地已载到哪个版本」——观看端拿它跟 canvas_changed 帧里的 version
    // 比大小，只有更新的版本才值得回拉快照。
    loadedVersionRef.current = snapshotQuery.data.version ?? 0;

    // snapshotQuery.data 在保存后不再被写回 query-cache（新模型下局部 mutation
    // 不 setQueryData），所以正常编辑期间这个 effect 不会重跑。仍保留守卫：
    // 只有首次进入、切换项目，或失权刷新显式 setReady(false) 后的 refetch，才
    // 重新初始化画布 store；否则会清空 selectedIds/undoStack，把正在输入的
    // AI绘图/AI视频 prompt 面板隐藏掉。
    if (ready && loadedProjectRef.current === pid) {
      return;
    }

    const raw = snapshotQuery.data.payload as Partial<CanvasSnapshot> | null | undefined;
    const nodes = Array.isArray(raw?.nodes) ? raw!.nodes : [];
    // view（pan/zoom）不再是服务端内容，改用当前浏览器按项目存的 localStorage。
    // 换电脑只丢视角，画布数据（节点/连线/内容）仍从服务端 JSON 加载。
    load({
      nodes,
      connections: Array.isArray(raw?.connections) ? raw!.connections : [],
      view: loadLocalView(pid),
    });
    // 播种字段级 diff 的基线。**只能喂服务端快照**（这里正是），本地导入的 JSON
    // 不能播——那些节点服务端没有，有了基线就会对不存在的节点发 PatchNode。
    // 不分个人/团队一起播：基线是 persistNodeById 的 diff 依据，与身份无关；
    // 不播只是退化成「每次发整节点 upsert」，白丢字段级 LWW。
    seedCanvasBaselines(nodes);
    loadedProjectRef.current = pid;
    setReady(true);
  }, [snapshotQuery.data, load, pid, ready]);

  // 持久化配置的公共尾段：两条路（团队直写 / 个人 Claim）拿到 token 后做的事
  // 完全一样，抽出来避免两处各写一份、改一处漏一处。
  const configurePersistence = useCallback(
    (token: string) => {
      setIsObserving(false);
      // 顺序要紧：先 configure 再设 token。configureCanvasPersistence 在换项目时
      // 会兜底 resetCanvasPersistence()，而那会把 ownerToken 清空——先设 token
      // 就等于把刚拿到的编辑权凭据交给 reset 抹掉，之后所有结构性 op 静默丢弃。
      configureCanvasPersistence({
        projectID: pid,
        onVersion: () => setLastSyncedAt(new Date().toISOString()),
        onError: (err) => {
          if (isCanvasOwnerLostError(err)) setOwnerLost(true);
          else message.error('保存失败');
        },
      });
      setCanvasOwnerToken(token);
      setOwnerLost(false);
    },
    [pid, message],
  );

  // 快照就绪后决定本页是「编辑」还是「观看」：
  //   - **团队身份：直接可写**（二期）。后端对团队身份跳过 owner.Require，
  //     并发写由 MySQL 行锁串行化——不 probe、不 Claim、不心跳，也就没有观看/
  //     接管/失锁这套单人锁 UI。ownerToken 降级成本标签页的来源指纹。
  //   - 个人身份：永远直接 Claim（一期行为逐字不变，零回归）。
  //   - 身份未知（contexts 拉失败）：走一期那条 probe 路线。宁可多一个 GET，
  //     也不能在身份不明时按团队直写——万一其实是个人身份，就少了那把锁。
  // 拿到 token 后配置局部 mutation 持久化（版本回写只用来刷新「已保存」时间戳）。
  useEffect(() => {
    if (!ready || !Number.isFinite(pid) || pid <= 0) return;
    if (!identityResolved) return;
    let cancelled = false;
    setClaimingOwner(true);

    const decide = async () => {
      if (isTeamIdentity) {
        configurePersistence(teamOwnerTokenRef.current!);
        return;
      }
      if (identityUnknown) {
        // probe 挂了（503/网络）当作「没探到」继续走 Claim：探测是为了不打扰
        // 队友，不该因为探测本身故障就把人锁在门外。
        const probe = await probeCanvasOwner(pid).catch((err) => {
          console.error('probe canvas owner failed', err);
          return null;
        });
        if (cancelled) return;
        if (shouldObserve(probe)) {
          setIsObserving(true);
          return;
        }
      }
      try {
        const claimed = await claimCanvasOwner(pid);
        if (cancelled) return;
        configurePersistence(claimed.ownerToken);
      } catch (err) {
        if (cancelled) return;
        console.error('claim canvas owner failed', err);
        message.error('获取画布编辑权失败');
      }
    };

    void decide().finally(() => {
      if (!cancelled) setClaimingOwner(false);
    });
    return () => {
      cancelled = true;
    };
  }, [pid, ready, message, identityResolved, isTeamIdentity, identityUnknown, configurePersistence]);

  // 观看模式 = 复用分享 viewer 那条只读通道：store 的 readOnly 挡住所有改
  // store 的 action，setReadOnly 内部同时翻 apiGuards，挡住网络侧写请求。
  // 退出（接管走 reload、或离开页面）必须还原，否则同标签页跳去别的画布还锁着。
  useEffect(() => {
    if (!isObserving) return;
    setReadOnly(true);
    return () => setReadOnly(false);
  }, [isObserving, setReadOnly]);

  // 每 10s 心跳续 owner TTL；一旦被新页面接管，心跳返回 409 → 进入只读。
  // 观看模式下本页压根没有 token，不发心跳。
  // 团队项目整条锁链路已下线（后端不校验 owner），心跳只会白刷一把没人读的锁，
  // 还会把团队 ownerToken 这个纯指纹写进 canvas_owner，误导排查。
  useEffect(() => {
    if (!ready || ownerLost || isObserving || isTeamIdentity) return;
    const timer = window.setInterval(() => {
      const token = getCanvasOwnerToken();
      if (!token) return;
      heartbeatCanvasOwner(pid, token).catch((err) => {
        if (isCanvasOwnerLostError(err)) setOwnerLost(true);
      });
    }, 10_000);
    return () => window.clearInterval(timer);
    // isObserving 必须在依赖里。probe 是异步的：ready 翻 true 那一帧
    // isObserving 还是 false，interval 已经建好，观看态是**之后**才到的第二次
    // state 变化——漏了它 effect 不重跑，这条 interval 就一直替本页续一把根本
    // 不属于它的锁（只剩 apiGuards 兜着）。
  }, [pid, ready, ownerLost, isObserving, isTeamIdentity]);

  // 局部 mutation 在保存时若发现自己已失权，会广播 owner-lost 事件；
  // 页面据此进入只读弹框。
  useEffect(() => onCanvasOwnerLost(() => setOwnerLost(true)), []);

  // 一期这里有一段「团队身份失锁 → 降级成观看模式」的 effect。二期删掉：团队
  // 项目根本不再持锁（后端跳过 owner.Require、本页不 Claim 不心跳），失锁事件
  // 对团队身份永远不会发生，留着只会让人以为团队还有观看态这条路。个人身份的
  // 失锁弹窗（下面的 Modal，条件 `ownerLost && !isTeamIdentity`）一行未动。
  //
  // 观看模式本身也随之只剩两个入口，都是**非团队**：一期身份未知（contexts 拉
  // 失败）时 probe 到别人在编辑。团队项目直接可写，没有横幅、没有「接管编辑」。

  // 横幅一律用通称。一期这里会拿 editorActorId 去团队成员名单里换邮箱，但那条
  // 查询的前置条件是 `currentTeamId > 0`，而观看模式现在只可能发生在**身份未知**
  // （contexts 拉失败 → currentTeamId 恒为 0）时——真团队身份直接可写，压根不进
  // 观看。也就是说那次查询永远 disabled、永远回退通称：留着只是一段看起来会
  // 解析邮箱、实际永远不解析的代码。谁在编辑这个信息由二期的 presence 名册
  // （帧里直接带邮箱）承担，不需要这条旁路。
  const editorLabel = '另一位成员';

  // 「接管编辑」= 现有的抢占式 Claim + 整页重载。
  //
  // 为什么是 reload 而不是就地转成编辑态：接管是低频动作，reload 走的是页面
  // 既有的加载路径（拉快照 → probe/claim → 配持久化 → 起心跳），一条路走到黑；
  // 就地迁移则要在运行中把 readOnly、ownerToken、持久化配置、心跳、undo 栈
  // 一个不落地切换过来，任何一处漏掉都是「看起来能编辑、其实存不上」。
  const takeOverEditing = useCallback(() => {
    setTakingOver(true);
    claimCanvasOwner(pid)
      .then(() => window.location.reload())
      .catch((err) => {
        console.error('take over canvas failed', err);
        message.error('接管编辑失败');
        setTakingOver(false);
      });
  }, [pid, message]);

  // ── 实时通道 ────────────────────────────────────────────────────────
  // 复用 agent 的 WebSocket（Hub 按 (userID, projectID) 分发，握手必须带
  // project_id，否则 projectID=0 收不到任何 canvas_changed）。
  //
  // 为什么在页面这一层单独建连、而不是复用 AgentPanel 里那个 AgentClient：
  // AgentPanel 只有在用户第一次打开对话后才挂载（agentEverOpened 门控），
  // 用户完全可能从头到尾不开对话——那条连接根本不存在。
  //
  // 二期起**团队项目常连**（编辑者也连）：队友的每一次写都要即时合并进本地画布，
  // 不再是「只有观看者才需要听」。个人项目仍只在观看态建连，编辑者一条都不多开。
  const refreshSnapshot = useCallback(() => {
    // 两道**并列**的过期守卫，缺一不可：
    //   1. 版本：两次整拉重叠先发后到、或整拉在飞期间乱序的 op 帧把本地推得比
    //      快照还新——旧响应落地就是拿旧内容盖新状态，loadedVersion 倒退、
    //      seedCanvasBaselines 被钉在旧状态，下一次 persist 于是把队友刚写的
    //      字段当成本地变更写回服务端（静默数据回滚）。快照不比本地新就丢。
    //   2. 代次（generation）：版本守卫管不到**离开这张画布**——切项目/卸载时
    //      在飞的响应会把上一个项目的快照 load 进当前 store（两个项目的版本号
    //      是两条时间线，比不出新旧）。清理里递增代次，作废在飞的整拉。
    const generation = ++refreshGenerationRef.current;
    getSnapshot(pid)
      .then((snap) => {
        if (generation !== refreshGenerationRef.current) return;
        const version = snap.version ?? 0;
        // 相等也丢：本地已经在这个版本上了，再 load 一次只会白清一次选中和
        // undo 栈（load 的既有语义），换不来任何新数据。
        if (version <= loadedVersionRef.current) return;
        const raw = snap.payload as Partial<CanvasSnapshot> | null | undefined;
        const nodes = Array.isArray(raw?.nodes) ? raw!.nodes : [];
        load(
          {
            nodes,
            connections: Array.isArray(raw?.connections) ? raw!.connections : [],
            // 保持本页**当前**的视角。这里若跟首次加载一样用 loadLocalView，
            // 队友每改一次就把画面弹回原点，根本没法用。
            view: useCanvasStore.getState().view,
          },
          // 同理保住选中与 undo 栈：整拉是「把本地对齐到服务端最新」，不是换画布。
          // CLI 写入不广播 op 帧，队友随后的任一次改动就会让本端进缺口态走到这里——
          // 这条路在团队 + CLI 混用时并不罕见，每次都把用户正开着的参数面板收起来、
          // 把他刚做的几步撤销记录清空，是能直接感知到的倒退。
          { preserveUiState: true },
        );
        // 整拉之后基线必须跟着换成这份服务端快照：不换的话，下一次 persist 会
        // 拿旧基线 diff，把「服务端已经有的字段」当成本地变更再发一遍，甚至把
        // 队友刚写的值按旧基线回滚。load() 自己已经清了挂起的远端 op 队列。
        seedCanvasBaselines(nodes);
        loadedVersionRef.current = version;
        setLastSyncedAt(snap.syncedAt ?? null);
      })
      .catch((err) => console.error('refresh canvas snapshot failed', err));
  }, [pid, load]);

  // 整拉兜底的防抖，**带最大等待上限**（trailing + maxWait）。
  //
  // 只有 500ms 尾防抖是不够的：进入缺口态之后，后面每一帧都还是缺口（本地版本
  // 不动），每帧都把窗口重置一次——多人同时编辑 + agent 批量回写时帧间隔稳定
  // 小于 500ms，于是整拉**永远不发生**，本地静默停在分叉状态不收敛。
  // 自首个待整拉的帧起最多等 REFRESH_MAX_WAIT_MS 就必须拉一次。
  const scheduleFullRefresh = useCallback(() => {
    const now = Date.now();
    if (refreshRequestedAtRef.current === undefined) refreshRequestedAtRef.current = now;
    const remaining = REFRESH_MAX_WAIT_MS - (now - refreshRequestedAtRef.current);
    const delay = Math.max(0, Math.min(OBSERVE_REFRESH_DEBOUNCE_MS, remaining));
    if (refreshTimerRef.current !== undefined) window.clearTimeout(refreshTimerRef.current);
    refreshTimerRef.current = window.setTimeout(() => {
      refreshTimerRef.current = undefined;
      refreshRequestedAtRef.current = undefined;
      refreshSnapshot();
    }, delay);
  }, [refreshSnapshot]);

  // 瞬态帧（拖拽实时流 / 「正在编辑」）与 presence 名册。**团队 only**：后端对
  // 个人身份既不转播也不入册，这里再挡一道——个人项目的行为必须逐字不变，
  // 靠「对端不发」来保证等于把这条纪律托付给了另一个进程。
  const handleTransientFrame = useCallback(
    (f: Frame) => {
      if (!isTeamIdentity) return;
      if (f.project_id && f.project_id !== pid) return;
      if (f.kind === 'drag') {
        const positions = parseTransientDragPositions(f.positions);
        if (positions.length > 0) useCanvasStore.getState().applyTransientPositions(positions);
        return;
      }
      if (f.kind === 'node_editing') {
        // actor_user_id 由服务端覆写；缺了它就认不出是谁，角标只能显示通称，
        // 但仍要显示——「有人在编辑」本身就是有用的信息。
        if (!f.node_id) return;
        useCanvasPresenceStore
          .getState()
          .noteNodeEditing(f.node_id, f.actor_user_id ?? 0, f.editing === true);
        return;
      }
      // 不认识的 kind 直接忽略：瞬态种类还会增长，老客户端见到新种类不该崩，
      // 也不该像权威帧那样退回整拉（瞬态本来就没有权威状态可拉）。
    },
    [isTeamIdentity, pid],
  );

  const handleRealtimeFrame = useCallback(
    (f: Frame) => {
      if (f.type === 'canvas_transient') {
        handleTransientFrame(f);
        return;
      }
      if (f.type === 'canvas_presence') {
        if (!isTeamIdentity) return;
        if (f.project_id && f.project_id !== pid) return;
        // 全量名册：有人进出就整份重发，本端直接替换，不做增量合并。
        useCanvasPresenceStore.getState().setMembers(parsePresenceMembers(f.members));
        return;
      }
      if (f.type !== 'canvas_changed') return;
      // 帧按用户维度分发，同一账号在别的项目里的改动也会到达这条连接。
      if (f.project_id && f.project_id !== pid) return;
      const version = f.version ?? 0;
      // 迟到帧 / 重复帧。整拉刚把 loadedVersion 推到更高值时，在途的旧帧都落在这里。
      if (version <= loadedVersionRef.current) return;
      // 自己触发的变更：本地早已应用（团队 ownerToken 是本标签页指纹，
      // 见 teamOwnerToken.ts）。观看者没有 token → mark 为空 → 恒不命中。
      const mark = ownerTokenMark(getCanvasOwnerToken());
      const isEcho = !!mark && f.source_mark === mark;

      // 版本连续时才本地合并——version 是服务端唯一的全序。缺口意味着中间有帧
      // 没到（写超时被跳过、断线窗口），此时本地状态已经跟服务端分叉，再合并
      // 只会把分叉固化下来，必须整拉。
      if (version === loadedVersionRef.current + 1) {
        if (isEcho) {
          // 自己的写不重放，但**版本要推进**：不推的话下一条队友的帧就成了
          // 「缺口」，每写一笔都触发一次整拉，本地合并等于没做。
          loadedVersionRef.current = version;
          return;
        }
        const op = parseRemoteCanvasOp(f.op);
        if (op) {
          useCanvasStore.getState().applyRemoteOp(op);
          loadedVersionRef.current = version;
          return;
        }
        // 没带 op（一期后端 / 摘要序列化失败 / 本客户端不认识的新 kind）→ 整拉。
      }
      scheduleFullRefresh();
    },
    [pid, scheduleFullRefresh, handleTransientFrame, isTeamIdentity],
  );

  // 帧处理器与刷新动作都走 ref 转发：它们的依赖变化不该把 WebSocket 拆了重连。
  const frameHandlerRef = useRef(handleRealtimeFrame);
  useEffect(() => {
    frameHandlerRef.current = handleRealtimeFrame;
  }, [handleRealtimeFrame]);
  const refreshRef = useRef(refreshSnapshot);
  useEffect(() => {
    refreshRef.current = refreshSnapshot;
  }, [refreshSnapshot]);

  // 团队项目常连；个人项目只在观看态连（编辑者零额外连接，一期行为不变）。
  // 必须等 ready：快照还没落地时 loadedVersion 还是 0，此刻到的帧一律被判成
  // 缺口，白触发一次整拉（观看态本来就在 ready 之后才可能为真，这条只对团队新增）。
  useEffect(() => {
    if (!ready) return;
    if (!isObserving && !isTeamIdentity) return;
    if (!authToken || !Number.isFinite(pid) || pid <= 0) return;
    const client = new AgentClient({ url: '/api/v1/agent/ws', token: authToken, projectId: pid });
    client.onFrame((f) => frameHandlerRef.current(f));
    // 断线期间推的帧是**丢掉**的（AgentClient 只重连，不补发）。重连成功后主动
    // 补拉一次：否则队友在断线窗口里的改动，要一直等到他下一次改动推来新帧才
    // 看得见——他要是就此收工，观看者屏幕上就永久停在旧版本。
    // 首次 open 跳过：那一刻快照刚加载完，再拉一遍纯属浪费。
    let opened = false;
    client.onOpen(() => {
      if (!opened) {
        opened = true;
        return;
      }
      refreshRef.current();
    });
    // 断线即清 presence。名册是服务端在连接进出时推的全量快照，**没有 TTL**：
    // 断线期间队友进出的每一次变更都收不到，屏幕上会留着一份越来越假的名册
    // （人早走了头像还在）。重连后服务端注册时会立刻重推一份全量，清空不会
    // 让名册少一帧。「正在编辑」角标同理一并清（它有 TTL，但没必要等 5s）。
    client.onClose(() => resetCanvasPresence());
    client.connect();
    // 把这条连接的发送能力交给瞬态通道——节点组件（NodeShell 拖拽 / TextNode
    // 编辑）就是从那里发帧的。**只有团队身份注册**：个人项目（含观看态）通道
    // 恒为 null，所有发送点自动退化成 no-op，一帧都发不出去。
    // 走模块级通道而不是 context：见 transientChannel.ts 顶部的取舍说明。
    if (isTeamIdentity) {
      setTransientChannel({
        projectId: pid,
        send: (frame) => client.sendCanvasTransient(frame),
      });
    }
    return () => {
      client.close();
      // 通道随连接一起注销；名册与「正在编辑」角标也一并清场——它们描述的是
      // 「此刻这条连接看到的人」，留到下一个项目就是一屏幻影。
      setTransientChannel(null);
      resetCanvasPresence();
      if (refreshTimerRef.current !== undefined) {
        window.clearTimeout(refreshTimerRef.current);
        refreshTimerRef.current = undefined;
      }
      refreshRequestedAtRef.current = undefined;
      // 作废在飞的整拉：响应回来时本页可能已经切到别的项目，load 下去就是把上
      // 一个项目的画布内容灌进当前画布。
      refreshGenerationRef.current += 1;
    };
  }, [ready, isObserving, isTeamIdentity, authToken, pid]);

  // 离开画布时清干净持久化运行态（owner-token、心跳队列、debounce 计时器、
  // 事件监听）。切项目由 configureCanvasPersistence 内部处理，这里兜卸载。
  useEffect(() => () => resetCanvasPersistence(), []);

  // flush 队列里排队/debounce 中的局部 mutation。发布前用它——发布出去的快照
  // 必须包含用户点击前那一刻还在 debounce 里的编辑。
  //
  // 已经不再有面向用户的「保存」入口了（侧边栏按钮和 Cmd+S 都已移除）：局部
  // mutation 重构后每个动作都即时落库，画布没有「未保存」这个概念。
  const saveNow = useCallback(async (): Promise<boolean> => {
    if (!ready || ownerLost) return false;
    try {
      await flushCanvasMutations();
      setLastSyncedAt(new Date().toISOString());
      return true;
    } catch (err) {
      console.error('save failed', err);
      if (isCanvasOwnerLostError(err)) setOwnerLost(true);
      else message.error('保存失败');
      return false;
    }
  }, [ready, ownerLost, message]);

  // Ctrl/Cmd + S 不再触发保存（画布即时落库，没有「保存」这个动作了），但仍然
  // 拦掉它：不拦的话浏览器会弹出「另存为网页」对话框——在一个网页应用里按 Cmd+S
  // 蹦出保存文件的窗口，比什么都不做更让人困惑。
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 's') e.preventDefault();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);

  // 「发布」拆成两个出口，浮层里选：发布作品(投稿广场，submitOpen——弹框
  // 内容是 Task 6 的范围，这里先只接住状态)/分享链接(shareOpen，走既有的
  // 精简版 PublishWorkflowModal)。popoverOpen 单独管浮层本身的开关。
  const [popoverOpen, setPopoverOpen] = useState(false);
  const [shareOpen, setShareOpen] = useState(false);
  const [submitOpen, setSubmitOpen] = useState(false);

  // 应用内跳转（如「返回」）。新模型下用户动作已即时落库，不再有「未保存」
  // 概念；只有 1.5s debounce 中的 prompt 可能还没提交，所以离开前先 flush
  // 一次队列再跳转，避免丢掉最后一次输入。
  const requestNavigate = useCallback((to: string) => {
    void flushCanvasMutations().catch(() => undefined).finally(() => navigate(to));
  }, [navigate]);

  if (!Number.isFinite(pid) || pid <= 0) {
    return <div className="p-8 text-white">无效的项目 ID</div>;
  }

  if (snapshotQuery.isPending || snapshotStatus === 401 || snapshotStatus === 403 || snapshotStatus === 404) {
    return <div className="p-8 text-white">Loading...</div>;
  }

  if (snapshotQuery.isError) {
    return <div className="p-8 text-white">加载画布失败，请稍后重试</div>;
  }

  // identityResolved 也在闸里：claim effect 的身份守卫在 setClaimingOwner(true)
  // **之前** return，所以身份未决时 claimingOwner 还是 false。少了这一条，快照
  // 比 /auth/contexts 先回来的冷加载里，用户会拿到一张能拖能改、但没有
  // ownerToken 的画布——mutation 全被静默丢弃，编辑看着生效实际没落库。
  if (!ready || claimingOwner || !identityResolved) {
    return <div className="p-8 text-white">Loading...</div>;
  }

  const balanceCredits = userQuery.data?.balanceCredits ?? 0;

  return (
    <div className="canvas-root">
      {/* 画布编辑器是壳外全屏路由,拿不到 AppShell 里的版本更新门。这里单独挂一份,
          否则部署新版后画布旧标签页会一直跑旧代码(尤其新的 /canvas-tasks 契约会打错)。 */}
      <VersionUpdateGate />

      {/* 第 0 层：全宽画布——铺满整个视口，让点状背景延伸到浮动顶栏和
          侧边栏后面（TapNow 的设计）。 */}
      <CanvasEditor projectId={pid} />

      {/* 第 0.5 层：故事板覆盖层。盖住画布和它的工具面板，但让开顶栏（z-40）。
          画布本身不卸载——切换成本接近零。
          data-storyboard-open 是画布快捷键的停用标记（CanvasEditor 的 onKey/
          空格抓手见到它直接返回）——画布被完全盖住但没卸载，selectedIds 仍在
          store 里，不停用的话在故事板视图按 Delete/Cmd+Z 会静默删掉/撤销画布
          上看不见的节点。与 3D 导演台的 data-director-desk-open 同一套机制。 */}
      {view === 'storyboard' && (
        <div
          data-storyboard-open
          // 页面底色比面板(#262626)暗一档：三个分区面板浮在这层暗底上，
          // 面板之间的间隙露出它，那道暗缝就是模块的分隔。两层同色的话
          // 模块之间会糊成一片（试过，只剩一条看不见的 1px 线）。
          // 铺满整个视口(inset-0)而不是从 top-14 开始：浮动顶栏不是满宽实心的
          // (top-3 left-4 right-4)，让开那 56px 的话，顶栏没盖到的地方会直接
          // 透出底下还活着的画布——节点和连线从顶栏两侧漏出来。覆盖层 z-30 <
          // 顶栏 z-40，铺满也不会挡住顶栏交互；故事板内容自己用 pt-14 让位。
          className="absolute inset-0 z-30 bg-[#141414] overflow-hidden"
          // Agent 面板开着时故事板内容向左让位（挤压而非被盖住）。让位用
          // paddingRight 而不是收窄覆盖层本身：背景层保持全宽压住底下的画布，
          // 面板 200ms 滑入动画期间右侧露出的是这层自己的暗底，不是画布节点。
          // 不加 transition——拖拽调宽时 padding 和面板宽度同帧变化才跟手，
          // 挂了过渡反而会在两者之间裂出缝。
          style={{ paddingRight: chatOpen ? agentPanelWidth : 0 }}
        >
          <StoryboardView />
        </div>
      )}

      {/* 第 1 层：抽屉面板，定位在 68px 宽的侧边栏间隙右侧，浮动布局，
          上下留白对称，这样顶栏不会盖住面板标题，下方的视觉间距也和
          上方对齐。故事板模式下它们是画布的工具，不该露出来。 */}
      {view === 'canvas' && (
        <div className="absolute top-14 bottom-14 left-[68px] flex gap-2 z-30">
          <HistoryPanel open={historyOpen} projectId={pid} onClose={() => setHistoryOpen(false)} />
          <LibraryPanel open={libraryOpen} projectId={pid} onClose={() => setLibraryOpen(false)} />
        </div>
      )}

      {/* 第 2 层：浮动的胶囊形侧边栏。同上，故事板模式下隐藏。 */}
      {view === 'canvas' && (
        <Sidebar
          projectId={pid}
          activeTool={activeTool}
          onToolChange={setActiveTool}
          historyOpen={historyOpen}
          onToggleHistory={() => setHistoryOpen((v) => !v)}
          libraryOpen={libraryOpen}
          onToggleLibrary={() => setLibraryOpen((v) => !v)}
        />
      )}

      {/* 第 3 层：浮动顶栏——左侧 logo 组没有背景（TapNow 设计）；右侧组
          是一个毛玻璃胶囊，跟侧边栏的表面风格一致。这里用内联样式来
          绕开 preflight: false。 */}
      <div className="absolute top-3 left-4 right-4 flex items-center gap-3 z-40 pointer-events-none">
        <div className="flex items-center gap-2 pointer-events-auto">
          {/* 跳首页走 requestNavigate：编辑器离开前有未保存确认，不能用裸 Link 绕过 */}
          <button
            className="flex items-center gap-2 p-0 bg-transparent border-none cursor-pointer"
            onClick={() => requestNavigate('/')}
            title="回到首页"
          >
            <BrandLogo size={28} />
            <div className="text-foreground text-sm font-semibold tracking-tight">
              喵剧AI
            </div>
          </button>
          <button
            className="flex items-center gap-1.5 px-2 py-1 text-sidebar-foreground hover:text-foreground text-xs rounded bg-transparent border-none cursor-pointer"
            onClick={() => requestNavigate('/canvas')}
          >
            <ArrowLeft size={14} />
            返回
          </button>
          <div className="h-5 w-px bg-accent" />
          {nameDraft !== null ? (
            <input
              ref={nameInputRef}
              aria-label="项目名称"
              autoFocus
              value={nameDraft}
              onFocus={(e) => e.currentTarget.select()}
              onChange={(e) => setNameDraft(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') e.currentTarget.blur();
                if (e.key === 'Escape') {
                  renameEscapedRef.current = true;
                  e.currentTarget.blur();
                }
              }}
              onBlur={(e) => {
                if (renameEscapedRef.current) {
                  renameEscapedRef.current = false;
                  renameClosedRef.current = true;
                  setNameDraft(null);
                  return;
                }
                commitRename(e.currentTarget.value);
              }}
              className="text-foreground text-sm bg-transparent rounded px-1 py-0.5 w-[220px] outline-none"
              style={{ borderWidth: 1, borderStyle: 'solid', borderColor: 'rgba(255, 255, 255, 0.25)' }}
            />
          ) : (
            <button
              type="button"
              className="text-sidebar-foreground hover:text-foreground text-sm truncate max-w-[280px] bg-transparent border-none cursor-pointer px-0 text-left"
              title={projectQuery.data ? `${projectQuery.data.name}（点击重命名）` : ''}
              onClick={() => {
                // 项目还没加载出来时占位显示「项目 #id」，此时没有可编辑的旧名，不进编辑态。
                if (!projectQuery.data) return;
                renameClosedRef.current = false;
                setNameDraft(projectQuery.data.name);
              }}
            >
              {projectQuery.data?.name ?? `项目 #${pid}`}
            </button>
          )}
          <div className="h-5 w-px bg-accent" />
          {/* 视图切换：只放图标，名字走 hover tooltip。顶栏左侧寸土寸金——
              项目名本来就可能很长，两个按钮各带两个汉字会把它挤到省略号。 */}
          <div className="flex items-center gap-0.5 p-0.5 rounded-lg" style={{ backgroundColor: 'rgba(28, 28, 28, 0.95)' }}>
            <Tooltip title="工作流" placement="bottom">
              <button
                type="button"
                aria-label="工作流"
                aria-pressed={view === 'canvas'}
                onClick={() => setView('canvas')}
                className={`flex items-center justify-center w-7 h-7 rounded-md border-none cursor-pointer ${
                  view === 'canvas' ? 'bg-accent text-foreground' : 'bg-transparent text-sidebar-foreground'
                }`}
              >
                <Waypoints size={15} />
              </button>
            </Tooltip>
            <Tooltip title="故事板" placement="bottom">
              <button
                type="button"
                aria-label="故事板"
                aria-pressed={view === 'storyboard'}
                onClick={() => setView('storyboard')}
                className={`flex items-center justify-center w-7 h-7 rounded-md border-none cursor-pointer ${
                  view === 'storyboard' ? 'bg-accent text-foreground' : 'bg-transparent text-sidebar-foreground'
                }`}
              >
                <LayoutPanelLeft size={15} />
              </button>
            </Tooltip>
          </div>
        </div>

        <div className="flex-1" />

        <div
          className="flex items-center gap-3 px-4 py-2 rounded-xl text-sm text-sidebar-foreground shadow-xl shadow-black/40 pointer-events-auto"
          style={{
            backgroundColor: 'rgba(28, 28, 28, 0.95)',
            borderWidth: 1,
            borderStyle: 'solid',
            borderColor: 'rgba(255, 255, 255, 0.10)',
            backdropFilter: 'blur(24px)',
            WebkitBackdropFilter: 'blur(24px)',
          }}
        >
          <span>
            积分 <span className={balanceCredits < 0 ? 'text-red-400' : 'text-primary'}>{Math.round(balanceCredits)}</span>
          </span>
          <span className="h-3.5 w-px bg-accent" />
          <span className="flex items-center gap-1">
            <Save size={14} />
            {lastSyncedAt
              ? `已保存 ${new Date(lastSyncedAt).toLocaleTimeString()}`
              : '尚未保存'}
          </span>
          <span className="h-3.5 w-px bg-accent" />
          <PublishSharePopover
            open={popoverOpen}
            onOpenChange={setPopoverOpen}
            onPickSubmit={async () => {
              setPopoverOpen(false);
              // 先保存，这样发布/分享出去的快照能包含用户在点击前刚做的、
              // 还没保存的编辑（两个出口共用这条前置动作）。
              await saveNow();
              setSubmitOpen(true);
            }}
            onPickShare={async () => {
              setPopoverOpen(false);
              await saveNow();
              setShareOpen(true);
            }}
          >
            <button
              type="button"
              className="flex items-center gap-1 text-sidebar-foreground hover:text-foreground bg-transparent border-none cursor-pointer text-sm"
              title="发布与分享"
            >
              <Share2 size={14} />
            </button>
          </PublishSharePopover>
        </div>

        {/* Agent 入口——对标上游 Tapnow：顶栏最右侧的独立按钮（原来是左侧
            dock 里的「AI 对话」图标）。顶栏在故事板视图也在，但 AgentPanel
            自己是 fixed z-[900]，盖得过故事板覆盖层（z-30），两个视图都能用。 */}
        <button
          type="button"
          onClick={() => setChatOpen((v) => !v)}
          className={`flex items-center gap-1.5 px-4 py-2 rounded-xl text-sm font-semibold text-foreground cursor-pointer shadow-xl shadow-black/40 pointer-events-auto transition-colors ${
            chatOpen ? 'bg-accent' : 'bg-[rgba(28,28,28,0.95)] hover:bg-accent'
          }`}
          style={{
            borderWidth: 1,
            borderStyle: 'solid',
            borderColor: 'rgba(255, 255, 255, 0.10)',
            backdropFilter: 'blur(24px)',
            WebkitBackdropFilter: 'blur(24px)',
          }}
        >
          <BotMessageSquare size={16} />
          Agent
        </button>
      </div>

      {/* 在线成员头像条（团队 only）。个人项目连组件都不挂载——名册永远是空的，
          挂着只会让人以为个人画布也有协作这回事。
          故事板模式下也不挂：故事板是覆盖全屏的第二视图(z-30，内容 pt-14)，
          头像条 z-40 会压在它的顶部内容上；同为协作提示的节点角标已经在故事板
          模式隐藏(NodeShell)，两者取舍必须一致。 */}
      {isTeamIdentity && view !== 'storyboard' && (
        <PresenceBar
          // 团队身份下 /me 返回的是影子用户（team-xxx@team.internal），只有
          // contexts 的 personalEmail 是真人邮箱——拿错就会把自己标成别人。
          selfEmail={contextsQuery.data?.personalEmail ?? userQuery.data?.email}
        />
      )}

      {/* 观看模式横幅。z-50 压在顶栏(z-40)之上——它承载的是「你现在不能编辑」
          这个全局状态，被任何浮层盖住都会让用户对着一张点不动的画布发懵。
          居中偏下让开顶栏胶囊，不抢项目名/积分那一行的位置。 */}
      {isObserving && (
        <div
          data-observe-banner
          className="absolute top-16 left-1/2 -translate-x-1/2 z-50 flex items-center gap-3 px-4 py-2 rounded-xl text-sm text-foreground shadow-xl shadow-black/40"
          style={{
            backgroundColor: 'rgba(28, 28, 28, 0.95)',
            borderWidth: 1,
            borderStyle: 'solid',
            borderColor: 'rgba(255, 255, 255, 0.10)',
            backdropFilter: 'blur(24px)',
            WebkitBackdropFilter: 'blur(24px)',
          }}
        >
          <span className="text-primary">◉</span>
          <span>
            {editorLabel} 正在编辑，你在观看
          </span>
          <Button size="small" type="primary" loading={takingOver} onClick={takeOverEditing}>
            接管编辑
          </Button>
        </div>
      )}

      {agentEverOpened && (
        <AgentPanel
          projectId={pid}
          jwt={getToken() ?? ''}
          open={chatOpen}
          width={agentPanelWidth}
          onWidthChange={setAgentPanelWidth}
          onClose={() => setChatOpen(false)}
          onArtifact={onAgentArtifact}
        />
      )}

      <Lightbox />
      {/* 小地图是画布的工具，同第 1/2 层一样在故事板模式下隐藏——它自己
          fixed + z-30，DOM 顺序又在覆盖层之后，不隐藏会浮在故事板内容上。 */}
      {view === 'canvas' && <Minimap />}

      {/* 快捷键提示同 Minimap 一个 bug 家族：自己 fixed + z-40（比故事板覆盖层
          的 z-30 还高），DOM 顺序又在覆盖层之后，不隐藏就会浮在故事板内容
          上方，宣传一堆已经被 data-storyboard-open 停用的画布快捷键。 */}
      {view === 'canvas' && <ShortcutsHint />}

      <PublishWorkflowModal
        open={shareOpen}
        projectId={pid}
        defaultTitle={projectQuery.data?.name ?? ''}
        onClose={() => setShareOpen(false)}
        onPublished={(token) => {
          // 发布后，把分享链接复制到用户剪贴板，并打开「我的工作流」
          // 方便用户管理。
          const url = `${window.location.origin}/share/${token}`;
          try {
            navigator.clipboard?.writeText(url);
            message.success('链接已复制');
          } catch { /* 忽略 */ }
        }}
      />

      <TvShowSubmitModal
        open={submitOpen}
        projectId={pid}
        defaultTitle={projectQuery.data?.name ?? ''}
        onClose={() => setSubmitOpen(false)}
      />

      {/* 失去编辑权提示。同一画布被更新的页面接管后，本页进入只读，必须
          刷新才能重新接管继续编辑（刷新即成为最新页面，重新 claim owner）。
          只在**个人身份**下弹：团队身份失锁走上面的观看模式降级（横幅 +
          实时刷新），不再拿一个关不掉的弹窗把人挡在项目外面。 */}
      <Modal
        open={ownerLost && !isTeamIdentity}
        title="画布已在其他页面继续编辑"
        maskClosable={false}
        closable={false}
        keyboard={false}
        footer={[
          <Button key="refresh" type="primary" onClick={() => window.location.reload()}>
            刷新
          </Button>,
        ]}
      >
        <p>当前页面已失去编辑权（同一画布在更新的页面被打开）。请刷新后继续编辑。</p>
      </Modal>
    </div>
  );
}
