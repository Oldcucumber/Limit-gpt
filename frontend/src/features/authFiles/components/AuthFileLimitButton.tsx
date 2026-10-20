// SPDX-License-Identifier: GPL-3.0-only
import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { Input } from '@/components/ui/Input';
import { Modal } from '@/components/ui/Modal';
import { ToggleSwitch } from '@/components/ui/ToggleSwitch';
import { useAuthStore } from '@/stores';
import { apiClient } from '@/services/api/client';
import { limitGptApi } from '@/services/api/limitGpt';
import { parseReservePercent } from '@/features/authFiles/limitGpt';
import { notifyAuthFilesChanged } from '@/features/authFiles/authFilesEvents';

export function AuthFileLimitButton({
  name,
  disabled,
  onSaved,
}: {
  name: string;
  disabled: boolean;
  onSaved: () => void;
}) {
  const { t } = useTranslation();
  const apiBase = useAuthStore((state) => state.apiBase);
  const managementKey = useAuthStore((state) => state.managementKey);
  const supportsPlugin = useAuthStore((state) => state.supportsPlugin);
  const [open, setOpen] = useState(false);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [loaded, setLoaded] = useState(false);
  const [enabled, setEnabled] = useState(false);
  const [reserve5h, setReserve5h] = useState('');
  const [reserve7d, setReserve7d] = useState('');
  const [error, setError] = useState('');
  const [queryError, setQueryError] = useState(false);
  const generation = useRef(0);

  useEffect(() => {
    let cancelled = false;
    queueMicrotask(() => {
      if (!cancelled) {
        setOpen(false);
        setLoaded(false);
      }
    });
    return () => {
      cancelled = true;
      generation.current += 1;
    };
  }, [apiBase, managementKey, name]);

  const load = async () => {
    const current = ++generation.current;
    const revision = apiClient.getConnectionRevision();
    const isCurrent = () =>
      generation.current === current && apiClient.getConnectionRevision() === revision;
    setOpen(true);
    setLoading(true);
    setLoaded(false);
    setError('');
    setQueryError(false);
    try {
      const result = await limitGptApi.get(apiBase, name);
      if (!isCurrent()) return;
      setEnabled(result.rule.enabled);
      setReserve5h(result.rule.reserve_5h == null ? '' : String(result.rule.reserve_5h));
      setReserve7d(result.rule.reserve_7d == null ? '' : String(result.rule.reserve_7d));
      setQueryError(Boolean(result.observation.error));
      setLoaded(true);
    } catch {
      if (isCurrent()) setError(t('limit_gpt.load_failed'));
    } finally {
      if (isCurrent()) setLoading(false);
    }
  };

  const save = async () => {
    let five: number | null;
    let seven: number | null;
    try {
      five = parseReservePercent(reserve5h);
      seven = parseReservePercent(reserve7d);
    } catch {
      setError(t('limit_gpt.invalid_percent'));
      return;
    }
    const current = generation.current;
    const revision = apiClient.getConnectionRevision();
    const isCurrent = () =>
      generation.current === current && apiClient.getConnectionRevision() === revision;
    setSaving(true);
    setError('');
    try {
      await limitGptApi.save(apiBase, name, { enabled, reserve_5h: five, reserve_7d: seven });
      if (!isCurrent()) return;
      setOpen(false);
      notifyAuthFilesChanged();
      onSaved();
    } catch {
      if (isCurrent()) setError(t('limit_gpt.save_failed'));
    } finally {
      if (isCurrent()) setSaving(false);
    }
  };

  if (!supportsPlugin) return null;

  return (
    <>
      <Button variant="secondary" size="sm" onClick={() => void load()} disabled={disabled}>
        {t('limit_gpt.button')}
      </Button>
      <Modal
        open={open}
        title={t('limit_gpt.title')}
        width={400}
        closeDisabled={saving}
        onClose={() => {
          generation.current += 1;
          setOpen(false);
        }}
        footer={
          <>
            <Button variant="secondary" onClick={() => setOpen(false)} disabled={saving}>
              {t('common.cancel')}
            </Button>
            <Button onClick={() => void save()} disabled={!loaded || loading} loading={saving}>
              {t('common.save')}
            </Button>
          </>
        }
      >
        <p className="hint" style={{ overflowWrap: 'anywhere' }}>
          {name}
        </p>
        <div className="form-group">
          <ToggleSwitch
            checked={enabled}
            onChange={setEnabled}
            label={t('limit_gpt.enabled')}
            disabled={!loaded || saving}
          />
        </div>
        <Input
          label={t('limit_gpt.reserve_5h')}
          type="number"
          min={0}
          max={100}
          step="any"
          value={reserve5h}
          onChange={(event) => setReserve5h(event.target.value)}
          disabled={!loaded || saving}
          placeholder={t('limit_gpt.unset')}
        />
        <Input
          label={t('limit_gpt.reserve_7d')}
          type="number"
          min={0}
          max={100}
          step="any"
          value={reserve7d}
          onChange={(event) => setReserve7d(event.target.value)}
          disabled={!loaded || saving}
          placeholder={t('limit_gpt.unset')}
        />
        <p className="hint">{t('limit_gpt.hint')}</p>
        {queryError && <p className="hint">{t('limit_gpt.query_failed')}</p>}
        {loading && <p className="hint">{t('common.loading')}</p>}
        {error && (
          <div className="error-box" role="alert">
            {error}
          </div>
        )}
      </Modal>
    </>
  );
}
