import axios, { type AxiosError } from 'axios';
import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { App as AntApp, Button, Result, Tooltip } from 'antd';
import { BotMessageSquare, LayoutGrid, LayoutPanelLeft, Save, Settings, Waypoints } from 'lucide-react';
import { BrandLogo } from '@/shared/BrandLogo';
import { CanvasEditor } from '@/modules/canvas/editor/CanvasEditor';
import { StoryboardView } from '@/modules/canvas/storyboard/StoryboardView';
import { Sidebar } from '@/modules/canvas/editor/Sidebar';
import { Lightbox } from '@/modules/canvas/editor/Lightbox';
import { Minimap } from '@/modules/canvas/editor/Minimap';
import { ShortcutsHint } from '@/modules/canvas/editor/ShortcutsHint';
import { useCanvasStore } from '@/modules/canvas/editor/store';
import {
  configureCanvasPersistence,
  onCanvasOwnerLost,
  persistNodeById,
  retireCanvasPersistence,
  seedCanvasBaselines,
  setCanvasOwnerToken,
  waitForCanvasMutationsIdle,
} from '@/modules/canvas/editor/persistence/canvasMutations';
import { useExternalChanges } from '@/modules/canvas/editor/hooks/useExternalChanges';
import { loadLocalView } from '@/modules/canvas/editor/persistence/localView';
import {
  type CanvasViewMode,
  loadCanvasViewMode,
  saveCanvasViewMode,
} from '@/modules/canvas/editor/persistence/localCanvasView';
import {
  type Snapshot,
  getProject,
  getSnapshot,
  listRuntimeModels,
  updateProject,
} from '@/shared/api';
import { sidecarToken } from '@/shared/sidecarSession';
import { saveLastProjectId } from '@/desktop/lastProject';
import type { CanvasSnapshot } from '@/modules/canvas/editor/types';
import { HistoryPanel } from '@/modules/canvas/panels/HistoryPanel';
import { AgentPanel, loadAgentPanelWidth, saveAgentPanelWidth } from '@/modules/canvas/editor/agent/AgentPanel';
import { useAgentHandoff } from '@/modules/canvas/editor/agent/agentHandoff';
import { agentDropPointFor, claimAgentSlot } from '@/modules/canvas/editor/agent/dropPoint';
import { applyCanvasOpArtifact, isCanvasOpArtifact } from '@/modules/canvas/editor/agent/canvasOps';
import { LibraryPanel } from '@/modules/canvas/panels/LibraryPanel';
import { openProjects } from '@/modules/canvas/projectsModalStore';
import { openSettings } from '@/modules/settings/settingsModalStore';
import '../../styles/canvas.css';

// 桌面端单用户、单窗口（spec X2、S11）：没有画布编辑锁，也就没有 claim / 心跳 /
// 观看模式。但 canvasMutations 仍要求一个非空 ownerToken 才会发结构性 mutation
// （空 token 时整批静默丢弃），所以给一个固定值。sidecar 的 owner 校验恒通过，
// 只把它当普通字段收下。
export const LOCAL_OWNER_TOKEN = 'local';

export function CanvasEditorPage() {
  const navigate = useNavigate();
  const { projectId } = useParams<{ projectId: string }>();
  const pid = Number(projectId);
  const load = useCanvasStore((s) => s.load);
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
  // useExternalChanges 在下方调用，先用 ref 把本地回执转发过去
  const noteLocalVersionRef = useRef<(version: number) => void>(() => {});
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
      // 不执行生成（铁律：生成必须用户逐节点手动点）。
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

  const projectQuery = useQuery({
    queryKey: ['project', pid],
    queryFn: () => getProject(pid),
    // 每次进画布都真正 GET 一次：后端借此刷新 updated_at（「所有项目」最近打开排第一），
    // 30s 内来回切项目也不能吃缓存跳过。
    refetchOnMount: 'always',
    enabled: Number.isFinite(pid) && pid > 0,
  });

  const snapshotQuery = useQuery<Snapshot, AxiosError>({
    queryKey: ['snapshot', pid],
    // 先等上一棵树留下的 mutation 落定再拉快照（sidecar 重启重挂时会有），否则快照里
    // 没有这些修改，load() 会把它们从 store 里覆盖掉。
    queryFn: async () => {
      await waitForCanvasMutationsIdle();
      return getSnapshot(pid);
    },
    enabled: Number.isFinite(pid) && pid > 0,
    refetchOnWindowFocus: false,
    staleTime: Infinity,
    retry: (failureCount, error) => {
      const status = error.response?.status;
      // 401 = 本地服务 token 对不上（sidecar 换过会话），自动重试也不会好，直接进错误页。
      if (status === 401 || status === 403 || status === 404) return false;
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
    // 配置局部 mutation 持久化（版本回写只用来刷新「已保存」时间戳），并且必须在
    // setReady(true) 之前做完：画布一露出来用户就能改，这时持久化还没配好的话，
    // 结构性 op 会因为没有 ownerToken 被静默丢弃。
    // 顺序要紧：先 configure 再设 token。configureCanvasPersistence 在换项目时会兜底
    // resetCanvasPersistence()，而那会把 ownerToken 清空——先设 token 就等于交给
    // reset 抹掉。
    configureCanvasPersistence({
      projectID: pid,
      onVersion: (v) => {
        setLastSyncedAt(new Date().toISOString());
        // 本地写入也会推高服务端版本，告诉外部变更订阅，免得重连补漏时多整拉一次
        noteLocalVersionRef.current(v);
      },
      onError: () => message.error('保存失败'),
    });
    setCanvasOwnerToken(LOCAL_OWNER_TOKEN);
    loadedProjectRef.current = pid;
    setReady(true);
    // 记住「上一次打开的项目」，下次启动直接进它（spec F9）。放在这里而不是挂在
    // ready 上：只有这个项目的快照真的载入了才记——打不开的项目（已删除 / 不存在）
    // 不该被当成上一次；同一页面里 pid 变了而 ready 还是 true 时也不会提前记。
    saveLastProjectId(pid);
  }, [snapshotQuery.data, load, pid, ready, message]);

  // 本地 sidecar 的 owner 校验恒放行（spec X2），正常不会失权。万一被拒（实现偏差、
  // 以后重新引入锁），canvasMutations 只发事件、不走 onError——没人听的话结构性改动
  // 会被静默丢弃，「已保存」时间戳停住，用户以为保存了。至少弹个提示。
  useEffect(
    () => onCanvasOwnerLost(() => message.error('保存失败：本地服务拒绝写入，请重新打开画布')),
    [message],
  );

  // 离开画布时清干净持久化运行态（owner-token、心跳队列、debounce 计时器、
  // 事件监听）。切项目由 configureCanvasPersistence 内部处理，这里兜卸载。
  // 卸载时先把 debounce 中的 patch flush 进队列再 reset：sidecar 自动重启时整棵树会被
  // SidecarGate 卸掉，这些修改不能丢；已入队的任务会等拦截器拿到新端口后照常发出。
  useEffect(() => () => retireCanvasPersistence(), []);

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

  // CLI 改了当前画布：空闲时整拉（CLI spec 第 6 节）
  const { noteLocalVersion } = useExternalChanges({
    projectId: pid,
    enabled: ready,
    initialVersion: snapshotQuery.data?.version ?? 0,
    onReloaded: () => setLastSyncedAt(new Date().toISOString()),
  });
  noteLocalVersionRef.current = noteLocalVersion;

  if (!Number.isFinite(pid) || pid <= 0) {
    return <div className="p-8 text-white">无效的项目 ID</div>;
  }

  // 403/404 由上面的 effect 跳回项目列表，跳转前这一帧仍显示 Loading。
  if (snapshotQuery.isPending || snapshotStatus === 403 || snapshotStatus === 404) {
    return <div className="p-8 text-white">Loading...</div>;
  }

  if (snapshotQuery.isError) {
    // 401 不能再归进 Loading：参考仓靠「401 → 跳登录」兜底，桌面端没有登录，
    // 归进 Loading 就成了没有出口的死胡同。
    const sessionLost = snapshotStatus === 401;
    return (
      <Result
        status="error"
        title={sessionLost ? '本地服务会话已失效' : '加载画布失败'}
        subTitle={
          sessionLost
            ? '本地服务可能刚重启过。请点「重试」；仍然失败的话，请重启应用或运行环境诊断。'
            : '请点「重试」；仍然失败的话，请运行环境诊断查看原因。'
        }
        extra={[
          <Button key="retry" type="primary" onClick={() => void snapshotQuery.refetch()}>
            重试
          </Button>,
          <Button key="diag" onClick={() => navigate('/diag')}>
            环境诊断
          </Button>,
        ]}
      />
    );
  }

  if (!ready) {
    return <div className="p-8 text-white">Loading...</div>;
  }

  return (
    <div className="canvas-root">
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
          <div className="flex items-center gap-2">
            <BrandLogo size={28} />
            <div className="text-foreground text-sm font-semibold tracking-tight">
              喵剧AI
            </div>
          </div>
          {/* 「所有项目」：开项目弹框（ProjectsModal），不离开画布；切到别的项目时由弹框负责 flush。 */}
          <button
            className="flex items-center gap-1.5 px-2 py-1 text-sidebar-foreground hover:text-foreground text-xs rounded bg-transparent border-none cursor-pointer"
            onClick={() => openProjects()}
          >
            <LayoutGrid size={14} />
            所有项目
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
          <span className="flex items-center gap-1">
            <Save size={14} />
            {lastSyncedAt
              ? `已保存 ${new Date(lastSyncedAt).toLocaleTimeString()}`
              : '尚未保存'}
          </span>
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

        {/* 「设置」（P4）：放在顶栏最右侧、Agent 之后。上游地址与 Key、模型中心、画布配置、
            数据目录、环境诊断。开弹框，不离开画布，所以不需要 flush。 */}
        <button
          type="button"
          onClick={() => openSettings()}
          className="flex items-center gap-1.5 px-4 py-2 rounded-xl text-sm font-semibold text-foreground cursor-pointer shadow-xl shadow-black/40 pointer-events-auto transition-colors bg-[rgba(28,28,28,0.95)] hover:bg-accent"
          style={{
            borderWidth: 1,
            borderStyle: 'solid',
            borderColor: 'rgba(255, 255, 255, 0.10)',
            backdropFilter: 'blur(24px)',
            WebkitBackdropFilter: 'blur(24px)',
          }}
        >
          <Settings size={16} />
          设置
        </button>
      </div>

      {agentEverOpened && (
        <AgentPanel
          projectId={pid}
          jwt={sidecarToken()}
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
    </div>
  );
}
