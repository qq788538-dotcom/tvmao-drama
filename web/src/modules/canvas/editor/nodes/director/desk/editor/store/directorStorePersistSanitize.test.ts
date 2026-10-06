import { afterEach, beforeEach, vi } from "vitest";
import { createInitialDirectorState, useDirectorStore } from "./directorStore";
import { setDirectorHostAdapter, type DirectorHostAdapter } from "../io/hostAdapter";
import { storeCaptureDataUrls } from "../io/captureUpload";
import type { DirectorState } from "./directorStore";

// 消毒闸回归：上传挂钩曾漏过 ViewportToolbar 快门路径，dataURL 直接随画布
// 快照落库（线上实拍）。这里钉死两层防线的行为：
// 1) storeCaptureDataUrls 是唯一上传收口（适配器在场必上传、缺席原样透传）；
// 2) 持久化唯一出口把仍是 data: 的截图从写库副本剥掉，内存不受影响。
function createMemoryStorage(): Storage {
  const storage = new Map<string, string>();

  return {
    get length() {
      return storage.size;
    },
    clear: () => storage.clear(),
    getItem: (key) => storage.get(key) ?? null,
    key: (index) => Array.from(storage.keys())[index] ?? null,
    removeItem: (key) => {
      storage.delete(key);
    },
    setItem: (key, value) => {
      storage.set(key, String(value));
    },
  };
}

function createAdapterStub(): { adapter: DirectorHostAdapter; written: unknown[] } {
  const written: unknown[] = [];
  return {
    written,
    adapter: {
      readState: () => null,
      writeState: (state) => {
        written.push(state);
      },
      storeCapture: vi.fn(async (_dataUrl: string, fileName: string) => `https://media.example/${fileName}`),
      storeAssetFile: vi.fn(async () => "https://media.example/asset"),
      resolveAssetUrl: vi.fn(async (url: string) => url),
      listUserModels: vi.fn(async () => []),
      uploadUserModel: vi.fn(async (_file: Blob, fileName: string) => ({
        id: 1,
        name: fileName,
        fileName,
        url: `https://media.example/${fileName}`,
      })),
      deleteUserModel: vi.fn(async () => true),
      listPresetLibrary: vi.fn(async () => []),
    },
  };
}

beforeEach(() => {
  vi.stubGlobal("localStorage", createMemoryStorage());
  setDirectorHostAdapter(null);
  useDirectorStore.getState().openScopedScene(null);
  useDirectorStore.setState({
    ...useDirectorStore.getState(),
    ...createInitialDirectorState(),
    clipboard: [],
    clipboardPasteCount: 0,
    undoStack: [],
    undoBatchDepth: 0,
    undoBatchSnapshot: null,
    undoBatchHasTrackedChanges: false,
  });
});

afterEach(() => {
  setDirectorHostAdapter(null);
  vi.unstubAllGlobals();
});

it("storeCaptureDataUrls：适配器在场逐张上传返回签名 URL，缺席原样透传", async () => {
  const { adapter } = createAdapterStub();
  setDirectorHostAdapter(adapter);
  const urls = await storeCaptureDataUrls(["data:image/png;base64,AA", "data:image/png;base64,BB"], (i) => `c-${i}.png`);
  expect(urls).toEqual(["https://media.example/c-0.png", "https://media.example/c-1.png"]);
  expect(adapter.storeCapture).toHaveBeenCalledTimes(2);

  setDirectorHostAdapter(null);
  const passthrough = await storeCaptureDataUrls(["data:image/png;base64,AA"], () => "x.png");
  expect(passthrough).toEqual(["data:image/png;base64,AA"]);
});

it("持久化副本剥掉 data: 截图与 lastCaptureUrl，https 截图保留，内存不受影响", () => {
  const { adapter, written } = createAdapterStub();
  setDirectorHostAdapter(adapter);

  const cameraId = useDirectorStore.getState().project.cameras[0]?.id;
  expect(cameraId).toBeTruthy();
  // 混入一张漏网的 data: 截图 + 一张已上传的 https 截图（模拟未来某条路径漏挂）。
  useDirectorStore.getState().addCameraCaptures(cameraId, [
    "data:image/png;base64,LEAKED",
    "https://media.example/ok.png?sig=1",
  ]);

  const lastWritten = written[written.length - 1] as DirectorState;
  const cam = lastWritten.project.cameras.find((c) => c.id === cameraId)!;
  const persistedUrls = (cam.captures ?? []).map((c) => c.dataUrl);
  expect(persistedUrls).toEqual(["https://media.example/ok.png?sig=1"]);
  expect(cam.lastCaptureUrl).toBe("https://media.example/ok.png?sig=1");

  // 内存里的镜头列表仍是两张——剥除只发生在写库副本，本次会话不丢用户可见内容。
  const memCam = useDirectorStore.getState().project.cameras.find((c) => c.id === cameraId)!;
  expect((memCam.captures ?? []).length).toBe(2);
});
