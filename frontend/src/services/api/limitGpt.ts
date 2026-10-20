// SPDX-License-Identifier: GPL-3.0-only
import { apiClient } from './client';
import { normalizeApiBase } from '@/utils/connection';

export interface LimitGptRule {
  enabled: boolean;
  reserve_5h: number | null;
  reserve_7d: number | null;
}

export interface LimitGptResponse {
  rule: LimitGptRule;
  observation: {
    remaining_5h: number | null;
    remaining_7d: number | null;
    checked_at: string;
    error?: string;
  };
}

// Custom plugin routes retain the path declared by the native plugin host.
const endpoint = (apiBase: string) =>
  `${normalizeApiBase(apiBase)}/v0/management/plugins/limit-gpt/rule`;

export const limitGptApi = {
  get: (apiBase: string, name: string) =>
    apiClient.get<LimitGptResponse>(endpoint(apiBase), { params: { name } }),
  save: (apiBase: string, name: string, rule: LimitGptRule) =>
    apiClient.put<LimitGptResponse>(endpoint(apiBase), { name, rule }),
};
