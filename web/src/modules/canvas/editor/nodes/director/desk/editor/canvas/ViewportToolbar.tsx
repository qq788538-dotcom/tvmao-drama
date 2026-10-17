import {
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type CSSProperties,
  type ChangeEvent,
  type MutableRefObject,
} from "react";
import { mediaCrossOrigin } from "../../../../../utils/mediaCrossOrigin";
import {
  Box,
  Boxes,
  Camera,
  ChevronRight,
  Expand,
  Grid2X2,
  Grid3X3,
  Image,
  ImagePlus,
  Move3D,
  Plus,
  Ratio,
  Rotate3D,
  Scale3D,
  Trash2,
  UserPlus,
  Video,
  X,
  type LucideIcon,
} from "lucide-react";
import { storeCaptureDataUrls } from "../io/captureUpload";
import {
  getDirectorHostAdapter,
  type DirectorPresetCategory,
  type DirectorUserModel,
} from "../io/hostAdapter";
import { requestViewportCapture } from "../io/captureBridge";
import { readLocalModelFile } from "../loaders/localModelImport";
import { readPanoramaFile } from "../loaders/panoramaImport";
import {
  getModelLibraryItems,
  MODEL_LIBRARY_CATEGORIES,
  type ModelLibraryCategoryId,
  type ModelLibraryItem,
} from "../modelLibrary/modelLibraryCatalog";
import {
  VIEWPORT_ASPECT_RATIO_OPTIONS,
  type ViewportAspectRatio,
} from "../schema/viewportAspectRatio";
import { BODY_TYPE_OPTIONS, type CharacterBodyType } from "../runtime/mannequin/bodyTypes";
import { GEOMETRY_PRIMITIVE_OPTIONS, type GeometryPrimitiveType } from "../schema/directorProject";
import {
  CROWD_CHARACTER_LIMIT,
  useDirectorStore,
  type CameraShotSnapshot,
  type CrowdCharactersInput,
  type TransformMode,
} from "../store/directorStore";

type ToolbarAction = {
  label: string;
  icon: LucideIcon;
  mode?: TransformMode;
  onClick: () => void;
};

const DEFAULT_VIEWPORT_TOOLBAR_HEIGHT = 46;
const VIEWPORT_TOOLBAR_BOTTOM_OFFSET = 40;
const DEFAULT_CROWD_ROWS = 3;
const DEFAULT_CROWD_COLUMNS = 3;
const DEFAULT_CROWD_SPACING = 1.2;
// 「我的模型」每用户上限（仅展示用）。真源是服务端 directormodels.UserModelLimit
// 的硬编码 30——两处同属一次发版，改上限时一起改；不为一个数字加拉取接口。
const USER_MODEL_LIMIT = 30;
const MIN_CROWD_GRID_SIZE = 1;
const MAX_CROWD_GRID_SIZE = 12;
const MIN_CROWD_SPACING = 0.1;
const MAX_CROWD_SPACING = 10;

function clampCrowdGridSize(value: number) {
  if (!Number.isFinite(value)) return MIN_CROWD_GRID_SIZE;
  return Math.min(MAX_CROWD_GRID_SIZE, Math.max(MIN_CROWD_GRID_SIZE, Math.round(value)));
}

function clampCrowdSpacing(value: number) {
  if (!Number.isFinite(value)) return DEFAULT_CROWD_SPACING;
  return Math.min(MAX_CROWD_SPACING, Math.max(MIN_CROWD_SPACING, Number(value.toFixed(2))));
}

function waitForNextAnimationFrame() {
  return new Promise<void>((resolve) => {
    requestAnimationFrame(() => resolve());
  });
}

export function ViewportToolbar({
  getViewportCameraSnapshot,
  toolbarContainerRef,
}: {
  getViewportCameraSnapshot?: () => CameraShotSnapshot;
  toolbarContainerRef?: MutableRefObject<HTMLDivElement | null>;
}) {
  const toolbarRef = useRef<HTMLDivElement | null>(null);
  const aspectRatioPanelRef = useRef<HTMLDivElement | null>(null);
  const characterTriggerRef = useRef<HTMLButtonElement | null>(null);
  const geometryTriggerRef = useRef<HTMLButtonElement | null>(null);
  const crowdTriggerRef = useRef<HTMLButtonElement | null>(null);
  const modelLibraryTriggerRef = useRef<HTMLButtonElement | null>(null);
  const characterMenuRef = useRef<HTMLDivElement | null>(null);
  const geometryMenuRef = useRef<HTMLDivElement | null>(null);
  const crowdPanelRef = useRef<HTMLDivElement | null>(null);
  const modelLibraryPanelRef = useRef<HTMLDivElement | null>(null);
  const sceneLocalModelInputRef = useRef<HTMLInputElement | null>(null);
  const libraryLocalModelInputRef = useRef<HTMLInputElement | null>(null);
  const panoramaInputRef = useRef<HTMLInputElement | null>(null);
  const [characterMenuOpen, setCharacterMenuOpen] = useState(false);
  const [geometryMenuOpen, setGeometryMenuOpen] = useState(false);
  const [crowdPanelOpen, setCrowdPanelOpen] = useState(false);
  const [modelLibraryOpen, setModelLibraryOpen] = useState(false);
  const [aspectRatioPanelOpen, setAspectRatioPanelOpen] = useState(false);
  const [toolbarHeight, setToolbarHeight] = useState(DEFAULT_VIEWPORT_TOOLBAR_HEIGHT);
  const [characterMenuStyle, setCharacterMenuStyle] = useState<CSSProperties>({});
  const [geometryMenuStyle, setGeometryMenuStyle] = useState<CSSProperties>({});
  const [crowdPanelStyle, setCrowdPanelStyle] = useState<CSSProperties>({});
  const [modelLibraryPanelStyle, setModelLibraryPanelStyle] = useState<CSSProperties>({});
  const [crowdBodyType, setCrowdBodyType] = useState<CharacterBodyType>(BODY_TYPE_OPTIONS[0]?.bodyType ?? "mannequin");
  // 群演总数超限（store 层的闸返回空数组）时展示在面板里的错误文案。
  // 放本地 state 而不是 store：这是一次性操作反馈，不属于场景状态，不该进持久化。
  const [crowdLimitError, setCrowdLimitError] = useState<string | null>(null);
  const [crowdRows, setCrowdRows] = useState(String(DEFAULT_CROWD_ROWS));
  const [crowdColumns, setCrowdColumns] = useState(String(DEFAULT_CROWD_COLUMNS));
  const [crowdSpacing, setCrowdSpacing] = useState(String(DEFAULT_CROWD_SPACING));
  // 类型是 string 而非上游的 ModelLibraryCategoryId 字面量联合：宿主在场时
  // tab id 来自后台分类（"preset-<id>"，可增删），联合类型在这里是错的收窄。
  const [activeModelLibraryCategoryId, setActiveModelLibraryCategoryId] =
    useState<string>("convenience");
  const addImportedAsset = useDirectorStore((state) => state.addImportedAsset);
  const addObjectFromAsset = useDirectorStore((state) => state.addObjectFromAsset);
  const removeImportedAsset = useDirectorStore((state) => state.removeImportedAsset);
  const assets = useDirectorStore((state) => state.project.assets);
  const addPresetCharacter = useDirectorStore((state) => state.addPresetCharacter);
  const addCrowdCharacters = useDirectorStore((state) => state.addCrowdCharacters);
  const addGeometryPrimitive = useDirectorStore((state) => state.addGeometryPrimitive);
  const addCameraShot = useDirectorStore((state) => state.addCameraShot);
  const addCameraCaptures = useDirectorStore((state) => state.addCameraCaptures);
  const activeCameraId = useDirectorStore((state) => state.project.activeCameraId);
  const viewMode = useDirectorStore((state) => state.viewMode);
  const transformMode = useDirectorStore((state) => state.transformMode);
  const viewportAspectRatio = useDirectorStore((state) => state.viewportAspectRatio);
  const setViewMode = useDirectorStore((state) => state.setViewMode);
  const setTransformMode = useDirectorStore((state) => state.setTransformMode);
  const setViewportAspectRatio = useDirectorStore((state) => state.setViewportAspectRatio);
  const toggleViewportPanelsCollapsed = useDirectorStore((state) => state.toggleViewportPanelsCollapsed);

  useEffect(() => {
    if (!characterMenuOpen && !crowdPanelOpen && !modelLibraryOpen && !aspectRatioPanelOpen) return;

    function closeMenusOnOutsidePointerDown(event: PointerEvent) {
      if (event.target instanceof Node && toolbarRef.current?.contains(event.target)) return;
      if (event.target instanceof Node && characterMenuRef.current?.contains(event.target)) return;
      if (event.target instanceof Node && geometryMenuRef.current?.contains(event.target)) return;
      if (event.target instanceof Node && crowdPanelRef.current?.contains(event.target)) return;
      if (event.target instanceof Node && modelLibraryPanelRef.current?.contains(event.target)) return;
      if (event.target instanceof Node && aspectRatioPanelRef.current?.contains(event.target)) return;
      if (event.target instanceof Node && sceneLocalModelInputRef.current?.contains(event.target)) return;
      if (event.target instanceof Node && libraryLocalModelInputRef.current?.contains(event.target)) return;
      if (event.target instanceof Node && panoramaInputRef.current?.contains(event.target)) return;

      setCharacterMenuOpen(false);
      setGeometryMenuOpen(false);
      setCrowdPanelOpen(false);
      setModelLibraryOpen(false);
      setAspectRatioPanelOpen(false);
    }

    document.addEventListener("pointerdown", closeMenusOnOutsidePointerDown);

    return () => {
      document.removeEventListener("pointerdown", closeMenusOnOutsidePointerDown);
    };
  }, [aspectRatioPanelOpen, characterMenuOpen, crowdPanelOpen, modelLibraryOpen]);

  useLayoutEffect(() => {
    const element = toolbarRef.current;
    if (!element) return;

    const updateHeight = () => {
      const nextHeight = Math.max(element.offsetHeight, DEFAULT_VIEWPORT_TOOLBAR_HEIGHT);
      setToolbarHeight((currentHeight) => (currentHeight === nextHeight ? currentHeight : nextHeight));
    };

    updateHeight();

    if (typeof ResizeObserver === "undefined") {
      window.addEventListener("resize", updateHeight);
      return () => {
        window.removeEventListener("resize", updateHeight);
      };
    }

    const resizeObserver = new ResizeObserver(updateHeight);
    resizeObserver.observe(element);
    window.addEventListener("resize", updateHeight);

    return () => {
      resizeObserver.disconnect();
      window.removeEventListener("resize", updateHeight);
    };
  }, []);

  useLayoutEffect(() => {
    const toolbarElement = toolbarRef.current;
    const frameElement = toolbarElement?.parentElement;
    if (!toolbarElement || !frameElement) return;

    const updateFloatingPositions = () => {
      const frameRect = frameElement.getBoundingClientRect();

      if (characterMenuOpen && characterTriggerRef.current) {
        const triggerRect = characterTriggerRef.current.getBoundingClientRect();
        setCharacterMenuStyle({
          left: `${triggerRect.left - frameRect.left + triggerRect.width / 2}px`,
          bottom: `${frameRect.bottom - triggerRect.top + 8}px`,
        });
      }

      if (geometryMenuOpen && geometryTriggerRef.current) {
        const triggerRect = geometryTriggerRef.current.getBoundingClientRect();
        setGeometryMenuStyle({
          left: `${triggerRect.right - frameRect.left + 8}px`,
          bottom: `${frameRect.bottom - triggerRect.bottom}px`,
        });
      }

      if (crowdPanelOpen && crowdTriggerRef.current) {
        const triggerRect = crowdTriggerRef.current.getBoundingClientRect();
        setCrowdPanelStyle({
          left: `${triggerRect.right - frameRect.left + 8}px`,
          bottom: `${frameRect.bottom - triggerRect.bottom}px`,
        });
      }

      if (modelLibraryOpen) {
        const toolbarRect = toolbarElement.getBoundingClientRect();
        setModelLibraryPanelStyle({
          left: `${toolbarRect.left - frameRect.left + toolbarRect.width / 2}px`,
          bottom: `${frameRect.bottom - toolbarRect.top + 10}px`,
        });
      }
    };

    updateFloatingPositions();

    if (typeof ResizeObserver === "undefined") {
      window.addEventListener("resize", updateFloatingPositions);
      return () => {
        window.removeEventListener("resize", updateFloatingPositions);
      };
    }

    const resizeObserver = new ResizeObserver(updateFloatingPositions);
    resizeObserver.observe(frameElement);
    resizeObserver.observe(toolbarElement);
    if (characterTriggerRef.current) {
      resizeObserver.observe(characterTriggerRef.current);
    }
    if (geometryTriggerRef.current) {
      resizeObserver.observe(geometryTriggerRef.current);
    }
    if (crowdTriggerRef.current) {
      resizeObserver.observe(crowdTriggerRef.current);
    }
    if (modelLibraryTriggerRef.current) {
      resizeObserver.observe(modelLibraryTriggerRef.current);
    }
    window.addEventListener("resize", updateFloatingPositions);

    return () => {
      resizeObserver.disconnect();
      window.removeEventListener("resize", updateFloatingPositions);
    };
  }, [characterMenuOpen, crowdPanelOpen, geometryMenuOpen, modelLibraryOpen]);

  async function handleLocalModelChange(
    event: ChangeEvent<HTMLInputElement>,
    addToScene: boolean
  ) {
    const input = event.currentTarget;
    const files = Array.from(input.files ?? []);
    if (!files.length) return;

    try {
      for (const file of files) {
        // readLocalModelFile 在宿主在场时已把文件传进用户级云端目录。
        const result = await readLocalModelFile(file);
        if (getDirectorHostAdapter() && !addToScene) {
          // 仅入库：目录才是真源，不再往 project.assets 塞影子条目
          //（塞了会在退化路径的过滤里重复出现，且删目录后变死引用）。
          continue;
        }
        addImportedAsset({
          kind: "prop",
          ...result,
          addToScene,
          assetSource: getDirectorHostAdapter() ? "library" : "local",
        });
      }
      if (getDirectorHostAdapter()) await refreshUserModels();
    } catch {
      // The toolbar keeps file actions quiet; detailed import feedback lives in the side panel.
    } finally {
      input.value = "";
    }
  }

  async function handlePanoramaChange(event: ChangeEvent<HTMLInputElement>) {
    const input = event.currentTarget;
    const file = input.files?.[0];
    if (!file) return;

    try {
      const result = await readPanoramaFile(file);
      addImportedAsset({ kind: "panorama", ...result });
    } catch {
      // The toolbar keeps file actions quiet; detailed import feedback lives in the side panel.
    } finally {
      input.value = "";
    }
  }

  async function handleCapture(preset: "current" | "four" | "twelve") {
    try {
      const targetCameraId =
        viewMode === "director" ? addCameraShot(getViewportCameraSnapshot?.()) : activeCameraId;

      setViewMode("camera");
      await waitForNextAnimationFrame();

      const results = await requestViewportCapture({
        preset,
        source: "camera-panel",
        cameraId: targetCameraId,
      });
      // 必须过统一上传收口（storeCaptureDataUrls）：本路径曾直接入列 dataURL，
      // 随画布快照落库把 payload 撑到 MB 级（线上实拍）。4/12 宫格在这里并发上传。
      const captureUrls = await storeCaptureDataUrls(
        results.map((result) => result.dataUrl),
        (index) => `机位截图-${Date.now()}-${index + 1}.png`,
      );
      addCameraCaptures(targetCameraId, captureUrls);
    } catch {
      // Keep the capsule toolbar icon-only and free of transient status text.
    }
  }

  function selectTransformMode(mode: TransformMode) {
    setTransformMode(mode);
  }

  function toggleCharacterMenu() {
    setCharacterMenuOpen((isOpen) => !isOpen);
    setGeometryMenuOpen(false);
    setCrowdPanelOpen(false);
    setModelLibraryOpen(false);
    setAspectRatioPanelOpen(false);
  }

  function addCharacterWithBodyType(bodyType: CharacterBodyType) {
    addPresetCharacter(bodyType);
    setCharacterMenuOpen(false);
    setGeometryMenuOpen(false);
    setCrowdPanelOpen(false);
  }

  function addGeometryWithType(geometryType: GeometryPrimitiveType) {
    addGeometryPrimitive(geometryType);
    setCharacterMenuOpen(false);
    setGeometryMenuOpen(false);
    setCrowdPanelOpen(false);
  }

  function openCrowdPanel() {
    setCrowdPanelOpen(true);
    setGeometryMenuOpen(false);
    // 重开面板时清掉上次的超限提示，避免陈旧文案误导本次输入。
    setCrowdLimitError(null);
  }

  function closeCrowdPanel() {
    setCrowdPanelOpen(false);
  }

  function getCrowdInputValue(): CrowdCharactersInput {
    return {
      bodyType: crowdBodyType,
      rows: clampCrowdGridSize(Number(crowdRows)),
      columns: clampCrowdGridSize(Number(crowdColumns)),
      spacing: clampCrowdSpacing(Number(crowdSpacing)),
    };
  }

  function applyCrowdValueDrafts(input: CrowdCharactersInput) {
    setCrowdRows(String(input.rows));
    setCrowdColumns(String(input.columns));
    setCrowdSpacing(String(input.spacing));
  }

  function addCrowd() {
    const nextInput = getCrowdInputValue();
    applyCrowdValueDrafts(nextInput);
    const createdIds = addCrowdCharacters(nextInput);

    // store 层的总数闸（CROWD_CHARACTER_LIMIT）超限时返回空数组。此时保持面板
    // 打开并就地展示原因——静默不加会让用户以为点击没生效而反复重试。
    if (nextInput.rows * nextInput.columns > 0 && createdIds.length === 0) {
      const existingCrowdCount = useDirectorStore
        .getState()
        .project.objects.filter((object) => object.crowdId).length;
      setCrowdLimitError(`群演总数上限 ${CROWD_CHARACTER_LIMIT}，当前已有 ${existingCrowdCount} 名`);
      return;
    }

    setCrowdLimitError(null);
    setCharacterMenuOpen(false);
    setGeometryMenuOpen(false);
    setCrowdPanelOpen(false);
  }

  // 用户级云端「我的模型」目录（宿主在场时的数据源）。null=尚未加载。
  // 只在面板打开时拉取，上传/删除后各自主动 refresh。
  const [userModels, setUserModels] = useState<DirectorUserModel[] | null>(null);
  // 平台预置目录（后台配置的分类+模型）。分类 tab 按它动态渲染，
  // 上游写死的四个分类只在无宿主独立运行时回落使用。
  const [presetLibrary, setPresetLibrary] = useState<DirectorPresetCategory[] | null>(null);

  async function refreshPresetLibrary() {
    const adapter = getDirectorHostAdapter();
    if (!adapter) return;
    try {
      const categories = await adapter.listPresetLibrary();
      setPresetLibrary(categories);
      // 当前激活 tab 不在新目录里（首开时默认值是上游静态 id、或分类刚被
      // 后台删除）→ 吸附到第一个分类，没有分类就落「我的模型」。
      setActiveModelLibraryCategoryId((current) =>
        current === "my-models" || categories.some((category) => category.id === current)
          ? current
          : categories[0]?.id ?? "my-models"
      );
    } catch {
      // 拉取失败保持上次内容；目录是增强不是阻塞项。
    }
  }

  async function refreshUserModels() {
    const adapter = getDirectorHostAdapter();
    if (!adapter) return;
    try {
      setUserModels(await adapter.listUserModels());
    } catch {
      // 列表拉取失败保持上次内容；面板静默策略与文件操作一致。
    }
  }

  function toggleModelLibrary() {
    if (!modelLibraryOpen && getDirectorHostAdapter()) {
      void refreshUserModels();
      void refreshPresetLibrary();
    }
    setModelLibraryOpen((isOpen) => !isOpen);
    setCharacterMenuOpen(false);
    setGeometryMenuOpen(false);
    setCrowdPanelOpen(false);
    setAspectRatioPanelOpen(false);
  }

  function addModelLibraryItem(item: ModelLibraryItem) {
    addImportedAsset({
      kind: "prop",
      assetSource: "library",
      fileName: item.fileName,
      name: item.name,
      url: item.url,
    });
    setModelLibraryOpen(false);
  }

  async function handleMyModelsImport() {
    libraryLocalModelInputRef.current?.click();
  }

  // 宿主在场：「我的模型」= 用户级云端目录（跨项目共享）；
  // 独立运行回落：仍列当前场景 project.assets 里的本地导入资产。
  const myModelLibraryItems: ModelLibraryItem[] = getDirectorHostAdapter()
    ? (userModels ?? []).map(
        (model) =>
          ({
            categoryId: "my-models",
            fileName: model.fileName,
            id: String(model.id),
            name: model.name,
            thumbUrl: undefined,
            url: model.url,
          }) satisfies ModelLibraryItem
      )
    : assets
        .filter((asset) => asset.sourceType === "model" && asset.assetSource === "local")
        .map(
          (asset) =>
            ({
              categoryId: "my-models",
              fileName: asset.fileName,
              id: asset.id,
              name: asset.name ?? asset.fileName.replace(/\.(fbx|obj)$/i, ""),
              thumbUrl: undefined,
              url: asset.url,
            }) satisfies ModelLibraryItem
        );

  function addCameraFromViewport() {
    const snapshot = getViewportCameraSnapshot?.();
    addCameraShot(snapshot);
  }

  function toggleAspectRatioPanel() {
    setAspectRatioPanelOpen((isOpen) => !isOpen);
    setCharacterMenuOpen(false);
    setGeometryMenuOpen(false);
    setCrowdPanelOpen(false);
    setModelLibraryOpen(false);
  }

  function selectAspectRatio(ratio: ViewportAspectRatio) {
    setViewportAspectRatio(ratio);
    setAspectRatioPanelOpen(false);
  }

  const actions: ToolbarAction[] = [
    { label: "移动", icon: Move3D, mode: "translate", onClick: () => selectTransformMode("translate") },
    { label: "旋转", icon: Rotate3D, mode: "rotate", onClick: () => selectTransformMode("rotate") },
    { label: "缩放", icon: Scale3D, mode: "scale", onClick: () => selectTransformMode("scale") },
    { label: "导入全景图", icon: ImagePlus, onClick: () => panoramaInputRef.current?.click() },
    {
      label: "导入本地模型",
      icon: Box,
      onClick: () => {
        sceneLocalModelInputRef.current?.click();
      },
    },
    { label: "模型库", icon: Boxes, onClick: toggleModelLibrary },
    { label: "添加机位", icon: Video, onClick: addCameraFromViewport },
    { label: "选择画幅比例", icon: Ratio, onClick: toggleAspectRatioPanel },
    { label: "当前视角截图", icon: Camera, onClick: () => void handleCapture("current") },
    { label: "四方位截图", icon: Grid2X2, onClick: () => void handleCapture("four") },
    { label: "十二方位截图", icon: Grid3X3, onClick: () => void handleCapture("twelve") },
    { label: "全屏", icon: Expand, onClick: toggleViewportPanelsCollapsed },
  ];

  function renderActionButton(action: ToolbarAction) {
    const Icon = action.icon;
    const active = action.mode ? transformMode === action.mode : false;

    return (
      <button
        key={action.label}
        aria-label={action.label}
        aria-pressed={action.mode ? active : undefined}
        className={`ui-icon-button viewport-toolbar-button${active ? " is-active" : ""}`}
        type="button"
        onClick={action.onClick}
      >
        <Icon aria-hidden="true" size={17} strokeWidth={1.9} />
        <span className="viewport-toolbar-label">{action.label}</span>
      </button>
    );
  }

  const modelLibraryItems = getModelLibraryItems();
  // 宿主在场：分类 tab = 后台数据（可增删改名）+「我的模型」固定尾项；
  // 独立运行：上游静态四分类。二者都以 {id,label} 收敛供 tab 渲染。
  const hostPresent = Boolean(getDirectorHostAdapter());
  const modelLibraryTabs: Array<{ id: string; label: string }> = hostPresent
    ? [
        ...(presetLibrary ?? []).map((category) => ({ id: category.id, label: category.label })),
        { id: "my-models", label: "我的模型" },
      ]
    : MODEL_LIBRARY_CATEGORIES.map((category) => ({ id: category.id, label: category.label }));
  const activeModelLibraryItems =
    activeModelLibraryCategoryId === "my-models"
      ? myModelLibraryItems
      : hostPresent
        ? ((presetLibrary ?? [])
            .find((category) => category.id === activeModelLibraryCategoryId)
            ?.items.map(
              (item) =>
                ({
                  // 仅为满足 ModelLibraryItem 类型的占位；卡片特判基于激活 tab，
                  // 不读 item.categoryId。
                  categoryId: "convenience",
                  fileName: item.fileName,
                  id: item.id,
                  name: item.name,
                  thumbUrl: item.thumbUrl,
                  url: item.url,
                }) satisfies ModelLibraryItem
            ) ?? [])
        : modelLibraryItems.filter((item) => item.categoryId === activeModelLibraryCategoryId);
  const crowdInputValue = getCrowdInputValue();
  const crowdTotalCount = crowdInputValue.rows * crowdInputValue.columns;

  function setToolbarElement(element: HTMLDivElement | null) {
    toolbarRef.current = element;
    if (toolbarContainerRef) {
      toolbarContainerRef.current = element;
    }
  }

  const aspectRatioPanelStyle = {
    "--viewport-toolbar-height": `${toolbarHeight}px`,
  } as CSSProperties;

  return (
    <>
      <div className="viewport-toolbar" role="group" aria-label="3D视口快捷工具" ref={setToolbarElement}>
        {actions.slice(0, 3).map(renderActionButton)}
        <div className="viewport-toolbar-menu-wrap">
          <button
            aria-expanded={characterMenuOpen}
            aria-label="添加角色"
            className="ui-icon-button viewport-toolbar-button"
            ref={characterTriggerRef}
            type="button"
            onClick={toggleCharacterMenu}
          >
            <UserPlus aria-hidden="true" size={17} strokeWidth={1.9} />
            <span className="viewport-toolbar-label">添加角色</span>
          </button>
        </div>
        {actions.slice(3).map((action) => {
          if (action.label !== "模型库") {
            return renderActionButton(action);
          }

          const Icon = action.icon;

          return (
            <button
              key={action.label}
              aria-label={action.label}
              className="ui-icon-button viewport-toolbar-button"
              ref={modelLibraryTriggerRef}
              type="button"
              onClick={action.onClick}
            >
              <Icon aria-hidden="true" size={17} strokeWidth={1.9} />
              <span className="viewport-toolbar-label">{action.label}</span>
            </button>
          );
        })}
      </div>
      {characterMenuOpen ? (
        <div
          ref={characterMenuRef}
          className="viewport-toolbar-menu"
          role="menu"
          aria-label="选择角色体型"
          style={characterMenuStyle}
        >
          {BODY_TYPE_OPTIONS.map((option) => (
            <button
              key={option.bodyType}
              role="menuitem"
              type="button"
              onClick={() => addCharacterWithBodyType(option.bodyType)}
              onMouseEnter={() => {
                setGeometryMenuOpen(false);
                setCrowdPanelOpen(false);
              }}
            >
              {option.label}
            </button>
          ))}
          <div
            className="viewport-toolbar-submenu-wrap"
            onMouseEnter={openCrowdPanel}
          >
            <button
              ref={crowdTriggerRef}
              aria-expanded={crowdPanelOpen}
              aria-haspopup="dialog"
              className="viewport-toolbar-menu-subtrigger"
              role="menuitem"
              type="button"
              onFocus={openCrowdPanel}
              onMouseEnter={openCrowdPanel}
            >
              <span>群众 (3x3)</span>
              <ChevronRight aria-hidden="true" size={14} strokeWidth={1.8} />
            </button>
          </div>
          <div
            className="viewport-toolbar-submenu-wrap"
            onMouseEnter={() => {
              setGeometryMenuOpen(true);
              setCrowdPanelOpen(false);
            }}
          >
            <button
              ref={geometryTriggerRef}
              aria-expanded={geometryMenuOpen}
              aria-haspopup="menu"
              className="viewport-toolbar-menu-subtrigger"
              role="menuitem"
              type="button"
              onMouseEnter={() => {
                setGeometryMenuOpen(true);
                setCrowdPanelOpen(false);
              }}
            >
              <span>几何模型</span>
              <ChevronRight aria-hidden="true" size={14} strokeWidth={1.8} />
            </button>
          </div>
        </div>
      ) : null}
      {crowdPanelOpen ? (
        <div
          ref={crowdPanelRef}
          className="viewport-toolbar-crowd-panel"
          role="dialog"
          aria-label="添加群众阵列"
          style={crowdPanelStyle}
        >
          <div className="viewport-toolbar-crowd-panel-header">
            <h2 className="viewport-toolbar-crowd-panel-title">添加群众阵列</h2>
            <span className="viewport-toolbar-crowd-panel-count">共{crowdTotalCount}人</span>
          </div>
          <div className="viewport-toolbar-crowd-grid">
            <label className="viewport-toolbar-crowd-field">
              <span>行数</span>
              <input
                className="ui-field"
                aria-label="群众行数"
                inputMode="numeric"
                type="number"
                min={MIN_CROWD_GRID_SIZE}
                max={MAX_CROWD_GRID_SIZE}
                value={crowdRows}
                onChange={(event) => setCrowdRows(event.currentTarget.value)}
              />
            </label>
            <span className="viewport-toolbar-crowd-separator" aria-hidden="true">
              ×
            </span>
            <label className="viewport-toolbar-crowd-field">
              <span>列数</span>
              <input
                className="ui-field"
                aria-label="群众列数"
                inputMode="numeric"
                type="number"
                min={MIN_CROWD_GRID_SIZE}
                max={MAX_CROWD_GRID_SIZE}
                value={crowdColumns}
                onChange={(event) => setCrowdColumns(event.currentTarget.value)}
              />
            </label>
            <label className="viewport-toolbar-crowd-field viewport-toolbar-crowd-field-spacing">
              <span>间距</span>
              <input
                className="ui-field"
                aria-label="群众间距"
                inputMode="decimal"
                type="number"
                min={MIN_CROWD_SPACING}
                max={MAX_CROWD_SPACING}
                step="0.1"
                value={crowdSpacing}
                onChange={(event) => setCrowdSpacing(event.currentTarget.value)}
              />
            </label>
          </div>
          {crowdLimitError ? (
            <span className="viewport-toolbar-crowd-panel-count" role="alert">
              {crowdLimitError}
            </span>
          ) : null}
          <div className="viewport-toolbar-crowd-actions">
            <button className="viewport-toolbar-crowd-cancel camera-capture-clear-all" type="button" onClick={closeCrowdPanel}>
              取消
            </button>
            <button
              aria-label="添加群众"
              className="viewport-toolbar-crowd-confirm camera-capture-send-all"
              type="button"
              onClick={addCrowd}
            >
              添加
            </button>
          </div>
        </div>
      ) : null}
      {geometryMenuOpen ? (
        <div
          ref={geometryMenuRef}
          className="viewport-toolbar-submenu"
          role="menu"
          aria-label="选择几何模型"
          style={geometryMenuStyle}
        >
          {GEOMETRY_PRIMITIVE_OPTIONS.map((option) => (
            <button
              key={option.type}
              role="menuitem"
              type="button"
              onClick={() => addGeometryWithType(option.type)}
            >
              {option.label}
            </button>
          ))}
        </div>
      ) : null}
      {modelLibraryOpen ? (
        <div
          ref={modelLibraryPanelRef}
          className="model-library-panel"
          role="dialog"
          aria-label="模型库"
          style={modelLibraryPanelStyle}
        >
          <div className="model-library-header">
            <h2 className="model-library-title">模型库</h2>
            <button
              aria-label="关闭模型库"
              className="top-bar-action-button model-library-close-button"
              type="button"
              onClick={() => setModelLibraryOpen(false)}
            >
              <X aria-hidden="true" size={16} strokeWidth={1.8} />
            </button>
          </div>
          <div className="model-library-tabs" role="tablist" aria-label="模型分类">
            {modelLibraryTabs.map((category) => {
              const active = category.id === activeModelLibraryCategoryId;

              return (
                <button
                  key={category.id}
                  aria-selected={active}
                  className={`model-library-tab${active ? " is-active" : ""}`}
                  role="tab"
                  type="button"
                  onClick={() => setActiveModelLibraryCategoryId(category.id)}
                >
                  {category.label}
                </button>
              );
            })}
          </div>
          {activeModelLibraryCategoryId === "my-models" && hostPresent && (
            <div className="model-library-limit-hint">
              已上传 {myModelLibraryItems.length} / {USER_MODEL_LIMIT} 个模型，达到上限后需删除才能继续上传
            </div>
          )}
          {activeModelLibraryCategoryId === "my-models" && activeModelLibraryItems.length === 0 ? (
            <div className="model-library-empty-state object-search-empty-state" role="status" aria-label="暂无任何模型">
              <span className="object-search-empty-icon" data-testid="my-models-empty-icon">
                <Boxes aria-hidden="true" size={16} strokeWidth={1.8} />
              </span>
              <span>暂无任何模型</span>
              <button className="top-bar-action-button model-library-empty-action" type="button" onClick={() => void handleMyModelsImport()}>
                本地导入
              </button>
            </div>
          ) : (
            <div className="model-library-grid" role="list" aria-label="模型列表">
              {activeModelLibraryItems.map((item) => (
                activeModelLibraryCategoryId === "my-models" ? (
                  <div key={item.id} className="model-library-card-wrap">
                    <button
                      aria-label={`添加模型 ${item.name}`}
                      className="model-library-card"
                      type="button"
                      onClick={() => {
                        addObjectFromAsset(item.id);
                        setModelLibraryOpen(false);
                      }}
                    >
                      <span className="model-library-thumb" aria-hidden="true">
                        {item.thumbUrl ? (
                          <img
                            alt=""
                            aria-hidden="true"
                            className="model-library-thumb-image"
                            loading="lazy"
                            src={item.thumbUrl}
                            crossOrigin={mediaCrossOrigin(item.thumbUrl)}
                          />
                        ) : (
                          <Boxes size={24} strokeWidth={1.6} />
                        )}
                      </span>
                      <span className="model-library-name">{item.name}</span>
                    </button>
                    <button
                      aria-label={`删除模型 ${item.name}`}
                      className="model-library-card-delete"
                      type="button"
                      onClick={() => {
                        const adapter = getDirectorHostAdapter();
                        if (adapter) {
                          // 引用检查与「有 N 个项目正在使用」确认弹窗都在适配器
                          // 实现侧；true=删成即刷目录，false=用户取消不动。
                          void adapter.deleteUserModel(Number(item.id)).then((deleted) => {
                            if (deleted) void refreshUserModels();
                          });
                          return;
                        }
                        removeImportedAsset(item.id);
                      }}
                    >
                      <Trash2 aria-hidden="true" size={14} strokeWidth={1.9} />
                    </button>
                  </div>
                ) : (
                  <button
                    key={item.id}
                    aria-label={`添加模型 ${item.name}`}
                    className="model-library-card"
                    type="button"
                    onClick={() => {
                      addModelLibraryItem(item);
                    }}
                  >
                    <span className="model-library-thumb" aria-hidden="true">
                      {item.thumbUrl ? (
                        <img
                          alt=""
                          aria-hidden="true"
                          className="model-library-thumb-image"
                          loading="lazy"
                          src={item.thumbUrl}
                          crossOrigin={mediaCrossOrigin(item.thumbUrl)}
                        />
                      ) : (
                        <Boxes size={24} strokeWidth={1.6} />
                      )}
                    </span>
                    <span className="model-library-name">{item.name}</span>
                  </button>
                )
              ))}
              {activeModelLibraryCategoryId === "my-models" ? (
                <button
                  aria-label="本地导入"
                  className="model-library-card model-library-import-card"
                  type="button"
                  onClick={() => void handleMyModelsImport()}
                >
                  <span className="model-library-thumb model-library-thumb-import" aria-hidden="true">
                    <Plus size={28} strokeWidth={1.8} />
                  </span>
                  <span className="model-library-name">本地导入</span>
                </button>
              ) : null}
            </div>
          )}
        </div>
      ) : null}
      {aspectRatioPanelOpen ? (
        <div
          ref={aspectRatioPanelRef}
          className="viewport-aspect-panel"
          role="dialog"
          aria-label="比例"
          style={aspectRatioPanelStyle}
        >
          <h2 className="viewport-aspect-panel-title">比例</h2>
          <div className="viewport-aspect-panel-grid" role="group" aria-label="画幅比例选项">
            {VIEWPORT_ASPECT_RATIO_OPTIONS.map((option) => {
              const active = option.id === viewportAspectRatio;
              const frameClassName = `viewport-aspect-option-frame viewport-aspect-option-frame-${option.id.replace(":", "-")}`;

              return (
                <button
                  key={option.id}
                  aria-pressed={active}
                  className={`viewport-aspect-option${active ? " is-active" : ""}`}
                  type="button"
                  onClick={() => selectAspectRatio(option.id)}
                >
                  <span className={frameClassName} aria-hidden="true" />
                  <span className="viewport-aspect-option-label">{option.label}</span>
                </button>
              );
            })}
          </div>
        </div>
      ) : null}
      <input
        ref={panoramaInputRef}
        aria-hidden="true"
        className="hidden-file-input"
        tabIndex={-1}
        accept=".jpg,.jpeg,.png,.webp"
        type="file"
        onChange={(event) => void handlePanoramaChange(event)}
      />
      <input
        ref={sceneLocalModelInputRef}
        aria-hidden="true"
        className="hidden-file-input"
        data-testid="scene-local-model-input"
        tabIndex={-1}
        accept=".fbx,.obj,.glb"
        type="file"
        onChange={(event) => void handleLocalModelChange(event, true)}
      />
      <input
        ref={libraryLocalModelInputRef}
        aria-hidden="true"
        className="hidden-file-input"
        data-testid="library-local-model-input"
        tabIndex={-1}
        accept=".fbx,.obj,.glb"
        multiple
        type="file"
        onChange={(event) => void handleLocalModelChange(event, false)}
      />
    </>
  );
}
