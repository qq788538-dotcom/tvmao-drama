import { test } from 'node:test';
import assert from 'node:assert/strict';
import { listResources } from './list-resources.mjs';

test('没有 resources 返回空', () => {
  assert.deepEqual(listResources({ bundle: {} }), []);
});

test('映射形式按 [src, dest] 输出', () => {
  const conf = { bundle: { resources: { 'resources/python/': 'python/', 'resources/a.txt': 'cfg/a.txt' } } };
  assert.deepEqual(listResources(conf), [
    ['resources/python/', 'python/'],
    ['resources/a.txt', 'cfg/a.txt'],
  ]);
});

test('数组形式报错：zip 打包需要明确目标路径', () => {
  assert.throws(() => listResources({ bundle: { resources: ['a/*'] } }), /映射形式/);
});

test('通配符、绝对路径、.. 报错', () => {
  assert.throws(() => listResources({ bundle: { resources: { 'a/*': 'a/' } } }), /通配符/);
  assert.throws(() => listResources({ bundle: { resources: { a: '/abs' } } }), /目标路径无效/);
  assert.throws(() => listResources({ bundle: { resources: { a: '../up' } } }), /目标路径无效/);
});
