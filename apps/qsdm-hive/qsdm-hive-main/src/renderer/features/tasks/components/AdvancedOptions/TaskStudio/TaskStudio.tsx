import React, { FormEvent, useState } from 'react';
import { useQueryClient } from 'react-query';

import { QsdmTaskCatalogManageRequest } from 'models/api/qsdm';
import { QueryKeys } from 'renderer/services';
import { getErrorToDisplay } from 'renderer/utils/error';
import { CloseLine, Icon, UploadLine } from 'vendor/qsdm-styleguide';

type Props = {
  onClose: () => void;
};

const inputClassName =
  'w-full h-10 px-3 border border-finnieTeal/40 rounded-md bg-finnieBlue-light-tertiary text-sm text-white focus:outline-none focus:border-finnieTeal';
const labelClassName = 'flex flex-col gap-1 text-xs text-finnieTeal';

const optionalNumber = (value: string) =>
  value.trim() === '' ? undefined : Number(value);

export function TaskStudio({ onClose }: Props) {
  const queryClient = useQueryClient();
  const [taskId, setTaskId] = useState('');
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [minimumStake, setMinimumStake] = useState('1');
  const [rewardPerRound, setRewardPerRound] = useState('0.05');
  const [roundTime, setRoundTime] = useState('60');
  const [submissionWindow, setSubmissionWindow] = useState('30');
  const [auditWindow, setAuditWindow] = useState('15');
  const [minimumHiveVersion, setMinimumHiveVersion] = useState('');
  const [metadataUrl, setMetadataUrl] = useState('');
  const [sourceUrl, setSourceUrl] = useState('');
  const [iconUrl, setIconUrl] = useState('');
  const [tags, setTags] = useState('qsdm,cell');
  const [active, setActive] = useState(true);
  const [busy, setBusy] = useState(false);
  const [status, setStatus] = useState('');
  const [error, setError] = useState('');

  const runOperation = async (request: QsdmTaskCatalogManageRequest) => {
    setBusy(true);
    setStatus('');
    setError('');
    try {
      const response = await window.main.manageQsdmTaskCatalog(request);
      const actionLabel =
        response.catalogAction === 'catalog-register'
          ? 'Registration'
          : response.catalogAction === 'catalog-update'
          ? 'Update'
          : response.catalogAction === 'catalog-pause'
          ? 'Pause'
          : 'Resume';
      setStatus(
        `${actionLabel} submitted to QSDM consensus. Catalog version ${
          response.catalogVersion || '-'
        } will appear after validator finalization.`
      );
      await queryClient.invalidateQueries([QueryKeys.availableTaskList]);
      setTimeout(() => {
        queryClient.invalidateQueries([QueryKeys.availableTaskList]);
      }, 15000);
    } catch (caughtError) {
      setError(
        getErrorToDisplay(caughtError as Error) ||
          'The catalog action could not be submitted.'
      );
    } finally {
      setBusy(false);
    }
  };

  const publish = (event: FormEvent) => {
    event.preventDefault();
    const normalizedTags = tags
      .split(',')
      .map((tag) => tag.trim().toLowerCase())
      .filter(Boolean);
    runOperation({
      operation: 'publish',
      taskId: taskId.trim(),
      draft: {
        task_id: taskId.trim(),
        name: name.trim(),
        description: description.trim() || undefined,
        active,
        runtime: {
          kind: 'capability',
          capability: 'generic-proof-v1',
          min_hive_version: minimumHiveVersion.trim() || undefined,
          max_memory_mb: 256,
          max_runtime_seconds: 30,
        },
        minimum_stake_amount: optionalNumber(minimumStake),
        reward_per_round: optionalNumber(rewardPerRound),
        round_time: Number(roundTime),
        submission_window: optionalNumber(submissionWindow),
        audit_window: optionalNumber(auditWindow),
        metadata_url: metadataUrl.trim() || undefined,
        source_url: sourceUrl.trim() || undefined,
        icon_url: iconUrl.trim() || undefined,
        tags: normalizedTags,
      },
    });
  };

  const changeState = (operation: 'pause' | 'resume') => {
    runOperation({ operation, taskId: taskId.trim() });
  };

  return (
    <form
      onSubmit={publish}
      className="mt-4 mx-4 border-t border-finnieTeal/30 bg-finnieBlue-light-secondary"
    >
      <div className="flex items-center justify-between h-14 px-4 border-b border-finnieTeal/20">
        <div className="flex items-center gap-3">
          <Icon source={UploadLine} size={20} color="#5ED9D1" />
          <h2 className="text-base font-semibold text-white">Task Studio</h2>
          <span className="text-xs text-yellow-400">Trust: unverified</span>
        </div>
        <button
          type="button"
          onClick={onClose}
          className="w-9 h-9 flex items-center justify-center"
          title="Close Task Studio"
        >
          <Icon source={CloseLine} size={18} color="#FFFFFF" />
        </button>
      </div>

      <div className="grid grid-cols-4 gap-4 p-4">
        <label className={labelClassName}>
          Task ID
          <input
            required
            value={taskId}
            onChange={(event) => setTaskId(event.target.value)}
            pattern="[A-Za-z0-9][A-Za-z0-9._:-]{0,127}"
            className={inputClassName}
          />
        </label>
        <label className={`${labelClassName} col-span-2`}>
          Task name
          <input
            required
            maxLength={120}
            value={name}
            onChange={(event) => setName(event.target.value)}
            className={inputClassName}
          />
        </label>
        <label className={labelClassName}>
          Capability
          <select className={inputClassName} value="generic-proof-v1" disabled>
            <option value="generic-proof-v1">Generic proof v1</option>
          </select>
        </label>

        <label className={`${labelClassName} col-span-4`}>
          Description
          <textarea
            maxLength={2000}
            value={description}
            onChange={(event) => setDescription(event.target.value)}
            className={`${inputClassName} h-20 py-2 resize-none`}
          />
        </label>

        <label className={labelClassName}>
          Minimum stake (CELL)
          <input
            type="number"
            min="0"
            step="0.000000001"
            value={minimumStake}
            onChange={(event) => setMinimumStake(event.target.value)}
            className={inputClassName}
          />
        </label>
        <label className={labelClassName}>
          Reward per round (CELL)
          <input
            type="number"
            min="0"
            step="0.000000001"
            value={rewardPerRound}
            onChange={(event) => setRewardPerRound(event.target.value)}
            className={inputClassName}
          />
        </label>
        <label className={labelClassName}>
          Round length (blocks)
          <input
            required
            type="number"
            min="1"
            step="1"
            value={roundTime}
            onChange={(event) => setRoundTime(event.target.value)}
            className={inputClassName}
          />
        </label>
        <label className={labelClassName}>
          Minimum Hive version
          <input
            placeholder="1.3.65"
            value={minimumHiveVersion}
            onChange={(event) => setMinimumHiveVersion(event.target.value)}
            pattern="[0-9]+\.[0-9]+\.[0-9]+([-+][0-9A-Za-z.-]+)?"
            className={inputClassName}
          />
        </label>

        <label className={labelClassName}>
          Submission window (blocks)
          <input
            type="number"
            min="0"
            step="1"
            value={submissionWindow}
            onChange={(event) => setSubmissionWindow(event.target.value)}
            className={inputClassName}
          />
        </label>
        <label className={labelClassName}>
          Audit window (blocks)
          <input
            type="number"
            min="0"
            step="1"
            value={auditWindow}
            onChange={(event) => setAuditWindow(event.target.value)}
            className={inputClassName}
          />
        </label>
        <label className={`${labelClassName} col-span-2`}>
          Tags
          <input
            value={tags}
            onChange={(event) => setTags(event.target.value)}
            className={inputClassName}
          />
        </label>

        <label className={`${labelClassName} col-span-2`}>
          Metadata URL
          <input
            type="url"
            value={metadataUrl}
            onChange={(event) => setMetadataUrl(event.target.value)}
            className={inputClassName}
          />
        </label>
        <label className={`${labelClassName} col-span-2`}>
          Source URL
          <input
            type="url"
            value={sourceUrl}
            onChange={(event) => setSourceUrl(event.target.value)}
            className={inputClassName}
          />
        </label>
        <label className={`${labelClassName} col-span-3`}>
          Icon URL
          <input
            type="url"
            value={iconUrl}
            onChange={(event) => setIconUrl(event.target.value)}
            className={inputClassName}
          />
        </label>
        <label className="flex items-center gap-3 pt-6 text-sm text-white">
          <input
            type="checkbox"
            checked={active}
            onChange={(event) => setActive(event.target.checked)}
            className="w-4 h-4 accent-[#5ED9D1]"
          />
          Active
        </label>
      </div>

      {(status || error) && (
        <div
          className={`mx-4 mb-3 text-sm ${
            error ? 'text-red-400' : 'text-green-400'
          }`}
        >
          {error || status}
        </div>
      )}

      <div className="flex items-center justify-end gap-3 px-4 pb-4">
        <button
          type="button"
          disabled={busy || !taskId.trim()}
          onClick={() => changeState('pause')}
          className="h-10 px-4 border border-finnieTeal/50 rounded-md text-sm text-white disabled:opacity-40"
        >
          Pause
        </button>
        <button
          type="button"
          disabled={busy || !taskId.trim()}
          onClick={() => changeState('resume')}
          className="h-10 px-4 border border-finnieTeal/50 rounded-md text-sm text-white disabled:opacity-40"
        >
          Resume
        </button>
        <button
          type="submit"
          disabled={busy}
          className="h-10 px-5 rounded-md bg-finnieTeal text-finnieBlue-dark font-semibold text-sm disabled:opacity-40"
        >
          {busy ? 'Submitting...' : 'Publish'}
        </button>
      </div>
    </form>
  );
}
