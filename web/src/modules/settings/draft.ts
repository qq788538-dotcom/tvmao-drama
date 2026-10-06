export type DraftPath = (string | number)[];

/** 不可变地替换 path 处的值：路径上的对象/数组浅拷贝，其余分支复用原引用，未知字段原样保留。 */
export function updateAt<T>(obj: T, path: DraftPath, value: unknown): T {
  if (path.length === 0) return value as T;
  const [head, ...rest] = path;
  if (Array.isArray(obj)) {
    const copy = obj.slice();
    copy[head as number] = updateAt(copy[head as number], rest, value);
    return copy as unknown as T;
  }
  const base = (obj ?? {}) as Record<string, unknown>;
  return { ...base, [head]: updateAt(base[head as string], rest, value) } as T;
}

export function deepClone<T>(v: T): T {
  return JSON.parse(JSON.stringify(v)) as T;
}
