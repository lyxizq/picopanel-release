// 拖拽排序语义单测（node --test 运行：cd web && node --test tests/）
import test from 'node:test'
import assert from 'node:assert/strict'
import { moveById } from '../src/utils/dragReorder.js'

const L = (id, group_id = 1) => ({ id, group_id })

test('向下拖：A 拖到 C 上 = 落到 C 之后', () => {
  const r = moveById([L(1), L(2), L(3)], 1, 3)
  assert.deepEqual(r.arr.map((x) => x.id), [2, 3, 1])
})

test('向上拖：C 拖到 A 上 = 插到 A 之前', () => {
  const r = moveById([L(1), L(2), L(3)], 3, 1)
  assert.deepEqual(r.arr.map((x) => x.id), [3, 1, 2])
})

test('相邻拖拽（两个方向）= 交换', () => {
  assert.deepEqual(moveById([L(1), L(2)], 1, 2).arr.map((x) => x.id), [2, 1])
  assert.deepEqual(moveById([L(1), L(2)], 2, 1).arr.map((x) => x.id), [2, 1])
})

test('跨组：归属取目标项的组', () => {
  const r = moveById([L(1, 1), L(2, 1), L(3, 2)], 1, 3, (t) => t.group_id)
  assert.deepEqual(r.arr.map((x) => [x.id, x.group_id]), [[2, 1], [3, 2], [1, 2]])
  assert.equal(r.group, 2)
})

test('同 id / 找不到：返回 null', () => {
  assert.equal(moveById([L(1)], 1, 1), null)
  assert.equal(moveById([L(1)], 9, 1), null)
})
