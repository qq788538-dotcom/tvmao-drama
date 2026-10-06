import { afterEach, beforeEach, vi } from "vitest";
import { CROWD_CHARACTER_LIMIT, createInitialDirectorState, useDirectorStore } from "./directorStore";

// 与 directorStore.test.ts 同一套隔离手法：内存 localStorage + 每条用例重置
// store——群演闸测试会真的往 store 里灌 100 个对象并触发持久化，不隔离会把
// 巨型场景写进共享 storage 污染其他用例。
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

beforeEach(() => {
  vi.stubGlobal("localStorage", createMemoryStorage());
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
  vi.unstubAllGlobals();
});

it("allows adding crowd characters exactly up to the limit", () => {
  const createdIds = useDirectorStore.getState().addCrowdCharacters({ rows: 10, columns: 10, spacing: 1 });

  expect(createdIds).toHaveLength(CROWD_CHARACTER_LIMIT);
  expect(
    useDirectorStore.getState().project.objects.filter((object) => object.crowdId)
  ).toHaveLength(CROWD_CHARACTER_LIMIT);
});

it("rejects crowd characters beyond the limit without mutating the scene", () => {
  const createdIds = useDirectorStore.getState().addCrowdCharacters({ rows: 10, columns: 10, spacing: 1 });
  expect(createdIds).toHaveLength(CROWD_CHARACTER_LIMIT);

  const objectCountAtLimit = useDirectorStore.getState().project.objects.length;

  const rejectedIds = useDirectorStore.getState().addCrowdCharacters({ rows: 1, columns: 1, spacing: 1 });

  expect(rejectedIds).toEqual([]);
  expect(useDirectorStore.getState().project.objects).toHaveLength(objectCountAtLimit);
});
