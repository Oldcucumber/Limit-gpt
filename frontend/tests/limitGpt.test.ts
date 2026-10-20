// SPDX-License-Identifier: GPL-3.0-only
import { describe, expect, test } from 'bun:test';
import { parseReservePercent } from '../src/features/authFiles/limitGpt';

describe('quota reserve percentages', () => {
  test('empty means that window is not managed', () => {
    expect(parseReservePercent('')).toBeNull();
    expect(parseReservePercent('  ')).toBeNull();
  });
  test('accepts zero, decimals and 100', () => {
    expect(parseReservePercent('0')).toBe(0);
    expect(parseReservePercent('20.5')).toBe(20.5);
    expect(parseReservePercent('100')).toBe(100);
  });
  test('rejects invalid or out-of-range values', () => {
    for (const value of ['-1', '101', 'NaN', 'Infinity', 'hello']) {
      expect(() => parseReservePercent(value)).toThrow();
    }
  });
});
