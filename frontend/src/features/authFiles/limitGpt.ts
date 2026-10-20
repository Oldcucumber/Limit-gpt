// SPDX-License-Identifier: GPL-3.0-only
export const parseReservePercent = (value: string): number | null => {
  if (!value.trim()) return null;
  const number = Number(value);
  if (!Number.isFinite(number) || number < 0 || number > 100) {
    throw new Error('invalid_percent');
  }
  return number;
};
