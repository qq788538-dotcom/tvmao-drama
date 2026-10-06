import { getDirectorHostAdapter } from "../io/hostAdapter";

// .glb 是单文件二进制可直接放开；.gltf 仍拒——多文件格式（外部 bin/贴图），
// 单文件上传必然缺资源破图。
const LOCAL_MODEL_EXTENSION_RE = /\.(fbx|obj|glb)$/i;

function readFileAsDataUrl(file: File) {
  return new Promise<string>((resolve, reject) => {
    const reader = new FileReader();

    reader.addEventListener("load", () => {
      if (typeof reader.result === "string") {
        resolve(reader.result);
        return;
      }

      reject(new Error("模型文件读取失败"));
    });
    reader.addEventListener("error", () => reject(reader.error ?? new Error("模型文件读取失败")));
    reader.readAsDataURL(file);
  });
}

export async function readLocalModelFile(file: File) {
  if (!LOCAL_MODEL_EXTENSION_RE.test(file.name)) {
    throw new Error("当前仅支持 FBX / OBJ / GLB 模型文件");
  }

  // 宿主在场时模型走**用户级云端模型库**（uploadUserModel，跨项目复用、上限
  // 服务端控制），不走项目级 storeAssetFile——项目级文件会随删项目被清理，
  // 别的项目还引用着就断了。dataURL 只是无宿主时的独立运行回落（dataURL 进
  // 场景 JSON 随快照落库会把快照写爆，宿主在场绝不允许）。上限/大小校验都在
  // 适配器实现侧，超限 reject 原样向上抛，调用方既有 catch 负责展示。
  const adapter = getDirectorHostAdapter();
  if (adapter) {
    const model = await adapter.uploadUserModel(file, file.name);
    return {
      id: crypto.randomUUID(),
      fileName: model.fileName,
      name: model.name,
      url: model.url,
    };
  }

  return {
    id: crypto.randomUUID(),
    fileName: file.name,
    name: file.name.replace(LOCAL_MODEL_EXTENSION_RE, ""),
    url: await readFileAsDataUrl(file),
  };
}
